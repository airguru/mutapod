set -eu

username="$1"
public_key_b64="$2"
marker="$3"

home_dir="$(getent passwd "$username" | cut -d: -f6)"
if [ -z "$home_dir" ]; then
    echo "mutapod: SSH user not found: $username" >&2
    exit 1
fi

group_name="$(id -gn "$username")"
ssh_dir="$home_dir/.ssh"
authorized_keys="$ssh_dir/authorized_keys"

install -d -m 700 -o "$username" -g "$group_name" "$ssh_dir"
touch "$authorized_keys"
chown "$username:$group_name" "$authorized_keys"
chmod 600 "$authorized_keys"

case $((${#public_key_b64} % 4)) in
    0) ;;
    2) public_key_b64="${public_key_b64}==" ;;
    3) public_key_b64="${public_key_b64}=" ;;
    *)
        echo "mutapod: invalid SSH public key encoding" >&2
        exit 1
        ;;
esac

public_key="$(printf '%s' "$public_key_b64" | base64 -d)"
case "$public_key" in
    ssh-*' '*) ;;
    *)
        echo "mutapod: decoded SSH public key is empty or invalid" >&2
        exit 1
        ;;
esac
tmp="$(mktemp "$ssh_dir/authorized_keys.mutapod.XXXXXX")"
trap 'rm -f "$tmp"' EXIT

awk -v marker="$marker" 'NF < 3 || $NF != marker' "$authorized_keys" > "$tmp"
printf '%s %s\n' "$public_key" "$marker" >> "$tmp"
chown "$username:$group_name" "$tmp"
chmod 600 "$tmp"
mv "$tmp" "$authorized_keys"

if ! grep -Fqx "$public_key $marker" "$authorized_keys"; then
    echo "mutapod: failed to verify installed SSH public key" >&2
    exit 1
fi

trap - EXIT
