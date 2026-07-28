# asustor-realfanctrl — shell helpers
#
# Author:     Christoph C. Cemper / Magosol Kft.
# Repository: https://github.com/christophcemper/asustor-realfanctrl
# License:    MIT — see LICENSE.  NO WARRANTY; tested only on ASUSTOR AS6812F.
#
# POSIX sh compatible on purpose. ADM ships NO bash: /bin/sh is busybox ash and
# /bin/bash does not exist, so nothing here may use bashisms. In particular,
# function names must be plain names — busybox rejects `rfc-remote()` with
# "bad function name" — so the functions use underscores and hyphenated
# aliases are provided for convenience.
#
# On the NAS, busybox reads ~/.profile for login shells (NOT ~/.bashrc, which
# nothing reads). Install with:
#
#     cp .bash_aliases ~/.bash_aliases
#     printf '%s\n' '[ -f "$HOME/.bash_aliases" ] && . "$HOME/.bash_aliases"' >> ~/.profile
#     . ~/.bash_aliases
#
# NOTE: busybox's `.` and `source` search $PATH when the argument contains no
# slash, so `source .bash_aliases` fails with "not found". Always use a path:
# `. ~/.bash_aliases` or `. ./.bash_aliases`.
#
# On a workstation (bash/zsh) source it too, and set RFC_HOSTS to your NAS ssh
# targets to drive them remotely with rfc_all / rfc_remote.

# ---------------------------------------------------------------------------
# Main CLI:  realfanctrl start|stop|restart|status|log
# ---------------------------------------------------------------------------
alias realfanctrl='sudo /usr/local/etc/init.d/S60realfanctrld'

# Short forms for the things you type most.
alias rfc='realfanctrl'
alias rfc-status='sudo /usr/local/etc/init.d/S60realfanctrld status'
alias rfc-start='sudo /usr/local/etc/init.d/S60realfanctrld start'
alias rfc-stop='sudo /usr/local/etc/init.d/S60realfanctrld stop'
alias rfc-restart='sudo /usr/local/etc/init.d/S60realfanctrld restart'

# ---------------------------------------------------------------------------
# Diagnostics
# ---------------------------------------------------------------------------

# Follow the daemon log (bay name included on every PWM change).
alias rfc-log='sudo tail -f /usr/local/var/log/realfanctrld.log'

# Temperatures and the PWM the curve is asking for, without touching the fans.
alias rfc-temps='sudo /usr/local/bin/realfanctrld -config /usr/local/etc/realfanctrl.conf -status'

# What the MCU actually reports right now — the ground truth for both fans.
alias rfc-rpm='sudo /usr/sbin/fanctrl -getfanspeed'

# Audit / apply ADM's own fan settings (see INSTALL.md).
alias rfc-check='sudo /usr/local/bin/realfanctrld -check'
alias rfc-apply='sudo /usr/local/bin/realfanctrld -apply-config'

# Edit the curve, then reload it.
alias rfc-conf='sudo ${EDITOR:-vi} /usr/local/etc/realfanctrl.conf && sudo /usr/local/etc/init.d/S60realfanctrld restart'

# Every temperature sensor the kernel exposes, raw — useful when adding a model.
alias rfc-sensors='for h in /sys/class/hwmon/hwmon*; do n=$(cat $h/name 2>/dev/null); for t in $h/temp*_input; do [ -f "$t" ] || continue; l=$(cat ${t%_input}_label 2>/dev/null); echo "$n $(basename $t) ${l:-nolabel} $(( $(cat $t)/1000 ))C"; done; done'

# Map NVMe bays to PCI addresses — what slot_map in the config is keyed on.
alias rfc-slots='for h in /sys/class/hwmon/hwmon*; do [ "$(cat $h/name 2>/dev/null)" = nvme ] || continue; echo "$(basename $(readlink -f $h/device)) $(basename $(readlink -f $h/device/device))"; done | sort -V'

# ASUSTOR's own fan logic, verbose. Ctrl-C to stop; it prints the hardcoded
# PWM ceiling in Init_Fan_Attribute and the Composite-only thermal decisions.
alias rfc-adm-debug='sudo /usr/sbin/emboardmand -debug'

# ---------------------------------------------------------------------------
# Multi-NAS helpers (run from a workstation)
# ---------------------------------------------------------------------------

# Set to your NAS ssh targets, e.g. in ~/.profile:
#     export RFC_HOSTS="cccnas6 cccnas7"
: "${RFC_HOSTS:=}"

# rfc_remote <host> [start|stop|restart|status]
rfc_remote() {
    _rfc_host="$1"
    if [ -z "$_rfc_host" ]; then
        echo "usage: rfc_remote <host> [start|stop|restart|status]"
        return 1
    fi
    shift
    ssh -t "$_rfc_host" "sudo /usr/local/etc/init.d/S60realfanctrld ${*:-status}"
}

# rfc_all [start|stop|status|...] — same command across every host in RFC_HOSTS.
rfc_all() {
    if [ -z "$RFC_HOSTS" ]; then
        echo 'set RFC_HOSTS first, e.g. export RFC_HOSTS="nas1 nas2"'
        return 1
    fi
    for _rfc_h in $RFC_HOSTS; do
        echo "=== $_rfc_h ==="
        rfc_remote "$_rfc_h" "$@"
    done
}

# Hyphenated spellings, for consistency with the rfc-* aliases above.
alias rfc-remote='rfc_remote'
alias rfc-all='rfc_all'
