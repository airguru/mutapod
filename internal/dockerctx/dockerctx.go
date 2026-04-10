package dockerctx

import (
	"context"
	"fmt"

	"github.com/mutapod/mutapod/internal/config"
	"github.com/mutapod/mutapod/internal/provider"
	"github.com/mutapod/mutapod/internal/shell"
)

// EnsureContext creates or updates a project-scoped Docker context pointing at
// the remote mutapod host. It never switches the globally active context.
func EnsureContext(ctx context.Context, cfg *config.Config, sshCfg *provider.SSHConfig, cmd shell.Commander) (string, error) {
	contextName := cfg.InstanceName()
	dockerEndpoint := fmt.Sprintf("host=ssh://%s@%s", sshCfg.User, sshCfg.Host)

	if err := cmd.Run(ctx, shell.RunOptions{}, "docker", "context", "inspect", contextName); err == nil {
		if err := cmd.Run(ctx, shell.RunOptions{}, "docker", "context", "update",
			contextName,
			"--description", "mutapod workspace "+cfg.Name,
			"--docker", dockerEndpoint,
		); err != nil {
			return "", fmt.Errorf("docker context update %s: %w", contextName, err)
		}
		return contextName, nil
	}

	if err := cmd.Run(ctx, shell.RunOptions{}, "docker", "context", "create",
		contextName,
		"--description", "mutapod workspace "+cfg.Name,
		"--docker", dockerEndpoint,
	); err != nil {
		return "", fmt.Errorf("docker context create %s: %w", contextName, err)
	}
	return contextName, nil
}
