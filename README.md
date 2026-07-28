# asustor-realfanctrl

Adaptive fan control for ASUSTOR NAS units running ADM, because the built-in
fan control caps the fans at roughly a quarter of their capability and never
looks at the sensor that matters.

**Author:** Christoph C. Cemper / Magosol Kft. · MIT licensed

> ## ⚠️ No guarantees, limited testing
>
> This software drives cooling hardware. It is provided **as is**, with **no
> warranty**, and it has **not** undergone extended or broad testing.
>
> It was developed and tested on exactly **one** machine type: **ASUSTOR
> AS6812F (Flashstor), ADM 5.1.3.RI81, 12 × NVMe**. Behaviour on any other
> model, ADM release, fan controller, or drive combination is **unverified**.
>
> The fan curves, the PWM range, the M.2 slot map, and the assumption that
> `fanctrl` reaches the fan MCU are all specific to that hardware. A wrong
> curve, an unsupported MCU, or a misread sensor can leave a machine
> under-cooled, risking **hardware damage and data loss**.
>
> After installing, run `realfanctrl status` and confirm that the temperatures
> look sane and that fan RPM actually responds. Then keep monitoring.
> **Use at your own risk.**

## The problem

Two independent defects in ADM's `emboardmand`, both confirmed with its own
debug output. Full write-up in [ASUSTOR-DEFECTS.md](ASUSTOR-DEFECTS.md).

### 1. The fans are capped at ~23–31% duty, in firmware

`emboardmand` loads a per-model PWM table and will never exceed it:

```
Init_Fan_Attribute: Low: 40 Medium: 55 High: 70 Max: 80 Step: 5
Fan1 PWM: 25, 37, 55, 58      <- fan 0 can never exceed 58 of 255
Fan2 PWM: 40, 55, 70, 80      <- fan 1 can never exceed 80 of 255
```

Even with the SSD severity term already at maximum, the loop asks for PWM 55
and 72. Its own SSD warning threshold is 58 °C; the drives were at 80 °C.
No setting in `nas.conf` or `emboard.conf` reaches this ceiling, and the ADM
UI's fan control does nothing.

What the hardware can actually do:

| PWM | Fan 0 | Fan 1 |
|-----|-------|-------|
| ADM's ceiling (55 / 71) | 1502 RPM | 2198 RPM |
| Forced 235 | **3841 RPM** | **5793 RPM** |

### 2. It reads the wrong temperature

ADM regulates on each NVMe's **Composite** sensor only. These drives also
expose a controller hot spot ("Sensor 1") that runs much hotter:

| Device | Composite (what ADM sees) | Sensor 1 (reality) | Gap |
|--------|---------------------------|--------------------|-----|
| nvme9 | 73 °C | **100 °C** | +27 |
| nvme8 | 66 °C | 90 °C | +24 |
| nvme7 | 55 °C | 69 °C | +14 |

ADM compares 73 °C against an 89 °C warning threshold, concludes everything is
fine, and leaves the fans idling — while that controller sits at 100 °C.

### What it costs

Same 4 GB SMB write, same client, same array — only chassis temperature differs:

| Chassis | Sustained write |
|---------|-----------------|
| Cool (fans forced high) | **590 MB/s** |
| Hot (ADM's fan ceiling) | **113 MB/s** |

A **5.2× throughput loss** from thermal throttling, plus the reliability cost of
running NVMe controllers 30–45 °C hotter than they need to be.

## What this does

`realfanctrld` reads **every** temperature the kernel exposes, maps the hottest
sensor per group through a configurable curve, and re-asserts the result through
ASUSTOR's own `fanctrl` binary every 2 seconds — often enough to win against
`emboardmand`, which walks PWM back by only ~3 per 2.5 s cycle.

It **co-exists with `emboardmand` rather than replacing it.** That daemon also
owns the LEDs, the power button, power schedules and disk hibernation, so
killing it would break more than it fixes. Stopping `realfanctrld` hands fan
control straight back to ADM.

### Advantages over stock ADM

- **Uses the real hot spot.** Every NVMe sensor, not just Composite.
- **Full PWM range.** 0–255 instead of a 58/80 ceiling.
- **Names the bay.** `M.2-6`, not `nvme9` — mapped by PCI address, so it stays
  correct regardless of probe order.
- **Tunable curves** per group (SSD / CPU / NIC) in plain JSON, no rebuild.
- **Handles lying sensors.** Some drives report a constant bogus value on an
  unimplemented register; `ignore_sensors` excludes them from the curve while
  still displaying them.
- **Smooth spin-down.** Rises immediately, falls at a bounded rate, so the fans
  don't oscillate audibly.
- **Fails safe.** If no sensor can be read it goes to a configurable high PWM.
- **Survives reboots**, installed where ADM's persistent init actually runs.
- **Audits ADM's own settings** (`-check` / `-apply-config`) so that if the
  daemon is ever stopped, ADM falls back to its highest band instead of its
  lowest.

### What it does *not* do

- It does not raise ADM's ceiling — that is firmware. It works around it by
  writing PWM directly and often.
- It does not fix ADM's temperature displays, which still show Composite only.
- It does not replace `emboardmand` or touch LEDs, buttons or schedules.

## Example

```
$ realfanctrl status
realfanctrld running (pid 24626)
SLOT     DEVICE         COMPOSITE    HOTSPOT   HOTTEST SENSOR
M.2-6    nvme9               69 C       95 C   Sensor 1
M.2-5    nvme8               52 C       76 C   Sensor 1
M.2-7    nvme6               56 C       68 C   Sensor 1
M.2-8    nvme7               55 C       68 C   Sensor 1
...
M.2-4    nvme11              43 C       52 C   Sensor 1

CPU      k10temp                        64 C   Tctl
NIC      0000f000200                    50 C   temp1

peaks: ssd=95C(M.2-6) cpu=64C(k10temp) nic=50C(0000f000200)
curve target: PWM 255 — driven by ssd 95 C on M.2-6

Fan[0] speed is 3841, pwm is 235
Fan[1] speed is 5793, pwm is 235
```

## Install

See **[INSTALL.md](INSTALL.md)**. Short version, from a workstation with Go and
SSH access to the NAS:

```bash
make deploy NAS=your-nas-host
```

## CLI

`realfanctrl` is installed at `/usr/local/bin/realfanctrl`, which is on ADM's
default PATH. **No alias or shell setup is needed** — it works from any shell,
including non-interactive ones and cron. It re-invokes itself under `sudo` when
you are not root.

| Command | Does |
|---------|------|
| `realfanctrl start` | start the daemon |
| `realfanctrl stop` | stop it — ADM resumes fan control |
| `realfanctrl restart` | reload binary and config |
| `realfanctrl status` | sensors, curve target, real fan RPM |
| `realfanctrl log` | follow the log |
| `realfanctrl temps` | sensors only, without touching the fans |
| `realfanctrl rpm` | what the MCU reports right now |
| `realfanctrl check` | audit ADM's own fan settings |
| `realfanctrl apply-config` | apply them and restart `emboardmand` |
| `realfanctrl config` | edit the curve and restart |
| `realfanctrl version` | version and build info |

[`.bash_aliases`](.bash_aliases) is **optional** shorthand on top of that —
`rfc` for `realfanctrl`, plus `rfc-status`, `rfc-log`, `rfc-temps`, `rfc-rpm`,
`rfc-check`, `rfc-apply`, `rfc-conf`, and three raw diagnostics that have no
`realfanctrl` equivalent:

| Alias | Does |
|-------|------|
| `rfc-sensors` | every raw hwmon sensor (for porting to a new model) |
| `rfc-slots` | NVMe bay → PCI address map (for `slot_map`) |
| `rfc-adm-debug` | ASUSTOR's own fan logic, verbose |

ADM has no bash, so those load from `~/.profile` — see
[INSTALL.md](INSTALL.md#4-shell-aliases-optional).

The daemon binary also runs standalone:

```
realfanctrld -status         sensors and the PWM the curve wants
realfanctrld -check          audit ADM's fan settings (exit 1 if they differ)
realfanctrld -apply-config   apply them and restart emboardmand
realfanctrld -write-config   write the default config file
realfanctrld -once           apply one PWM update and exit
realfanctrld -version        version, and the no-warranty reminder
```

## Configuration

`/usr/local/etc/realfanctrl.conf` — see [examples/realfanctrl.conf](examples/realfanctrl.conf).

| Key | Meaning |
|-----|---------|
| `interval_sec` | how often to re-assert PWM (default 2; lower = tighter grip) |
| `fan_ids` | fan ids passed to `fanctrl` (default `[0, 1]`) |
| `min_pwm` / `max_pwm` | clamp for the computed PWM |
| `fallback_pwm` | used when no sensor can be read |
| `cooldown_step` | max PWM drop per cycle — smooths spin-down |
| `log_delta` | suppress log lines until PWM moves this much |
| `heartbeat_min` | log a line at least this often anyway |
| `ssd_curve` / `cpu_curve` / `nic_curve` | `[{temp, pwm}, …]`, linearly interpolated |
| `slot_map` | PCI address → bay label |
| `ignore_sensors` | e.g. `["M.2-8:Sensor 2"]` to drop a bogus sensor |

The applied PWM is the **highest** of what the three curves ask for.

## Porting to another ASUSTOR model

Nothing here is AS6812F-specific by design, but the defaults are. To adapt:

1. `rfc-sensors` — check which chips and labels your box exposes.
2. `rfc-slots` — get your PCI → device mapping, then get the bay numbering from
   `sudo /usr/sbin/emboardmand -debug` (look for `M.2-N [id, nvmeXn1]`) and
   write `slot_map` accordingly.
3. `sudo /usr/sbin/fanctrl -getfanspeed` — confirm how many fans respond and
   set `fan_ids`.
4. Set curves conservatively, then watch `realfanctrl status` under load before
   trusting it.

Reports from other models are welcome — please include `rfc-sensors`,
`rfc-slots` and the `Init_Fan_Attribute` line from `rfc-adm-debug`.

## How it works

```
sysfs /sys/class/hwmon/*        ADM
  nvme temp1/2/3  ──┐            emboardmand ── walks PWM ±3 per 2.5s
  k10temp Tctl    ──┼─► curves ─► PWM ─► fanctrl ─► PIC16F1829 MCU ─► fans
  NIC temps       ──┘            (realfanctrld writes every 2s and wins)
```

Because both processes write to the same MCU, the reported PWM settles 10–20
below what `realfanctrld` requests — `emboardmand` nudges it down between our
writes. This is expected, not a fault. Lower `interval_sec` to 1 to tighten it.

## Credits

Diagnosed and built with [Claude Code](https://claude.com/claude-code).
