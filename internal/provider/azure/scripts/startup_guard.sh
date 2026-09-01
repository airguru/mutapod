set -eu

mode="$1"
idle_timer="mutapod-idle-check.timer"

case "$mode" in
    restart|disable) ;;
    *)
        echo "mutapod: invalid startup guard mode" >&2
        exit 1
        ;;
esac

if ! systemctl list-unit-files --type=timer --no-legend "$idle_timer" 2>/dev/null | grep -q "^$idle_timer"; then
    echo "mutapod:startup-guard-not-needed:$mode"
    exit 0
fi

systemctl stop "$idle_timer"

if [ "$mode" = "restart" ]; then
    guard_unit="mutapod-startup-guard-$(date +%s)-$$"
    systemd-run --quiet --unit="$guard_unit" --on-active=30m /bin/systemctl start "$idle_timer"
    if ! systemctl is-active --quiet "$guard_unit.timer"; then
        echo "mutapod: startup guard timer did not become active" >&2
        exit 1
    fi
elif systemctl is-active --quiet "$idle_timer"; then
    echo "mutapod: idle timer remained active" >&2
    exit 1
fi

echo "mutapod:startup-guard-active:$mode"
