package sshkey

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

func TestEnsureManagedGeneratesAndReusesPair(t *testing.T) {
	home := tempHome(t)
	target := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm"

	first, err := EnsureManaged("azure", target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.PrivatePath, filepath.Join(home, ".mutapod", "keys", "azure")) {
		t.Fatalf("unexpected private key path: %q", first.PrivatePath)
	}
	if !strings.HasPrefix(first.Marker, "mutapod-") {
		t.Fatalf("unexpected marker: %q", first.Marker)
	}
	privateData, err := os.ReadFile(first.PrivatePath)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.ParsePrivateKey(privateData)
	if err != nil {
		t.Fatalf("parse generated private key: %v", err)
	}
	if got := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey()))); got != first.PublicKey {
		t.Fatalf("public key does not match private key")
	}

	second, err := EnsureManaged("azure", target)
	if err != nil {
		t.Fatal(err)
	}
	if second.PrivatePath != first.PrivatePath || second.PublicKey != first.PublicKey || second.Marker != first.Marker {
		t.Fatalf("managed key was not reused: first=%#v second=%#v", first, second)
	}
}

func TestEnsureManagedRepairsPublicKeyFile(t *testing.T) {
	tempHome(t)
	pair, err := EnsureManaged("azure", "target")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pair.PublicPath, []byte("stale\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repaired, err := EnsureManaged("azure", "target")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(repaired.PublicPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != repaired.AuthorizedKey {
		t.Fatalf("public key file was not repaired: %q", string(data))
	}
}

func TestEnsureManagedRotatesCorruptPrivateKey(t *testing.T) {
	tempHome(t)
	pair, err := EnsureManaged("azure", "target")
	if err != nil {
		t.Fatal(err)
	}
	oldPublicKey := pair.PublicKey
	if err := os.WriteFile(pair.PrivatePath, []byte("not a private key\n"), 0600); err != nil {
		t.Fatal(err)
	}

	rotated, err := EnsureManaged("azure", "target")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.PublicKey == oldPublicKey {
		t.Fatal("corrupt managed key was not rotated")
	}
	archives, err := filepath.Glob(pair.PrivatePath + ".invalid-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 {
		t.Fatalf("expected one archived invalid private key, got %v", archives)
	}
}

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}
