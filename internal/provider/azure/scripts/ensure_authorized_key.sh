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
user_id="$(id -u "$username")"

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
case "$marker" in
    mutapod-*) ;;
    *)
        echo "mutapod: invalid managed SSH key marker" >&2
        exit 1
        ;;
esac

sshd_bin="$(command -v sshd || true)"
authorized_keys_files=""
if [ -n "$sshd_bin" ]; then
    effective_sshd="$($sshd_bin -T -C "user=$username,host=$(hostname),addr=127.0.0.1")"
    if ! printf '%s\n' "$effective_sshd" | grep -q '^pubkeyauthentication yes$'; then
        echo "mutapod: sshd public-key authentication is disabled" >&2
        exit 1
    fi
    authorized_keys_files="$(printf '%s\n' "$effective_sshd" | awk '$1 == "authorizedkeysfile" { for (i = 2; i <= NF; i++) print $i }')"
fi
if [ -z "$authorized_keys_files" ]; then
    authorized_keys_files=".ssh/authorized_keys"
fi

installed_count=0
for configured_path in $authorized_keys_files; do
    if [ "$configured_path" = "none" ]; then
        continue
    fi
    expanded_path="$(printf '%s\n' "$configured_path" | awk -v h="$home_dir" -v u="$username" -v uid="$user_id" '
        {
            token = sprintf("%c", 28)
            gsub(/%%/, token)
            gsub(/%h/, h)
            gsub(/%u/, u)
            gsub(/%U/, uid)
            gsub(token, "%")
            print
        }
    ')"
    case "$expanded_path" in
        /*) authorized_keys="$expanded_path" ;;
        *) authorized_keys="$home_dir/$expanded_path" ;;
    esac

    key_dir="$(dirname "$authorized_keys")"
    case "$authorized_keys" in
        "$home_dir"/*)
            install -d -m 700 -o "$username" -g "$group_name" "$key_dir"
            touch "$authorized_keys"
            chown "$username:$group_name" "$authorized_keys"
            ;;
        *)
            install -d -m 755 -o root -g root "$key_dir"
            touch "$authorized_keys"
            chown root:root "$authorized_keys"
            ;;
    esac
    chmod 600 "$authorized_keys"

    tmp="$(mktemp "$key_dir/authorized_keys.mutapod.XXXXXX")"
    trap 'rm -f "$tmp"' EXIT
    awk -v marker="$marker" 'NF < 3 || $NF != marker' "$authorized_keys" > "$tmp"
    printf '%s %s\n' "$public_key" "$marker" >> "$tmp"
    case "$authorized_keys" in
        "$home_dir"/*) chown "$username:$group_name" "$tmp" ;;
        *) chown root:root "$tmp" ;;
    esac
    chmod 600 "$tmp"
    mv "$tmp" "$authorized_keys"
    trap - EXIT

    if ! grep -Fqx "$public_key $marker" "$authorized_keys"; then
        echo "mutapod: failed to verify installed SSH public key" >&2
        exit 1
    fi
    installed_count=$((installed_count + 1))
done

if [ "$installed_count" -eq 0 ]; then
    echo "mutapod: sshd has no file-based AuthorizedKeysFile" >&2
    exit 1
fi

key_sha256="$(printf '%s' "$public_key" | sha256sum | awk '{print $1}')"
printf 'mutapod:ssh-key-installed:%s:%s:%s\n' "$marker" "$key_sha256" "$installed_count"
