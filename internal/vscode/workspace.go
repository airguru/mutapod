package vscode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mutapod/mutapod/internal/config"
	"github.com/mutapod/mutapod/internal/provider"
)

const workspaceFilename = "mutapod.code-workspace"

func WorkspaceFilename() string {
	return workspaceFilename
}

type workspaceFile struct {
	Folders  []workspaceFolder `json:"folders"`
	Settings map[string]any    `json:"settings"`
}

type workspaceFolder struct {
	Path string `json:"path"`
}

// ConfigureWorkspace writes a project-local .code-workspace file that applies
// remote Docker settings only when opened through that workspace wrapper.
func ConfigureWorkspace(cfg *config.Config, sshCfg *provider.SSHConfig, dockerContext string) (string, error) {
	dockerHost := fmt.Sprintf("ssh://%s@%s", sshCfg.User, sshCfg.Host)
	data := workspaceFile{
		Folders: []workspaceFolder{{Path: "."}},
		Settings: map[string]any{
			"containers.environment": map[string]string{
				"DOCKER_CONTEXT": dockerContext,
			},
			"terminal.integrated.env.windows": map[string]string{
				"DOCKER_CONTEXT": dockerContext,
			},
			"terminal.integrated.env.linux": map[string]string{
				"DOCKER_CONTEXT": dockerContext,
			},
			"terminal.integrated.env.osx": map[string]string{
				"DOCKER_CONTEXT": dockerContext,
			},
			"docker.host":    dockerHost,
			"docker.context": dockerContext,
			"remote.extensionKind": map[string][]string{
				"ms-azuretools.vscode-containers": {"ui"},
			},
			"docker.environment": map[string]string{
				"DOCKER_HOST": dockerHost,
			},
		},
	}

	path := filepath.Join(cfg.Dir, workspaceFilename)
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("vscode: marshal workspace: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0644); err != nil {
		return "", fmt.Errorf("vscode: write workspace file: %w", err)
	}
	return path, nil
}
