# Support ticket draft for ASUSTOR

Ready-to-send text for ASUSTOR support (https://member.asustor.com/ → Support →
Technical Inquiry). Fill in the two placeholders, then record the date and
ticket number in the submission log in [../ASUSTOR-DEFECTS.md](../ASUSTOR-DEFECTS.md).

> **Do not commit your serial number.** Fill it in only in the ticket itself.

---

## Subject

`AS6812F / ADM 5.1.3.RI81: fan PWM capped at 23–31% duty and thermal control ignores NVMe hot-spot sensors`

## Body

Hello,

I have identified two reproducible defects in ADM's fan control on the AS6812F.
Together they cause sustained thermal throttling under normal load: my NVMe
controllers reach 100 °C while ADM reports 73 °C and holds the fans at roughly
a quarter of their capability.

**Affected units:** 2 × AS6812F (both behave identically)
**ADM version:** 5.1.3.RI81
**Serial number:** `<FILL IN>`
**Drives:** 12 × T-FORCE TM8FFH004T 4 TB NVMe, RAID 6
**Contact:** `<FILL IN>`

### Defect 1 — Fan PWM is capped at 23–31% duty in firmware

`emboardmand` initialises fan control from a per-model table whose maximum is
far below what the hardware delivers. From `sudo /usr/sbin/emboardmand -debug`:

```
Init_Fan_Attribute(805): --> Low: 40 Medium: 55 High: 70 Max: 80 Step: 5
Thermal_Monitor_Thread(1714): --> Fan1 PWM: 25, 37, 55, 58
Thermal_Monitor_Thread(1714): --> Fan2 PWM: 40, 55, 70, 80
```

Fan 0 can never exceed PWM 58/255 (23% duty); fan 1 never exceeds 80/255 (31%).

With SSDs at 80 °C the loop has already escalated to maximum severity (`Ssd: 4`)
and still requests only PWM 55 and 72:

```
Thermal_Monitor_Thread(1815): --> iCpu: 71 C, Hdd: 0 C, Ssd: 80 C, LAN: 0 C
Thermal_Monitor_Thread(1816): --> Fan1 -- ... Ssd: 4, Range: [55 ~ 58], Want: 55/0, PWM 52 -> 55
Thermal_Monitor_Thread(1816): --> Fan2 -- ... Ssd: 4, Range: [70 ~ 80], Want: 72/0, PWM 58 -> 61
```

`emboardmand`'s own configured SSD warning temperature is 58 °C. The drives are
22 °C past that warning and the fans remain at ~30% duty.

The hardware has far more headroom — forcing PWM manually with
`fanctrl -setfanpwm`:

| PWM | Fan 0 | Fan 1 |
|-----|-------|-------|
| ADM's ceiling (55 / 71) | 1502 RPM | 2198 RPM |
| Forced 235 | 3841 RPM | 5793 RPM |

No configuration reaches this ceiling. I applied each of the following and
restarted `emboardmand`; none changed the behaviour:

- `/etc/nas.conf` `[Hardware]`: `FanNumber` 1→2, `IsSmartFan` Yes→No,
  `FanSpeed` High, `LowSpeed`/`NormalSpeed`/`HighSpeed` 25/35/45 → 25/60/90,
  `HighSpeed` 220
- `/usr/etc/emboard.conf` `[Fan1]`: `Mode` 1→0, `Level` 1→3

The fan speed control in the ADM web UI likewise has no measurable effect
(filed separately below).

### Defect 2 — Thermal control reads only the NVMe Composite sensor

`emboardmand` consumes exactly one temperature per drive. These NVMe drives
expose three, and the controller hot spot (`temp2_input`, "Sensor 1") runs
13–27 °C above Composite. Read from sysfs at the same moment, all 12 drives
identical models in one array:

| Device | Composite (what ADM uses) | Sensor 1 | Gap |
|--------|---------------------------|----------|-----|
| nvme9 | 73 °C | **100 °C** | +27 |
| nvme8 | 66 °C | 90 °C | +24 |
| nvme4 | 46 °C | 64 °C | +18 |
| nvme7 | 55 °C | 69 °C | +14 |

Drive-declared limits: `temp1_max` 89 °C, `temp1_crit` 109 °C, `temp2_max` 119 °C.

So ADM compares 73 °C against an 89 °C warning threshold, reports the system as
healthy, and leaves the fans idling — while that drive's controller is at 100 °C.
This temperature is invisible in every ADM display.

I verified this is a genuine thermal reading and not a sensor artefact: RAID 6
stripes uniformly (all drives measured at an identical 30.7 MB/s), yet the
spread between identical drives under identical load was 100 °C vs 53 °C.

### Measured impact

Same 4 GB SMB write, same client and array, differing only in chassis temperature:

| Chassis state | Sustained write |
|---------------|-----------------|
| Cool (fans forced to PWM 235) | **590 MB/s** |
| Hot (fans at ADM's ceiling) | **113 MB/s** |

A 5.2× throughput loss caused purely by the fan ceiling, plus the long-term
reliability cost of running NVMe controllers 30–45 °C hotter than necessary.

### Additional issues observed

- **UI fan control has no effect.** Setting the highest fan speed in the ADM UI
  changes nothing measurable; an externally applied PWM decays back to the table
  value at ~3 PWM per 2.5 s cycle.
- **Incomplete model profile.** The same debug run prints
  `Load_Hardware_Conf(645): --> The nas bay number is 0` on a 12-bay unit, plus
  `Failed to set front-usb led mode! (-153)` and
  `Failed to set 10g led mode! (-153)` on every boot.
  `/usr/etc.base/emboard.conf` declares `[Disk] Internal = 6` and only a
  `[Fan1]` section on a 12-bay, 2-fan chassis. This suggests the AS6812F
  inherited another model's platform profile, which would also explain the
  wrong fan table.
- **Failed LACP setup left the NAS unreachable.** Creating an 802.3ad bond from
  the UI failed with "failed to configure IP6" and left the bond half-applied:
  both members enslaved, no ICMP/SMB/SSH on any address, with no rollback and no
  in-band recovery. Recovery required correcting the switch LAG blind. Please
  consider a commit/confirm timeout that auto-reverts if management connectivity
  is lost, as network operating systems do for remote configuration changes.

### What I am asking for

1. Confirmation that you can reproduce Defect 1 and Defect 2 on the AS6812F.
2. A firmware fix that (a) allows the fan table to use the hardware's real PWM
   range, and (b) feeds the maximum of all exposed NVMe sensors into the thermal
   loop and the disk health UI.
3. An indication of which ADM release would carry the fix.

Full write-up, complete debug capture and all measurements are public here:
https://github.com/christophcemper/asustor-realfanctrl/blob/main/ASUSTOR-DEFECTS.md

I have published an open-source workaround daemon in the same repository, since
running these drives at 100 °C was not something I could leave in place. I would
much rather retire it in favour of a firmware fix.

Best regards,
Christoph C. Cemper / Magosol Kft.

---

## Short version (if the form has a character limit)

> AS6812F, ADM 5.1.3.RI81, two units affected. Serial `<FILL IN>`.
>
> Two reproducible fan-control defects.
>
> 1. `emboardmand` caps fan PWM at a per-model table — 58/255 for fan 0, 80/255
>    for fan 1 (23–31% duty). `emboardmand -debug` prints
>    `Init_Fan_Attribute: Low: 40 Medium: 55 High: 70 Max: 80` and
>    `Fan1 PWM: 25, 37, 55, 58` / `Fan2 PWM: 40, 55, 70, 80`. Even at maximum
>    severity (Ssd: 4, drives at 80 °C, 22 °C past its own 58 °C warning) it
>    requests only PWM 55/72. Forced to PWM 235 the same fans run 3841/5793 RPM
>    instead of 1502/2198, so the hardware has ample headroom. No setting in
>    nas.conf or emboard.conf changes this, and the UI fan control has no effect.
>
> 2. Thermal control reads only each NVMe's Composite sensor. On my drives
>    Sensor 1 runs 13–27 °C hotter: one drive read Composite 73 °C while Sensor 1
>    was at 100 °C. ADM therefore reports "healthy" against an 89 °C threshold
>    while the controller is at 100 °C, and the hot value appears nowhere in ADM.
>
> Impact: identical 4 GB SMB write drops from 590 MB/s (cool) to 113 MB/s (hot) —
> 5.2× throughput loss from thermal throttling.
>
> Full evidence: https://github.com/christophcemper/asustor-realfanctrl/blob/main/ASUSTOR-DEFECTS.md
>
> Please confirm reproduction and advise which ADM release will carry a fix.
