package cli

import (
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
