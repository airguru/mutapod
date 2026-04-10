// Package azure implements the Provider interface for Microsoft Azure VMs.
package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mutapod/mutapod/internal/config"
	"github.com/mutapod/mutapod/internal/provider"
	"github.com/mutapod/mutapod/internal/shell"
)

func init() {
	provider.Register("azure", func(cfg *config.Config, cmd shell.Commander) (provider.Provider, error) {
		return New(cfg, cmd), nil
	})
}

// Provider implements provider.Provider for Azure VMs.
type Provider struct {
	cfg  *config.Config
	cmd  shell.Commander
	name string
}

// New creates a new Azure Provider.
func New(cfg *config.Config, cmd shell.Commander) *Provider {
	return &Provider{
		cfg:  cfg,
		cmd:  cmd,
		name: cfg.InstanceName(),
	}
}

func (p *Provider) Name() string                               { return "azure" }
func (p *Provider) PreferredSyncBackend() provider.SyncBackend { return provider.SyncMutagen }
func (p *Provider) ForwardedWorkspacePath() string             { return p.cfg.WorkspacePath() }

// State returns the current power state of the Azure VM.
func (p *Provider) State(ctx context.Context) (provider.InstanceState, error) {
	az := p.cfg.Provider.Azure
	out, err := p.cmd.Output(ctx, shell.RunOptions{}, "az", "vm", "show",
		"--resource-group", az.ResourceGroup,
		"--name", p.name,
		"--show-details",
		"--query", "powerState",
		"--output", "tsv",
	)
	if err != nil {
		if isNotFound(err) {
			return provider.StateNotFound, nil
		}
		return provider.StateUnknown, fmt.Errorf("azure: show vm: %w", err)
	}
	return azurePowerStateToState(strings.TrimSpace(string(out))), nil
}

func azurePowerStateToState(s string) provider.InstanceState {
	switch strings.ToLower(s) {
	case "vm running":
		return provider.StateRunning
	case "vm stopped", "vm deallocated":
		return provider.StateStopped
	case "vm stopping", "vm deallocating":
		return provider.StateStopping
	case "vm starting":
		return provider.StateStarting
	case "":
		return provider.StateNotFound
	default:
		return provider.StateUnknown
	}
}

// EnsureInstance creates the VM if absent, or starts it if stopped.
func (p *Provider) EnsureInstance(ctx context.Context) (provider.InstanceState, error) {
	state, err := p.State(ctx)
	if err != nil {
		return provider.StateUnknown, err
	}

	switch state {
	case provider.StateRunning:
		shell.Debugf("azure: VM %s is already running", p.name)
		return provider.StateRunning, nil

	case provider.StateNotFound:
		shell.Debugf("azure: creating VM %s", p.name)
		if err := p.createVM(ctx); err != nil {
			return provider.StateUnknown, err
		}

	case provider.StateStopped:
		shell.Debugf("azure: starting stopped VM %s", p.name)
		if err := p.startVM(ctx); err != nil {
			return provider.StateUnknown, err
		}

	case provider.StateStarting, provider.StateStopping:
		shell.Debugf("azure: VM %s is transitioning (%s), waiting...", p.name, state)

	default:
		return provider.StateUnknown, fmt.Errorf("azure: VM in unexpected state %q", state)
	}

	return p.waitForRunning(ctx)
}

func (p *Provider) createVM(ctx context.Context) error {
	az := p.cfg.Provider.Azure
	args := []string{
		"vm", "create",
		"--resource-group", az.ResourceGroup,
		"--name", p.name,
		"--location", az.Location,
		"--size", az.VMSize,
		"--image", az.Image,
		"--admin-username", az.AdminUsername,
		"--generate-ssh-keys",
		"--output", "json",
	}
	if az.Subscription != "" {
		args = append(args, "--subscription", az.Subscription)
	}
	if az.VNet != "" {
		args = append(args, "--vnet-name", az.VNet)
	}
	if az.Subnet != "" {
		args = append(args, "--subnet", az.Subnet)
	}
	if az.Identity != "" {
		args = append(args, "--assign-identity", az.Identity)
	}
	return p.cmd.Run(ctx, shell.RunOptions{}, "az", args...)
}

func (p *Provider) startVM(ctx context.Context) error {
	az := p.cfg.Provider.Azure
	return p.cmd.Run(ctx, shell.RunOptions{}, "az", "vm", "start",
		"--resource-group", az.ResourceGroup,
		"--name", p.name,
	)
}

func (p *Provider) waitForRunning(ctx context.Context) (provider.InstanceState, error) {
	deadline := time.Now().Add(10 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return provider.StateUnknown, ctx.Err()
		default:
		}
		state, err := p.State(ctx)
		if err != nil {
			return provider.StateUnknown, err
		}
		if state == provider.StateRunning {
			return provider.StateRunning, nil
		}
		if time.Now().After(deadline) {
			return state, fmt.Errorf("azure: timed out waiting for VM %s to reach running (current: %s)", p.name, state)
		}
		shell.Debugf("azure: waiting for VM %s to be running (current: %s)", p.name, state)
		select {
		case <-ctx.Done():
			return provider.StateUnknown, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// SSHConfig injects SSH config via az ssh config and returns connection params.
func (p *Provider) SSHConfig(ctx context.Context) (*provider.SSHConfig, error) {
	az := p.cfg.Provider.Azure
	if err := p.cmd.Run(ctx, shell.RunOptions{}, "az", "ssh", "config",
		"--resource-group", az.ResourceGroup,
		"--name", p.name,
		"--file", "~/.ssh/config",
		"--overwrite",
	); err != nil {
		return nil, fmt.Errorf("azure: ssh config: %w", err)
	}

	// Get the public IP for constructing the host alias
	out, err := p.cmd.Output(ctx, shell.RunOptions{}, "az", "vm", "show",
		"--resource-group", az.ResourceGroup,
		"--name", p.name,
		"--show-details",
		"--query", "publicIps",
		"--output", "tsv",
	)
	if err != nil {
		return nil, fmt.Errorf("azure: get public IP: %w", err)
	}

	// az ssh config injects the host alias as the VM name
	host := p.name
	_ = json.Unmarshal(out, &host) // best effort; fallback to name
	host = strings.TrimSpace(string(out))
	if host == "" {
		host = p.name
	}

	return &provider.SSHConfig{
		Host: host,
		Port: 22,
		User: az.AdminUsername,
	}, nil
}

// Exec runs a command on the remote VM via SSH.
func (p *Provider) Exec(ctx context.Context, cmd []string, opts provider.ExecOptions) error {
	sshCfg, err := p.SSHConfig(ctx)
	if err != nil {
		return err
	}
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-p", fmt.Sprintf("%d", sshCfg.Port),
		fmt.Sprintf("%s@%s", sshCfg.User, sshCfg.Host),
	}
	args = append(args, cmd...)
	return p.cmd.Run(ctx, shell.RunOptions{
		Stdin:  opts.Stdin,
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
	}, "ssh", args...)
}

// CopyFile copies a local file to the remote VM via scp.
func (p *Provider) CopyFile(ctx context.Context, localPath, remotePath string) error {
	sshCfg, err := p.SSHConfig(ctx)
	if err != nil {
		return err
	}
	dest := fmt.Sprintf("%s@%s:%s", sshCfg.User, sshCfg.Host, remotePath)
	return p.cmd.Run(ctx, shell.RunOptions{}, "scp",
		"-o", "StrictHostKeyChecking=no",
		"-P", fmt.Sprintf("%d", sshCfg.Port),
		localPath, dest,
	)
}

// StopInstance deallocates the VM.
func (p *Provider) StopInstance(ctx context.Context) error {
	state, err := p.State(ctx)
	if err != nil {
		return err
	}
	if state == provider.StateStopped || state == provider.StateNotFound {
		return nil
	}
	az := p.cfg.Provider.Azure
	return p.cmd.Run(ctx, shell.RunOptions{}, "az", "vm", "deallocate",
		"--resource-group", az.ResourceGroup,
		"--name", p.name,
	)
}

// DeleteInstance deletes the VM and its resources.
func (p *Provider) DeleteInstance(ctx context.Context) error {
	az := p.cfg.Provider.Azure
	return p.cmd.Run(ctx, shell.RunOptions{}, "az", "vm", "delete",
		"--resource-group", az.ResourceGroup,
		"--name", p.name,
		"--yes",
	)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "resourcenotfound") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "could not be found")
}
