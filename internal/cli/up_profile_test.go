package cli

import (
	"strings"
	"testing"

	"github.com/mutapod/mutapod/internal/state"
)

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
