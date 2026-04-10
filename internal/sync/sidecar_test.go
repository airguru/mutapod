package sync

import (
	"context"
	"testing"

	"github.com/mutapod/mutapod/internal/provider"
	"github.com/mutapod/mutapod/internal/shell"
)

func TestSidecarEnsure_CreateUsesExpectedFlags(t *testing.T) {
	fake := shell.NewFakeCommander()
	sshCfg := &provider.SSHConfig{Host: "example-host", User: "alice"}
	spec := SidecarSpec{
		SessionName:    "mutapod-demo-profile-codex",
		Label:          "mutapod-name=demo-profile-codex",
		LocalPath:      `C:\Users\pavel\.codex`,
		RemotePath:     "/var/lib/mutapod/profiles/codex",
		Mode:           "two-way-resolved",
		IgnorePatterns: []string{"cache", "cache/**"},
	}
	session := NewSidecar(spec, sshCfg, "mutagen", fake)
	fake.Stub("Status: Watching for changes\n", "mutagen", "sync", "list", spec.SessionName)

	if err := session.create(context.Background()); err != nil {
		t.Fatalf("create: %v", err)
	}

	if !fake.CalledWith(
		"mutagen",
		"sync", "create",
		"--name", "mutapod-demo-profile-codex",
		"--label", "mutapod-name=demo-profile-codex",
		"--no-global-configuration",
		"--sync-mode", "two-way-resolved",
		"--ignore", "cache",
		"--ignore", "cache/**",
		`C:\Users\pavel\.codex`,
		"alice@example-host:/var/lib/mutapod/profiles/codex",
	) {
		t.Fatalf("expected sidecar sync create, got %#v", fake.Calls)
	}
}

func TestSidecarConfigSignature_ChangesWithIgnoreRules(t *testing.T) {
	sshCfg := &provider.SSHConfig{Host: "example-host", User: "alice"}
	base := NewSidecar(SidecarSpec{
		SessionName:    "one",
		Label:          "mutapod-name=one",
		LocalPath:      "/local",
		RemotePath:     "/remote",
		Mode:           "two-way-resolved",
		IgnorePatterns: []string{"cache"},
	}, sshCfg, "mutagen", shell.NewFakeCommander())
	changed := NewSidecar(SidecarSpec{
		SessionName:    "one",
		Label:          "mutapod-name=one",
		LocalPath:      "/local",
		RemotePath:     "/remote",
		Mode:           "two-way-resolved",
		IgnorePatterns: []string{"cache", "tmp"},
	}, sshCfg, "mutagen", shell.NewFakeCommander())

	if base.ConfigSignature() == changed.ConfigSignature() {
		t.Fatal("expected config signature to change when ignore rules change")
	}
}
