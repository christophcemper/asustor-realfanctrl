// realfanctrld — adaptive fan daemon for ASUSTOR ADM NAS units.
//
// Author:     Christoph C. Cemper / Magosol Kft.
// Repository: https://github.com/christophcemper/asustor-realfanctrl
// License:    MIT — see LICENSE.
//
// ---------------------------------------------------------------------------
// WARNING — NO GUARANTEES, LIMITED TESTING
//
// This software drives cooling hardware. It is provided AS IS, without warranty
// of any kind, and it has NOT undergone extended or broad testing.
//
// It has been developed and tested on exactly ONE machine type:
//
//	ASUSTOR FS6812X / AS6812F / FS6812X (Flashstor 12 Pro), ADM 5.1.3.RI81, 12x NVMe, x86_64.
//
// Behaviour on any other ASUSTOR model, ADM release, fan controller or drive
// combination is UNVERIFIED. The fan curves, the PWM range, the M.2 slot map
// and the assumption that `fanctrl` reaches the MCU are all specific to that
// hardware. A wrong curve, an unsupported MCU or a misread sensor can leave a
// machine under-cooled, risking hardware damage and data loss.
//
// After installing, verify with `realfanctrl status` that the temperatures look
// sane and that fan RPM actually responds, then keep monitoring. Use at your
// own risk.
// ---------------------------------------------------------------------------
//
// ADM's emboardmand derives fan PWM from each NVMe drive's Composite sensor and
// clamps the result to a per-model table — on the AS6812F / FS6812X that ceiling is 58 for
// fan 0 and 80 for fan 1, out of 255, even when its own logic has escalated to
// the maximum severity. Meanwhile each drive's "Sensor 1" hot spot runs 15-25 C
// above Composite and is never consulted.
//
// realfanctrld reads every temperature the kernel exposes, maps the hottest per group
// through a configurable curve, and re-asserts the result via fanctrl. It does
// not replace emboardmand (which also owns LEDs, buttons, power schedules and
// disk hibernation); it simply writes more often than emboardmand walks its own
// target, so the fans settle within a few PWM of the requested value.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	hwmonRoot   = "/sys/class/hwmon"
	fanctrlPath = "/usr/sbin/fanctrl"
	defaultConf = "/usr/local/etc/realfanctrl.conf"
)

// Populated at build time by the Makefile via -ldflags -X.
var (
	version = "dev"
	commit  = "none"
	built   = "unknown"
)

// Point is one vertex of a fan curve: at Temp degrees C, run at PWM (0-255).
type Point struct {
	Temp int `json:"temp"`
	PWM  int `json:"pwm"`
}

// Curve is a piecewise-linear temperature-to-PWM mapping, sorted by Temp.
type Curve []Point

// PWMFor interpolates the curve, clamping to the first and last points.
func (c Curve) PWMFor(t int) int {
	if len(c) == 0 {
		return 0
	}
	if t <= c[0].Temp {
		return c[0].PWM
	}
	last := c[len(c)-1]
	if t >= last.Temp {
		return last.PWM
	}
	for i := 1; i < len(c); i++ {
		if t <= c[i].Temp {
			lo, hi := c[i-1], c[i]
			span := hi.Temp - lo.Temp
			if span <= 0 {
				return hi.PWM
			}
			return lo.PWM + (t-lo.Temp)*(hi.PWM-lo.PWM)/span
		}
	}
	return last.PWM
}

type Config struct {
	IntervalSec int   `json:"interval_sec"`
	FanIDs      []int `json:"fan_ids"`
	MinPWM      int   `json:"min_pwm"`
	MaxPWM      int   `json:"max_pwm"`
	// FallbackPWM is used when no sensor in any group can be read.
	FallbackPWM int `json:"fallback_pwm"`
	// CooldownStep caps how far PWM may drop per cycle, so falling
	// temperatures produce a smooth spin-down instead of a step.
	CooldownStep int `json:"cooldown_step"`
	// LogDelta suppresses log lines until PWM moves by at least this much.
	LogDelta     int   `json:"log_delta"`
	HeartbeatMin int   `json:"heartbeat_min"`
	SSDCurve     Curve `json:"ssd_curve"`
	CPUCurve     Curve `json:"cpu_curve"`
	NICCurve     Curve `json:"nic_curve"`
	// SlotMap labels drives by physical bay, keyed on PCI address. ADM keeps
	// this mapping compiled into emboardmand; the defaults below were read off
	// an AS6812F / FS6812X via `emboardmand -debug`. PCI address is used rather than the
	// nvmeN name because it is fixed by the wiring, not by probe order.
	SlotMap map[string]string `json:"slot_map"`
	// IgnoreSensors excludes individual sensors from the curves, for drives
	// that expose an unimplemented register as a constant bogus temperature.
	// Entries are "<slot>:<label>", "<device>:<label>" or "<chip>:<label>",
	// e.g. "M.2-8:Sensor 2". Ignored sensors are still shown by -status.
	IgnoreSensors []string `json:"ignore_sensors"`
}

// ignored reports whether a sensor is excluded from the fan curves.
func (c Config) ignored(r Reading) bool {
	for _, pat := range c.IgnoreSensors {
		pat = strings.ToLower(strings.TrimSpace(pat))
		if pat == "" {
			continue
		}
		for _, key := range []string{r.Slot, r.Device, r.Chip} {
			if key == "" {
				continue
			}
			if strings.ToLower(key+":"+r.Label) == pat {
				return true
			}
		}
	}
	return false
}

// defaultConfig is tuned from measurements on cccnas6: 12x T-FORCE TM8FFH004T
// in RAID 6, whose Sensor 1 readings sat 15-25 C above Composite under load.
// Curves are keyed on the hottest sensor in each group, not on Composite.
func defaultConfig() Config {
	return Config{
		IntervalSec:  2,
		FanIDs:       []int{0, 1},
		MinPWM:       60,
		MaxPWM:       255,
		FallbackPWM:  200,
		CooldownStep: 2,
		LogDelta:     3,
		HeartbeatMin: 15,
		SSDCurve: Curve{
			{55, 60}, {65, 80}, {75, 120}, {82, 165}, {88, 210}, {93, 255},
		},
		CPUCurve: Curve{
			{60, 60}, {75, 90}, {85, 140}, {92, 200}, {97, 255},
		},
		NICCurve: Curve{
			{70, 60}, {85, 120}, {95, 200}, {100, 255},
		},
		SlotMap: map[string]string{
			"0000:01:00.0": "M.2-1",
			"0000:08:00.0": "M.2-2",
			"0000:0d:00.0": "M.2-3",
			"0000:0e:00.0": "M.2-4",
			"0000:0b:00.0": "M.2-5",
			"0000:0c:00.0": "M.2-6",
			"0000:09:00.0": "M.2-7",
			"0000:0a:00.0": "M.2-8",
			"0000:07:00.0": "M.2-9",
			"0000:06:00.0": "M.2-10",
			"0000:05:00.0": "M.2-11",
			"0000:04:00.0": "M.2-12",
		},
		// Empty rather than nil so -write-config emits [] and the key is
		// obvious to anyone editing the file by hand.
		IgnoreSensors: []string{},
	}
}

func (c *Config) normalize() {
	if c.IntervalSec < 1 {
		c.IntervalSec = 1
	}
	if len(c.FanIDs) == 0 {
		c.FanIDs = []int{0, 1}
	}
	if c.MaxPWM <= 0 || c.MaxPWM > 255 {
		c.MaxPWM = 255
	}
	if c.MinPWM < 0 {
		c.MinPWM = 0
	}
	if c.MinPWM > c.MaxPWM {
		c.MinPWM = c.MaxPWM
	}
	if c.CooldownStep < 1 {
		c.CooldownStep = 1
	}
	if c.FallbackPWM <= 0 {
		c.FallbackPWM = 200
	}
	for _, cv := range []Curve{c.SSDCurve, c.CPUCurve, c.NICCurve} {
		sort.Slice(cv, func(i, j int) bool { return cv[i].Temp < cv[j].Temp })
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.normalize()
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", path, err)
	}
	cfg.normalize()
	return cfg, nil
}

func writeConfig(path string, cfg Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ---------------------------------------------------------------------------
// ADM configuration audit
//
// These settings do NOT lift the hardcoded PWM ceiling — that lives in
// emboardmand's per-model table and no config file reaches it (see
// ASUSTOR-DEFECTS.md, DEFECT-001). What they do is make ADM's *own* fallback
// as safe as possible: with FanSpeed=High and Level=3, emboardmand operates
// from its High baseline (PWM ~70) instead of Low (~40), so if realfanctrld is
// ever stopped the box idles less dangerously. FanNumber=2 corrects a factory
// value that claims this chassis has one fan when it has two.
//
// Both files are symlinks into /volume0 (the RAID1 system partition), so these
// edits persist across reboots. confutil is ADM's own writer for them.
// ---------------------------------------------------------------------------

const confutilPath = "/usr/bin/confutil"

type admSetting struct {
	File    string
	Section string
	Key     string
	Want    string
	Why     string
	// Cosmetic marks a setting we recommend for consistency but for which no
	// behavioural change was observed. Never oversell these.
	Cosmetic bool
}

func recommendedSettings() []admSetting {
	return []admSetting{
		{"/etc/nas.conf", "Hardware", "FanNumber", "2",
			"chassis has two fans; ADM ships FanNumber=1", false},
		{"/etc/nas.conf", "Hardware", "IsSmartFan", "No",
			"puts emboardmand in Custom Mode instead of its smart curve", false},
		{"/etc/nas.conf", "Hardware", "FanSpeed", "High",
			"selects ADM's High baseline (PWM ~70) as the fallback, not Low (~40)", false},
		{"/usr/etc/emboard.conf", "Fan1", "Mode", "0",
			"fixed-level mode rather than auto", false},
		{"/usr/etc/emboard.conf", "Fan1", "Level", "3",
			"level 3 = High, consistent with FanSpeed=High", false},
		{"/etc/nas.conf", "Hardware", "HighSpeed", "220",
			"states intent; emboardmand does not consult it (no observed effect)", true},
	}
}

// iniGet reads "Key = Value" from a [Section] in an ADM-style config file.
func iniGet(file, section, key string) (string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	inSection := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inSection = strings.EqualFold(strings.Trim(line, "[]"), section)
			continue
		}
		if !inSection {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.TrimSpace(v), nil
		}
	}
	return "", fmt.Errorf("[%s] %s not found", section, key)
}

type auditResult struct {
	Setting admSetting
	Current string
	Err     error
}

func (a auditResult) OK() bool {
	return a.Err == nil && strings.EqualFold(a.Current, a.Setting.Want)
}

func auditADM() []auditResult {
	var out []auditResult
	for _, s := range recommendedSettings() {
		cur, err := iniGet(s.File, s.Section, s.Key)
		out = append(out, auditResult{Setting: s, Current: cur, Err: err})
	}
	return out
}

// deviations returns settings not at their recommended value, excluding
// cosmetic ones unless includeCosmetic is set.
func deviations(rs []auditResult, includeCosmetic bool) []auditResult {
	var out []auditResult
	for _, r := range rs {
		if r.OK() || (r.Setting.Cosmetic && !includeCosmetic) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func printAudit(rs []auditResult) {
	fmt.Printf("%-18s %-9s %-11s %-9s %-7s %s\n",
		"FILE", "SECTION", "KEY", "CURRENT", "WANT", "STATUS")
	for _, r := range rs {
		cur, status := r.Current, "ok"
		switch {
		case r.Err != nil:
			cur, status = "-", "unreadable"
		case !r.OK() && r.Setting.Cosmetic:
			status = "differs (cosmetic)"
		case !r.OK():
			status = "DIFFERS"
		}
		fmt.Printf("%-18s %-9s %-11s %-9s %-7s %s\n",
			filepath.Base(r.Setting.File), r.Setting.Section, r.Setting.Key,
			cur, r.Setting.Want, status)
	}
	if d := deviations(rs, true); len(d) > 0 {
		fmt.Println()
		for _, r := range d {
			fmt.Printf("  %s: %s\n", r.Setting.Key, r.Setting.Why)
		}
		fmt.Println("\nApply with:  realfanctrld -apply-config")
	}
	fmt.Println("\nNote: none of these lift the hardcoded PWM ceiling (DEFECT-001). They only")
	fmt.Println("improve ADM's own fallback for when realfanctrld is not running.")
}

// applyADM writes the recommended values with ADM's confutil and optionally
// restarts emboardmand so they take effect immediately.
func applyADM(rs []auditResult, restart bool) error {
	todo := deviations(rs, true)
	if len(todo) == 0 {
		fmt.Println("All recommended ADM settings already applied; nothing to do.")
		return nil
	}
	for _, r := range todo {
		s := r.Setting
		if r.Err != nil {
			fmt.Printf("skipping %s [%s] %s: %v\n", s.File, s.Section, s.Key, r.Err)
			continue
		}
		out, err := exec.Command(confutilPath, "-set", s.File, s.Section, s.Key, s.Want).CombinedOutput()
		if err != nil {
			return fmt.Errorf("confutil -set %s %s %s: %v: %s",
				s.File, s.Section, s.Key, err, strings.TrimSpace(string(out)))
		}
		fmt.Printf("set %s [%s] %s = %s (was %q)\n",
			filepath.Base(s.File), s.Section, s.Key, s.Want, r.Current)
	}
	if !restart {
		fmt.Println("\nSkipped emboardmand restart; changes apply on its next restart or at reboot.")
		return nil
	}
	fmt.Println("\nRestarting emboardmand...")
	if out, err := exec.Command("/etc/init.d/S93emboardmand", "stop").CombinedOutput(); err != nil {
		return fmt.Errorf("stopping emboardmand: %v: %s", err, strings.TrimSpace(string(out)))
	}
	time.Sleep(2 * time.Second)
	if out, err := exec.Command("/etc/init.d/S93emboardmand", "start").CombinedOutput(); err != nil {
		return fmt.Errorf("starting emboardmand: %v: %s", err, strings.TrimSpace(string(out)))
	}
	fmt.Println("emboardmand restarted.")
	return nil
}

// Reading is one temperature sensor sample.
type Reading struct {
	Group   string
	Chip    string
	Device  string // "nvme7" for NVMe chips, else empty
	Slot    string // "M.2-8" when the slot map knows this PCI address
	Label   string
	Temp    int
	Ignored bool // excluded from the curves by IgnoreSensors
}

// Where names the drive bay if known, else the kernel device, else the chip.
func (r Reading) Where() string {
	switch {
	case r.Slot != "":
		return r.Slot
	case r.Device != "":
		return r.Device
	default:
		return r.Chip
	}
}

// groupFor maps an hwmon chip name to a curve group. acpitz is deliberately
// excluded: on the AS6812F / FS6812X it reports a constant 20 C and is meaningless.
func groupFor(chip string) string {
	switch {
	case chip == "nvme":
		return "ssd"
	case chip == "k10temp" || chip == "coretemp":
		return "cpu"
	case chip == "acpitz" || chip == "ACAD":
		return ""
	default:
		// The 10G NICs surface as hwmon chips named after their PCI address.
		return "nic"
	}
}

func readInt(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func readSensors(cfg Config) ([]Reading, error) {
	dirs, err := filepath.Glob(filepath.Join(hwmonRoot, "hwmon*"))
	if err != nil {
		return nil, err
	}
	var out []Reading
	for _, d := range dirs {
		nameBytes, err := os.ReadFile(filepath.Join(d, "name"))
		if err != nil {
			continue
		}
		chip := strings.TrimSpace(string(nameBytes))
		group := groupFor(chip)
		if group == "" {
			continue
		}

		// For NVMe, hwmonN/device is the nvme controller and
		// hwmonN/device/device is its PCI function.
		var device, slot string
		if group == "ssd" {
			if p, err := filepath.EvalSymlinks(filepath.Join(d, "device")); err == nil {
				device = filepath.Base(p)
			}
			if p, err := filepath.EvalSymlinks(filepath.Join(d, "device", "device")); err == nil {
				slot = cfg.SlotMap[filepath.Base(p)]
			}
		}

		inputs, _ := filepath.Glob(filepath.Join(d, "temp*_input"))
		sort.Strings(inputs)
		for _, in := range inputs {
			milli, err := readInt(in)
			if err != nil {
				continue
			}
			label := strings.TrimSuffix(filepath.Base(in), "_input")
			if lb, err := os.ReadFile(strings.TrimSuffix(in, "_input") + "_label"); err == nil {
				label = strings.TrimSpace(string(lb))
			}
			r := Reading{
				Group: group, Chip: chip, Device: device, Slot: slot,
				Label: label, Temp: milli / 1000,
			}
			r.Ignored = cfg.ignored(r)
			out = append(out, r)
		}
	}
	return out, nil
}

// hottestByGroup returns the single hottest reading in each group, so callers
// can report which drive bay is driving the fans rather than just a number.
func hottestByGroup(rs []Reading) map[string]Reading {
	m := map[string]Reading{}
	for _, r := range rs {
		if r.Ignored {
			continue
		}
		if cur, ok := m[r.Group]; !ok || r.Temp > cur.Temp {
			m[r.Group] = r
		}
	}
	return m
}

func setFan(id, pwm int) error {
	cmd := exec.Command(fanctrlPath, "-setfanpwm", strconv.Itoa(id), strconv.Itoa(pwm))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	// fanctrl exits 0 even when the MCU call fails, so inspect its output.
	if strings.Contains(string(out), "Failed") {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

func logf(format string, args ...any) {
	fmt.Printf("%s "+format+"\n", append([]any{time.Now().Format("2006-01-02 15:04:05")}, args...)...)
	os.Stdout.Sync()
}

// target computes the PWM the curves ask for, and names what drove it.
func target(cfg Config, peaks map[string]Reading) (int, string) {
	if len(peaks) == 0 {
		return cfg.FallbackPWM, "no sensors readable"
	}
	best, why := 0, "idle"
	for _, group := range []string{"ssd", "cpu", "nic"} {
		r, ok := peaks[group]
		if !ok {
			continue
		}
		curve := map[string]Curve{
			"ssd": cfg.SSDCurve, "cpu": cfg.CPUCurve, "nic": cfg.NICCurve,
		}[group]
		if p := curve.PWMFor(r.Temp); p > best {
			best = p
			why = fmt.Sprintf("%s %d C on %s", group, r.Temp, r.Where())
		}
	}
	return best, why
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// drive is the per-device rollup used by -status.
type drive struct {
	Slot      string
	Device    string
	Composite int
	Hotspot   int
	HotLabel  string
	SlotNum   int
	Skipped   []string // ignored sensors, shown but not acted on
}

func status(cfg Config) {
	rs, err := readSensors(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading sensors: %v\n", err)
		os.Exit(1)
	}

	// Roll the three sensors per NVMe up into one row per physical bay.
	byDev := map[string]*drive{}
	var others []Reading
	for _, r := range rs {
		if r.Group != "ssd" {
			others = append(others, r)
			continue
		}
		d, ok := byDev[r.Device]
		if !ok {
			n := 0
			if _, err := fmt.Sscanf(r.Slot, "M.2-%d", &n); err != nil {
				n = 1 << 30 // unmapped bays sort last
			}
			d = &drive{Slot: r.Slot, Device: r.Device, SlotNum: n}
			byDev[r.Device] = d
		}
		if r.Ignored {
			d.Skipped = append(d.Skipped, fmt.Sprintf("%s %d C", r.Label, r.Temp))
			continue
		}
		if strings.EqualFold(r.Label, "Composite") {
			d.Composite = r.Temp
		}
		if r.Temp > d.Hotspot {
			d.Hotspot, d.HotLabel = r.Temp, r.Label
		}
	}

	drives := make([]*drive, 0, len(byDev))
	for _, d := range byDev {
		drives = append(drives, d)
	}
	sort.Slice(drives, func(i, j int) bool { return drives[i].Hotspot > drives[j].Hotspot })

	fmt.Printf("%-8s %-13s %10s %10s   %s\n", "SLOT", "DEVICE", "COMPOSITE", "HOTSPOT", "HOTTEST SENSOR")
	for _, d := range drives {
		slot := d.Slot
		if slot == "" {
			slot = "?"
		}
		note := d.HotLabel
		if len(d.Skipped) > 0 {
			note += "   [ignored: " + strings.Join(d.Skipped, ", ") + "]"
		}
		fmt.Printf("%-8s %-13s %8d C %8d C   %s\n", slot, d.Device, d.Composite, d.Hotspot, note)
	}

	sort.Slice(others, func(i, j int) bool { return others[i].Temp > others[j].Temp })
	if len(others) > 0 {
		fmt.Println()
		for _, r := range others {
			fmt.Printf("%-8s %-13s %10s %8d C   %s\n",
				strings.ToUpper(r.Group), r.Chip, "", r.Temp, r.Label)
		}
	}

	peaks := hottestByGroup(rs)
	want, why := target(cfg, peaks)
	want = clamp(want, cfg.MinPWM, cfg.MaxPWM)
	fmt.Printf("\npeaks: ")
	for _, g := range []string{"ssd", "cpu", "nic"} {
		if r, ok := peaks[g]; ok {
			fmt.Printf("%s=%dC(%s) ", g, r.Temp, r.Where())
		}
	}
	fmt.Printf("\ncurve target: PWM %d — driven by %s\n", want, why)

	out, err := exec.Command(fanctrlPath, "-getfanspeed").CombinedOutput()
	switch {
	case err != nil || strings.Contains(string(out), "Failed"):
		fmt.Printf("\nactual fan speed: unavailable (run as root to read the MCU)\n")
	default:
		fmt.Printf("\n%s", out)
	}
}

func main() {
	confPath := flag.String("config", defaultConf, "path to config file")
	writeDefaults := flag.Bool("write-config", false, "write the default config to -config and exit")
	showStatus := flag.Bool("status", false, "print sensors and the PWM the curves ask for, then exit")
	once := flag.Bool("once", false, "apply one PWM update and exit")
	checkADM := flag.Bool("check", false, "audit ADM's fan settings against the recommended values, then exit")
	applyConf := flag.Bool("apply-config", false, "apply the recommended ADM fan settings and restart emboardmand")
	noRestart := flag.Bool("no-restart", false, "with -apply-config, do not restart emboardmand")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("realfanctrld %s (commit %s, built %s)\n", version, commit, built)
		fmt.Println("Christoph C. Cemper / Magosol Kft. — MIT licensed, no warranty.")
		fmt.Println("Tested only on ASUSTOR FS6812X (AS6812F / FS6812X) / ADM 5.1.3.RI81.")
		return
	}

	if *checkADM {
		rs := auditADM()
		printAudit(rs)
		if len(deviations(rs, false)) > 0 {
			os.Exit(1)
		}
		return
	}

	if *applyConf {
		if os.Geteuid() != 0 {
			fmt.Fprintln(os.Stderr, "-apply-config must run as root")
			os.Exit(1)
		}
		if err := applyADM(auditADM(), !*noRestart); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := loadConfig(*confPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	if *writeDefaults {
		if err := writeConfig(*confPath, defaultConfig()); err != nil {
			fmt.Fprintf(os.Stderr, "writing config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("wrote defaults to %s\n", *confPath)
		return
	}

	if *showStatus {
		status(cfg)
		return
	}

	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "realfanctrld must run as root (fanctrl talks to the MCU)")
		os.Exit(1)
	}

	applied := 0
	applyAndReport := func() (int, string) {
		rs, err := readSensors(cfg)
		peaks := map[string]Reading{}
		if err == nil {
			peaks = hottestByGroup(rs)
		}
		want, why := target(cfg, peaks)
		want = clamp(want, cfg.MinPWM, cfg.MaxPWM)

		// Rise immediately; fall no faster than CooldownStep per cycle.
		switch {
		case applied == 0:
			applied = want
		case want < applied-cfg.CooldownStep:
			applied -= cfg.CooldownStep
		default:
			applied = want
		}

		for _, id := range cfg.FanIDs {
			if err := setFan(id, applied); err != nil {
				logf("fan %d -> %d failed: %v", id, applied, err)
			}
		}
		return applied, why
	}

	if *once {
		pwm, why := applyAndReport()
		logf("applied PWM %d (%s)", pwm, why)
		return
	}

	logf("realfanctrld started: interval %ds, fans %v, range %d-%d, config %s",
		cfg.IntervalSec, cfg.FanIDs, cfg.MinPWM, cfg.MaxPWM, *confPath)

	// The daemon works regardless of these, but a mismatched ADM config means a
	// worse fallback if this process ever stops. Warn, never auto-apply.
	if d := deviations(auditADM(), false); len(d) > 0 {
		names := make([]string, 0, len(d))
		for _, r := range d {
			names = append(names, fmt.Sprintf("%s=%s (want %s)", r.Setting.Key, r.Current, r.Setting.Want))
		}
		logf("note: ADM fan settings differ from recommended: %s — run 'realfanctrld -apply-config' to fix",
			strings.Join(names, ", "))
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	ticker := time.NewTicker(time.Duration(cfg.IntervalSec) * time.Second)
	defer ticker.Stop()

	lastLogged := -1000
	lastHeartbeat := time.Now()

	for {
		pwm, why := applyAndReport()
		if abs(pwm-lastLogged) >= cfg.LogDelta {
			logf("PWM %d — %s", pwm, why)
			lastLogged = pwm
		} else if cfg.HeartbeatMin > 0 && time.Since(lastHeartbeat) >= time.Duration(cfg.HeartbeatMin)*time.Minute {
			logf("PWM %d — %s [heartbeat]", pwm, why)
			lastHeartbeat = time.Now()
		}

		select {
		case s := <-sigs:
			logf("received %s, exiting (emboardmand resumes control)", s)
			return
		case <-ticker.C:
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
