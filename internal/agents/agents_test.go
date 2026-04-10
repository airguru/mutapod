package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mutapod/mutapod/internal/config"
)

func testConfig(dir string) *config.Config {
	return &config.Config{
		Name: "testproject",
		Dir:  dir,
		Provider: config.ProviderConfig{
			Type: "gcp",
		},
		Sync: config.SyncConfig{
			Mode: "two-way-resolved",
		},
		Compose: config.ComposeConfig{
			File:            "compose-dev.yaml",
			PrimaryService:  "web",
			WorkspaceFolder: "/app",
		},
	}
}

func TestEnsureCreatesAgentsFile(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)

	path, err := Ensure(cfg)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if path != filepath.Join(dir, filename) {
		t.Fatalf("path: got %q", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "This repository is developed with `mutapod`.") {
		t.Fatalf("missing mutapod guidance: %s", text)
	}
	if !strings.Contains(text, "`mutapod up --build`") {
		t.Fatalf("missing build guidance: %s", text)
	}
}

func TestEnsureAmendsManagedBlock(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	path := filepath.Join(dir, filename)
	original := "# Existing\n\nKeep this.\n\n" + beginMarker + "\nold\n" + endMarker + "\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	if _, err := Ensure(cfg); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "Keep this.") {
		t.Fatal("expected existing content to be preserved")
	}
	if strings.Contains(text, "\nold\n") {
		t.Fatal("expected old managed block to be replaced")
	}
	if !strings.Contains(text, "Primary service for attached-container workflows: `web`") {
		t.Fatal("expected regenerated managed block")
	}
}

func TestEnsureAppendsManagedBlockWhenMissingMarkers(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte("# Existing\n"), 0644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	if _, err := Ensure(cfg); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "# Existing") {
		t.Fatal("expected existing content to remain")
	}
	if !strings.Contains(text, beginMarker) || !strings.Contains(text, endMarker) {
		t.Fatal("expected managed block markers to be appended")
	}
}
