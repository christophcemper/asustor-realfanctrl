# Installing asustor-realfanctrl

> **Read the warning in [README.md](README.md) first.** This drives cooling
> hardware, has no warranty, and has only been tested on an ASUSTOR AS6812F
> running ADM 5.1.3.RI81.

## Requirements

**On the NAS**

- ASUSTOR NAS running ADM 5.x, x86_64.
- SSH enabled (Services → Terminal & SNMP → SSH) and an account in the
  `administrators` group, so `sudo` works.
- `/usr/sbin/fanctrl` present and functional — verify first:

  ```bash
  sudo /usr/sbin/fanctrl -getfanspeed
  ```

  You should get one `Fan[n] speed is …, pwm is …` line per fan. If this errors,
  **stop** — this tool cannot drive your fans.

**On the build machine** (only if building from source)

- Go 1.21 or newer. Nothing else; the build is static and cross-compiles.

## Quickest path

From a workstation with Go and SSH access:

```bash
git clone https://github.com/christophcemper/asustor-realfanctrl.git
cd asustor-realfanctrl
make deploy NAS=your-nas-host
```

`make deploy` builds for linux/amd64, copies the binary and init script,
installs them, writes a default config if none exists, restarts the daemon and
prints the status. It will prompt for the NAS sudo password.

Then jump to [Verify](#verify).

## Manual install

### 1. Build

```bash
make build
```

Produces a static `realfanctrld`. To build without make:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o realfanctrld .
```

### 2. Copy to the NAS

```bash
scp realfanctrld init.d/S60realfanctrld .bash_aliases your-nas-host:~/
```

### 3. Install (on the NAS)

```bash
sudo install -m 755 ~/realfanctrld /usr/local/bin/realfanctrld
sudo install -m 755 ~/S60realfanctrld /usr/local/etc/init.d/S60realfanctrld
sudo /usr/local/bin/realfanctrld -write-config
sudo /usr/local/etc/init.d/S60realfanctrld start
```

### 4. Shell aliases (optional but recommended)

**ADM ships no bash.** `/bin/sh` is busybox ash, `/bin/bash` does not exist, and
`~/.bashrc` is read by nothing. Busybox reads `~/.profile` for login shells, so
hook it in there:

```bash
printf '%s\n' '[ -f "$HOME/.bash_aliases" ] && . "$HOME/.bash_aliases"' >> ~/.profile
. ~/.bash_aliases
```

This gives you `realfanctrl start|stop|restart|status|log` plus the `rfc-*`
diagnostics listed in the README, on every future login.

> **Gotcha:** busybox's `.` and `source` search `$PATH` when the argument has no
> slash in it, so `source .bash_aliases` fails with `not found`. Always give it
> a path — `. ~/.bash_aliases` or `. ./.bash_aliases`.

The file is deliberately POSIX-sh clean so it works under both busybox ash and
bash/zsh. Note that shell **functions** cannot have hyphens in their names under
busybox (`rfc-remote()` fails with `bad function name`), which is why the two
multi-host helpers are `rfc_remote` / `rfc_all`, with hyphenated aliases
provided as well.

## Verify

```bash
realfanctrl status
```

Check three things:

1. **It is running** — `realfanctrld running (pid …)`.
2. **The sensors look sane** — every bay listed, no wild values. If a drive shows
   a hot spot far above its Composite that never changes, see
   [Bogus sensors](#a-drive-reports-a-constant-bogus-temperature).
3. **The fans respond** — the `Fan[n] speed is …` lines at the bottom should show
   a PWM near the curve target and RPM well above ADM's idle (~1500 / ~2200).

Then watch it work:

```bash
realfanctrl log
```

You should see lines like `PWM 158 — ssd 81 C on M.2-6` as temperatures move.

## Recommended ADM settings

These do **not** lift ADM's hardcoded PWM ceiling — nothing does. What they do
is make ADM's own fallback safer for when `realfanctrld` is stopped: with
`FanSpeed=High` and `Level=3`, `emboardmand` works from its High baseline
(PWM ~70) rather than Low (~40).

Audit them:

```bash
realfanctrld -check
```

Apply them (writes via ADM's own `confutil`, then restarts `emboardmand`):

```bash
sudo /usr/local/bin/realfanctrld -apply-config
```

What it sets:

| File | Section | Key | Value | Why |
|------|---------|-----|-------|-----|
| `/etc/nas.conf` | `Hardware` | `FanNumber` | `2` | chassis has two fans; ADM may ship `1` |
| `/etc/nas.conf` | `Hardware` | `IsSmartFan` | `No` | puts `emboardmand` in Custom Mode |
| `/etc/nas.conf` | `Hardware` | `FanSpeed` | `High` | selects the High baseline as fallback |
| `/usr/etc/emboard.conf` | `Fan1` | `Mode` | `0` | fixed-level rather than auto |
| `/usr/etc/emboard.conf` | `Fan1` | `Level` | `3` | level 3 = High |
| `/etc/nas.conf` | `Hardware` | `HighSpeed` | `220` | states intent; **no observed effect** |

Both files are symlinks into `/volume0`, so these edits survive reboots.
`-apply-config` skips anything already correct and reports what it changed.

To revert, set them back with `confutil`, e.g.:

```bash
sudo /usr/bin/confutil -set /etc/nas.conf Hardware IsSmartFan Yes
```

## Tuning the curve

```bash
rfc-conf     # edits /usr/local/etc/realfanctrl.conf, then restarts
```

Curves are lists of `{temp, pwm}` points, linearly interpolated, clamped at both
ends. The applied PWM is the highest any curve asks for. Start conservative
(cool early) and relax once you have watched real load.

Every config change needs a restart — the daemon reads the file only at startup:

```bash
realfanctrl restart
```

## Why it survives reboots

ADM's root filesystem is a **ramdisk**, rebuilt from firmware on every boot:

| Path | Persistent? |
|------|-------------|
| `/etc/init.d/` | ❌ ramdisk — custom init scripts are lost |
| `/usr/builtin/etc/crontabs/` | ❌ ramdisk — cron entries are lost |
| `/etc/nas.conf` | ✅ symlink to `/volume0/etc/nas.conf` |
| `/usr/etc/emboard.conf` | ✅ symlink to `/volume0/usr/etc/` |
| `/usr/local/**` | ✅ symlink to `/volume1/.@plugins` |

That is why everything installs under `/usr/local`. At boot,
`/etc/init.d/S99chk_config` links ADM's `rcS.pluginsfs` into place, which runs
every `S??*` script in `/usr/local/etc/init.d/` — including
`S60realfanctrld` — invoking it as `<script> start`.

A **major ADM upgrade may still remove it.** After any ADM update, run
`realfanctrl status` and reinstall if needed.

## Troubleshooting

### "not running" but the fans changed anyway

`emboardmand` is still active — that is by design. `realfanctrld` overrides it
while running; stopping the daemon simply returns control to ADM.

### The status output disagrees with the running daemon

The daemon reads its binary and config **once, at startup**. If you installed a
new build or edited the config without restarting, `-status` (a fresh process)
will show the new behaviour while the daemon still runs the old one. The status
output warns about this. Fix:

```bash
realfanctrl restart
```

### Reported PWM is 10–20 below what the curve asked for

Expected. `emboardmand` nudges PWM down ~3 every 2.5 s between our 2-second
writes, so the value settles slightly below target. To tighten, set
`"interval_sec": 1`.

### A drive reports a constant bogus temperature

Some drives expose an unimplemented sensor register as a fixed value. Observed
case: a Kingston SKC3000D reporting exactly `83 °C` on Sensor 2 while its
Composite read 34 °C on an idle drive, never changing.

Tell the two apart:

| | Real hot spot | Bogus register |
|---|---|---|
| Over time | varies with load | frozen at one value |
| Composite | tracks it | ignores it entirely |
| Siblings | similar gaps on similar drives | unique to that model |

Exclude a bogus one:

```json
"ignore_sensors": ["M.2-8:Sensor 2"]
```

Then `realfanctrl restart`. It still appears in `status`, marked `[ignored: …]`,
so it is never silently dropped.

### Fans are at maximum and temperatures still climb

Then the problem is airflow or drive mounting, not fan speed. Compare bays: in
RAID the I/O per drive is near-identical, so a large temperature spread between
identical drives under identical load points at heatsink or thermal-pad contact,
or a dead spot in the chassis airflow. Verify uniform load with:

```bash
grep -E 'nvme[0-9]+n1 ' /proc/diskstats
```

### `source .bash_aliases` says "not found"

Busybox's `.` / `source` searches `$PATH` unless the argument contains a slash.
Use `. ~/.bash_aliases`. If you instead get `syntax error: bad function name`,
you have an old copy of the file — busybox rejects hyphenated function names;
take the current one.

### `fanctrl` fails with `-13`

Permission denied — you are not root. Everything that touches the MCU needs
`sudo`; the aliases already include it.

## Uninstall

```bash
make uninstall NAS=your-nas-host
```

Or on the NAS:

```bash
sudo /usr/local/etc/init.d/S60realfanctrld stop
sudo rm -f /usr/local/etc/init.d/S60realfanctrld /usr/local/bin/realfanctrld
sudo rm -f /usr/local/etc/realfanctrl.conf   # optional
```

ADM resumes fan control immediately — with its original ceiling and all the
consequences described in [ASUSTOR-DEFECTS.md](ASUSTOR-DEFECTS.md).
