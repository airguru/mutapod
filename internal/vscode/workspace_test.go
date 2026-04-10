package vscode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mutapod/mutapod/internal/config"
	"github.com/mutapod/mutapod/internal/provider"
)

func TestConfigureWorkspace_WritesWorkspaceFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Name: "testproject", Dir: dir}
	sshCfg := &provider.SSHConfig{Host: "mutapod-testproject.example", User: "pavel"}

	path, err := ConfigureWorkspace(cfg, sshCfg, "mutapod-testproject")
	if err != nil {
		t.Fatalf("ConfigureWorkspace: %v", err)
	}
	if path != filepath.Join(dir, workspaceFilename) {
		t.Fatalf("workspace path: got %q", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}

	var workspace workspaceFile
	if err := json.Unmarshal(data, &workspace); err != nil {
		t.Fatalf("parse workspace file: %v", err)
	}

	if len(workspace.Folders) != 1 || workspace.Folders[0].Path != "." {
		t.Fatalf("unexpected folders: %#v", workspace.Folders)
	}
	assertDockerContextSetting(t, workspace.Settings, "containers.environment")
	assertDockerContextSetting(t, workspace.Settings, "terminal.integrated.env.windows")
	assertDockerContextSetting(t, workspace.Settings, "terminal.integrated.env.linux")
	assertDockerContextSetting(t, workspace.Settings, "terminal.integrated.env.osx")
	assertDockerHostSetting(t, workspace.Settings, "docker.environment")
	if workspace.Settings["docker.context"] != "mutapod-testproject" {
		t.Fatalf("expected docker.context, got %#v", workspace.Settings["docker.context"])
	}
	if workspace.Settings["docker.host"] != "ssh://pavel@mutapod-testproject.example" {
		t.Fatalf("expected docker.host, got %#v", workspace.Settings["docker.host"])
	}
}

func assertDockerContextSetting(t *testing.T, settings map[string]any, key string) {
	t.Helper()

	env := settings[key].(map[string]any)
	if env["DOCKER_CONTEXT"] != "mutapod-testproject" {
		t.Fatalf("expected DOCKER_CONTEXT for %s, got %#v", key, env["DOCKER_CONTEXT"])
	}
}

func assertDockerHostSetting(t *testing.T, settings map[string]any, key string) {
	t.Helper()

	env := settings[key].(map[string]any)
	if env["DOCKER_HOST"] != "ssh://pavel@mutapod-testproject.example" {
		t.Fatalf("expected DOCKER_HOST for %s, got %#v", key, env["DOCKER_HOST"])
	}
}
