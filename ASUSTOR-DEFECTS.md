# ASUSTOR ADM defect report

Findings from diagnosing an ASUSTOR AS6812F / FS6812X running ADM 5.1.3.RI81, documented
so they can be filed with ASUSTOR support and tracked to a resolution.

**Author:** Christoph C. Cemper / Magosol Kft.
**Repository:** https://github.com/christophcemper/asustor-realfanctrl

> All measurements below come from two AS6812F / FS6812X units. Nothing here has been
> verified on any other ASUSTOR model or ADM release.

## Status

| ID | Title | Severity | Found | Submitted | Response | Status |
|----|-------|----------|-------|-----------|----------|--------|
| [DEFECT-001](#defect-001--fan-pwm-ceiling-hardcoded-at-23-31-duty) | Fan PWM ceiling hardcoded at 23–31% duty | **Critical** | 2026-07-28 | _not yet submitted_ | — | Open |
| [DEFECT-002](#defect-002--fan-control-ignores-nvme-sensor-1--sensor-2) | Fan control ignores NVMe Sensor 1 / Sensor 2 | **Critical** | 2026-07-28 | _not yet submitted_ | — | Open |
| [DEFECT-003](#defect-003--ui-fan-speed-setting-has-no-effect) | UI fan speed setting has no effect | High | 2026-07-28 | _not yet submitted_ | — | Open |
| [DEFECT-004](#defect-004--incomplete-model-profile-for-AS6812F / FS6812X) | Incomplete model profile for AS6812F / FS6812X | Medium | 2026-07-28 | _not yet submitted_ | — | Open |
| [DEFECT-005](#defect-005--failed-lacp-setup-leaves-the-nas-unreachable) | Failed LACP setup leaves the NAS unreachable | High | 2026-07-28 | _not yet submitted_ | — | Open |

**Submission log**

| Date | Channel | Reference | Notes |
|------|---------|-----------|-------|
| _pending_ | _(ASUSTOR support portal / RMA / forum)_ | _(ticket no.)_ | _(fill in on submission)_ |

Ready-to-send ticket text: [docs/asustor-support-ticket.md](docs/asustor-support-ticket.md).

## Environment

| Item | Value |
|------|-------|
| Model | ASUSTOR **FS6812X** (Flashstor 12 Pro). `/etc/nas.conf` carries both names: `ModelName = FS6812X` (the retail name) and `Model = AS6812F` (the internal designation used throughout ADM's tooling and log output) |
| ADM version | 5.1.3.RI81 (`Version` in `/etc/nas.conf`), last updated 2026-06-16 |
| Kernel | Linux 6.6.x x86_64 |
| Drives | 12 × T-FORCE TM8FFH004T 4 TB NVMe |
| Array | RAID 6 (`md1`), 38 TB, ~85% full, btrfs |
| Fans | 2 (`fanctrl` addresses fan id 0 and 1) |
| Fan MCU | PIC16F1829 via `/dev/ttyS1`, driven by `emboardmand` through `libndal` |
| Units affected | Both AS6812F / FS6812X units tested (hostnames cccnas6, cccnas7) |

Reproduction tooling: `sudo /usr/sbin/emboardmand -debug`, `sudo /usr/sbin/fanctrl -getfanspeed`,
and direct sysfs reads under `/sys/class/hwmon/`. Full capture:
[`docs/evidence/emboardmand-debug-excerpt.txt`](docs/evidence/emboardmand-debug-excerpt.txt).

## DEFECT-001 — Fan PWM ceiling hardcoded at 23–31% duty

**Severity: Critical** · Found 2026-07-28 · Submitted: _not yet_ · Response: —

### Description

`emboardmand` initialises its fan control from a per-model table whose maximum
is far below what the hardware can deliver. On the AS6812F / FS6812X fan 0 can never be
driven above PWM 58/255 (~23% duty) and fan 1 never above PWM 80/255 (~31%),
regardless of temperature. No configuration file overrides this.

### Evidence

From `sudo /usr/sbin/emboardmand -debug`:

```
[EMBOARD] Init_Fan_Attribute(805):  --> Low: 40 Medium: 55 High: 70 Max: 80 Step: 5
[EMBOARD] Thermal_Monitor_Thread(1714): --> Fan1 PWM: 25, 37, 55, 58
[EMBOARD] Thermal_Monitor_Thread(1714): --> Fan2 PWM: 40, 55, 70, 80
```

With SSDs at 80 °C the SSD severity term is already at its maximum (`Ssd: 4`)
and the loop still selects only its highest permitted band:

```
[EMBOARD] Thermal_Monitor_Thread(1815): --> iCpu: 71 C, Hdd: 0 C, Ssd: 80 C, LAN: 0 C
[EMBOARD] Thermal_Monitor_Thread(1816): --> Fan1 -- ... Ssd: 4, Range: [55 ~ 58], Want: 55/0, PWM 52 -> 55
[EMBOARD] Thermal_Monitor_Thread(1816): --> Fan2 -- ... Ssd: 4, Range: [70 ~ 80], Want: 72/0, PWM 58 -> 61
```

`emboardmand`'s own configured SSD warning temperature is 58 °C. The drives are
22 °C past that warning and the fans remain at roughly 30% duty.

Forcing the fans manually proves the hardware has far more headroom:

| PWM | Fan 0 | Fan 1 |
|-----|-------|-------|
| ADM's ceiling (55 / 71) | 1502 RPM | 2198 RPM |
| Forced 235 | 3841 RPM | 5793 RPM |

### Configuration attempts that do **not** work

All were applied and `emboardmand` restarted; none changed the ceiling:

| File | Setting | Tried |
|------|---------|-------|
| `/etc/nas.conf` | `[Hardware] FanNumber` | `1` → `2` |
| `/etc/nas.conf` | `[Hardware] IsSmartFan` | `Yes` → `No` |
| `/etc/nas.conf` | `[Hardware] FanSpeed` | `High` |
| `/etc/nas.conf` | `[Hardware] LowSpeed/NormalSpeed/HighSpeed` | `25/35/45` → `25/60/90` → `HighSpeed 220` |
| `/usr/etc/emboard.conf` | `[Fan1] Mode` | `1` → `0` |
| `/usr/etc/emboard.conf` | `[Fan1] Level` | `1` → `3` |

Any externally applied PWM is walked back to the table value at roughly 3 PWM
per 2.5 s cycle, so `fanctrl -setfanpwm` alone does not hold either.

### Impact

Sustained thermal throttling under normal load. Same 4 GB SMB write, same
client and array, differing only in chassis temperature:

| Chassis state | Sustained write |
|---------------|-----------------|
| Cool (fans forced to PWM 235) | **590 MB/s** |
| Hot (fans at ADM's ceiling) | **113 MB/s** |

That is a **5.2× throughput loss** caused purely by the fan ceiling, plus the
long-term reliability cost of running NVMe controllers 30–45 °C hotter than
necessary.

### Suggested fix

Raise the AS6812F / FS6812X fan table to the hardware's real range (PWM up to 255), or
honour `HighSpeed` from `/etc/nas.conf` as the ceiling so operators can raise it.

## DEFECT-002 — Fan control ignores NVMe Sensor 1 / Sensor 2

**Severity: Critical** · Found 2026-07-28 · Submitted: _not yet_ · Response: —

### Description

`emboardmand` consumes exactly one temperature per NVMe drive — the Composite
sensor. Modern NVMe drives expose additional sensors, and on these drives the
controller hot spot (`temp2_input`, "Sensor 1") runs 13–27 °C above Composite.
ADM never reads it, so both the fan logic and every ADM temperature display
systematically understate drive temperature.

### Evidence

Composite vs Sensor 1, read from sysfs at the same moment, all 12 drives
identical models in one RAID 6 array:

| Device | Composite | Sensor 1 | Gap |
|--------|-----------|----------|-----|
| nvme9 | 73 °C | **100 °C** | +27 |
| nvme8 | 66 °C | 90 °C | +24 |
| nvme4 | 46 °C | 64 °C | +18 |
| nvme7 | 55 °C | 69 °C | +14 |
| nvme6 | 56 °C | 69 °C | +13 |
| nvme11 | 40 °C | 53 °C | +13 |

Drive-declared limits: `temp1_max` 89 °C, `temp1_crit` 109 °C, `temp2_max` 119 °C.

So ADM sees 73 °C against an 89 °C warning threshold and reports the system as
healthy, while that drive's controller is at 100 °C.

### Impact

Thermal events are invisible to the operator and to the fan controller. A drive
can sit 27 °C hotter than anything ADM will ever display or react to.

### Suggested fix

Feed the maximum of all exposed NVMe sensors into the thermal loop, and surface
per-sensor values in ADM's disk health UI.

## DEFECT-003 — UI fan speed setting has no effect

**Severity: High** · Found 2026-07-28 · Submitted: _not yet_ · Response: —

### Description

Changing the fan speed setting in the ADM web UI produces no audible or
measurable change in fan behaviour. The value is written to `/etc/nas.conf`
(`FanSpeed`, `LowSpeed`/`NormalSpeed`/`HighSpeed`) but the thermal loop
continues to operate from its hardcoded table (DEFECT-001), and reasserts its
own PWM within seconds.

### Reproduction

1. Set fan speed to its highest option in the ADM UI.
2. `sudo /usr/sbin/fanctrl -getfanspeed` — PWM is unchanged (~55 / ~71).
3. `sudo /usr/sbin/fanctrl -setfanpwm 0 255` — fans audibly spin up.
4. Poll `-getfanspeed`: PWM decays 245 → 235 → 225 → … back to ~55 within minutes.

### Impact

Operators have no working control over cooling and no indication that the
control is inert. This is what makes DEFECT-001 hard to discover.

### Suggested fix

Make the UI setting authoritative over the thermal loop's ceiling, or disable
the control and state that fan speed is automatic.

## DEFECT-004 — Incomplete model profile for AS6812F / FS6812X

**Severity: Medium** · Found 2026-07-28 · Submitted: _not yet_ · Response: —

### Description

Several platform values shipped for the AS6812F / FS6812X do not match the hardware.

### Evidence

```
[EMBOARD] Load_Hardware_Conf(645):  --> The nas bay number is 0
[EMBOARD] Reset_Led_Status(4919):   --> Failed to set front-usb led mode! (-153)
[EMBOARD] Reset_Led_Status(4950):   --> Failed to set 10g led mode! (-153)
```

| Item | Shipped value | Actual hardware |
|------|---------------|-----------------|
| Bay number (debug output) | `0` | 12 |
| `/usr/etc.base/emboard.conf` `[Disk] Internal` | `6` | 12 |
| `/usr/etc.base/emboard.conf` fan sections | `[Fan1]` only | 2 fans |
| `/etc/nas.conf` `[Hardware] FanNumber` (as shipped on one unit) | `1` | 2 |

The front-USB and 10G LED initialisation also fail with error `-153` on every
boot.

### Impact

Suggests the AS6812F / FS6812X inherited another model's profile, which is consistent
with the wrong fan table in DEFECT-001.

### Suggested fix

Ship a correct platform profile for the AS6812F / FS6812X: 12 bays, 2 fans, correct LED
map, correct fan PWM range.

## DEFECT-005 — Failed LACP setup leaves the NAS unreachable

**Severity: High** · Found 2026-07-28 · Submitted: _not yet_ · Response: —

### Description

Creating an 802.3ad (LACP) bond from the ADM UI failed with the error
**"failed to configure IP6"**. ADM left the bond half-created: both member
interfaces were enslaved and the NAS became completely unreachable — no ICMP,
no SMB, no SSH, on either IPv4 or IPv6 link-local — until the switch-side LAG
was corrected. There was no rollback and no console-free recovery path.

### Reproduction

1. Configure an 802.3ad bond in ADM before the switch's LAG group is active.
2. ADM reports "failed to configure IP6".
3. The NAS is unreachable on all addresses and protocols.

Recovery required either fixing the switch LAG blind, or a hardware network
reset (5-second reset button), which also resets the admin password.

### Impact

A single UI action can strand a headless NAS with no in-band recovery. For a
device commonly installed without a monitor, this risks extended downtime.

### Suggested fix

Apply bond changes with a commit/confirm timeout that automatically reverts if
the management interface does not respond, as network operating systems do for
remote configuration changes. At minimum, warn that the switch LAG must exist
first, and never leave a partially-applied bond in place after an error.

## Related observation (not filed as a defect)

Setting the bond to **balance-alb** (ADM's "adaptive load balancing") reduced
SMB write throughput from 394 MB/s to 129 MB/s — a 3× loss versus a single
unbonded 10 GbE link — while raw TCP throughput over the same bond was
unaffected (~525 MB/s across two parallel streams). This is consistent with a
known interaction between ALB's ARP-based receive balancing and SMB multichannel
over IPv6 link-local, rather than an ADM defect. Switching to 802.3ad with a
matching switch LAG resolved it (405–458 MB/s sustained). Recorded here because
ADM offers ALB as a default-looking choice with no warning about this cost.
