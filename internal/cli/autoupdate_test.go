package cli

import "testing"

func TestShouldInstallUpdateDefaultsToYes(t *testing.T) {
	for _, answer := range []string{"", "y", "Y", "yes", " YES "} {
		if !shouldInstallUpdate(answer) {
			t.Errorf("answer %q should install the update", answer)
		}
	}
}

func TestShouldInstallUpdateHonorsExplicitNo(t *testing.T) {
	for _, answer := range []string{"n", "N", "no", "later"} {
		if shouldInstallUpdate(answer) {
			t.Errorf("answer %q should keep the current version", answer)
		}
	}
}
