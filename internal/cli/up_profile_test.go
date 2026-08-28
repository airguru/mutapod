package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mutapod/mutapod/internal/config"
	"github.com/mutapod/mutapod/internal/shell"
	"github.com/mutapod/mutapod/internal/state"
	"github.com/mutapod/mutapod/internal/vscode"
)

func TestHeadlessSkipsProfileDetection(t *testing.T) {
	badProfilePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badProfilePath, []byte("test"), 0600); err != nil {
		t.Fatalf("write profile path: %v", err)
	}
	enabled := true
	cfg := &config.Config{
		Name: "demo",
		Profiles: config.ProfilesConfig{
			Codex: config.ProfileSyncConfig{
				Enabled:   &enabled,
				LocalPath: badProfilePath,
			},
		},
	}

	active, err := activeProfilesForLaunchMode(cfg, vscode.LaunchHeadless)
	if err != nil {
		t.Fatalf("headless profile selection: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("headless profiles: got %d, want 0", len(active))
	}

	for _, mode := range []vscode.LaunchMode{vscode.LaunchAttached, vscode.LaunchLocal} {
		if _, err := activeProfilesForLaunchMode(cfg, mode); err == nil {
			t.Fatalf("%s mode should retain profile detection", mode)
		}
	}
}

func TestHeadlessSkipsVSCodePreparation(t *testing.T) {
	if launchModeUsesVSCode(vscode.LaunchHeadless) {
		t.Fatal("headless mode should skip VS Code preparation")
	}
	for _, mode := range []vscode.LaunchMode{vscode.LaunchAttached, vscode.LaunchLocal} {
		if !launchModeUsesVSCode(mode) {
			t.Fatalf("%s mode should retain VS Code preparation", mode)
		}
	}
}

func TestTerminateSavedProfileSyncs(t *testing.T) {
	fake := shell.NewFakeCommander()
	terminateSavedProfileSyncs(context.Background(), "mutagen", fake, []state.ProfileSyncState{
		{Name: "codex", SessionName: "mutapod-demo-profile-codex"},
		{Name: "codex-without-local-state"},
		{Name: "claude", SessionName: "mutapod-demo-profile-claude"},
	})

	if !fake.CalledWith("mutagen", "sync", "terminate", "mutapod-demo-profile-codex") {
		t.Fatal("expected saved Codex profile session to be terminated")
	}
	if !fake.CalledWith("mutagen", "sync", "terminate", "mutapod-demo-profile-claude") {
		t.Fatal("expected saved Claude profile session to be terminated")
	}
	if got := fake.CallCount("mutagen"); got != 2 {
		t.Fatalf("Mutagen calls: got %d, want 2", got)
	}
}

func TestShouldRefreshProfileSessionWithoutSavedState(t *testing.T) {
	if !shouldRefreshProfileSession(state.ProfileSyncState{}, false, "sig") {
		t.Fatal("expected missing saved state to trigger one-time refresh")
	}
}

func TestShouldRefreshProfileSessionWithMissingSignature(t *testing.T) {
	prior := state.ProfileSyncState{SessionConfig: ""}
	if !shouldRefreshProfileSession(prior, true, "sig") {
		t.Fatal("expected missing signature to trigger refresh")
	}
}

func TestShouldRefreshProfileSessionWithChangedSignature(t *testing.T) {
	prior := state.ProfileSyncState{SessionConfig: "old"}
	if !shouldRefreshProfileSession(prior, true, "new") {
		t.Fatal("expected changed signature to trigger refresh")
	}
}

func TestShouldRefreshProfileSessionWithMatchingSignature(t *testing.T) {
	prior := state.ProfileSyncState{SessionConfig: "same"}
	if shouldRefreshProfileSession(prior, true, "same") {
		t.Fatal("expected matching signature to keep existing session")
	}
}

func TestEffectiveProfileSyncMode(t *testing.T) {
	if got := effectiveProfileSyncMode("two-way-safe", "two-way-resolved"); got != "two-way-safe" {
		t.Fatalf("explicit profile mode: got %q", got)
	}
	if got := effectiveProfileSyncMode("", "two-way-resolved"); got != "two-way-resolved" {
		t.Fatalf("inherited profile mode: got %q", got)
	}
}

func TestCodexProfileMigrationQuarantinesNonPortableStateOnce(t *testing.T) {
	cmd := codexProfileMigrationCommand(
		"/var/lib/mutapod/profiles/codex",
		"/var/lib/mutapod/runtime/codex-sqlite",
	)

	for _, expected := range []string{
		"profile='/var/lib/mutapod/profiles/codex'",
		"runtime='/var/lib/mutapod/runtime/codex-sqlite'",
		"marker=\"$runtime/.portable-profile-v1\"",
		"sessions|archived_sessions|attachments|generated_images|visualizations|memories|rules|skills|AGENTS.md|auth.json|config.toml",
		"/var/lib/mutapod/profile-backups/codex-runtime/",
		"sudo mv \"$entry\" \"$backup\"/",
		"sudo touch \"$marker\"",
	} {
		if !strings.Contains(cmd, expected) {
			t.Fatalf("cleanup command missing %q:\n%s", expected, cmd)
		}
	}
}
