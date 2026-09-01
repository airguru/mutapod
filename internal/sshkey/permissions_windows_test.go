//go:build windows

package sshkey

import (
	"os/exec"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

func TestManagedPrivateKeyAcceptedByWindowsOpenSSH(t *testing.T) {
	sshKeygen, err := exec.LookPath("ssh-keygen.exe")
	if err != nil {
		t.Skip("Windows OpenSSH ssh-keygen.exe is unavailable")
	}
	tempHome(t)
	pair, err := EnsureManaged("azure", "windows-openssh-acl-test")
	if err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(sshKeygen, "-y", "-f", pair.PrivatePath).CombinedOutput()
	if err != nil {
		t.Fatalf("Windows OpenSSH rejected managed key: %v\n%s", err, out)
	}
	key, _, _, _, err := gossh.ParseAuthorizedKey(out)
	if err != nil {
		t.Fatalf("parse Windows OpenSSH public key: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key))); got != pair.PublicKey {
		t.Fatalf("Windows OpenSSH derived unexpected public key:\n got %q\nwant %q", got, pair.PublicKey)
	}
}
