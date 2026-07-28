# asustor-realfanctrl — shell helpers
#
# Author:     Christoph C. Cemper / Magosol Kft.
# Repository: https://github.com/christophcemper/asustor-realfanctrl
# License:    MIT — see LICENSE.  NO WARRANTY; tested only on ASUSTOR AS6812F.
#
# On the NAS:  copy to ~/.bash_aliases and add to ~/.bashrc (see INSTALL.md):
#     [ -f ~/.bash_aliases ] && . ~/.bash_aliases
#
# On a workstation: source it too, and set RFC_HOSTS to your NAS ssh targets
# to drive them remotely with rfc-all / rfc-remote.

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

# Edit the curve, then reload it.
alias rfc-conf='sudo ${EDITOR:-vi} /usr/local/etc/realfanctrl.conf && sudo /usr/local/etc/init.d/S60realfanctrld restart'

# Every temperature sensor the kernel exposes, raw — useful when adding a model.
alias rfc-sensors='for h in /sys/class/hwmon/hwmon*; do n=$(cat $h/name 2>/dev/null); for t in $h/temp*_input; do [ -f "$t" ] || continue; l=$(cat ${t%_input}_label 2>/dev/null); echo "$n $(basename $t) ${l:-nolabel} $(( $(cat $t)/1000 ))C"; done; done'

# Map NVMe bays to PCI addresses — what slot_map in the config is keyed on.
alias rfc-slots='for h in /sys/class/hwmon/hwmon*; do [ "$(cat $h/name 2>/dev/null)" = nvme ] || continue; echo "$(basename $(readlink -f $h/device)) $(basename $(readlink -f $h/device/device))"; done | sort -V'

# ASUSTOR'"'"'s own fan logic, verbose. Ctrl-C to stop; it prints the hardcoded
# PWM ceiling in Init_Fan_Attribute and the Composite-only thermal decisions.
alias rfc-adm-debug='sudo /usr/sbin/emboardmand -debug'

# ---------------------------------------------------------------------------
# Multi-NAS helpers (run from a workstation)
# ---------------------------------------------------------------------------

# Set to your NAS ssh targets, e.g. in ~/.bashrc:
#     export RFC_HOSTS="cccnas6 cccnas7"
: "${RFC_HOSTS:=}"

# rfc-remote <host> [start|stop|status|...]
rfc-remote() {
    local host="$1"; shift
    [ -n "$host" ] || { echo "usage: rfc-remote <host> [start|stop|restart|status]"; return 1; }
    ssh -t "$host" "sudo /usr/local/etc/init.d/S60realfanctrld ${*:-status}"
}

# rfc-all [start|stop|status|...] — same command across every host in RFC_HOSTS.
rfc-all() {
    [ -n "$RFC_HOSTS" ] || { echo "set RFC_HOSTS first, e.g. export RFC_HOSTS=\"nas1 nas2\""; return 1; }
    local h
    for h in $RFC_HOSTS; do
        echo "=== $h ==="
        rfc-remote "$h" "$@"
    done
}
