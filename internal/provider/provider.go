// Package provider defines the interface all cloud/platform drivers implement.
package provider

import (
	"context"
	"io"
)

// InstanceState represents the lifecycle state of a VM or pod.
type InstanceState string

const (
	StateNotFound InstanceState = "not_found"
	StateStopped  InstanceState = "stopped"
	StateStarting InstanceState = "starting"
	StateRunning  InstanceState = "running"
	StateStopping InstanceState = "stopping"
	StateUnknown  InstanceState = "unknown"
)

// SSHConfig contains everything needed to SSH into the instance.
type SSHConfig struct {
	// Host is the SSH config alias (e.g. gcloud host alias) used by mutagen.
	Host string
	// IP is the raw external IP used for direct Go SSH connections.
	IP           string
	Port         int
	User         string
	IdentityFile string
}

// ExecOptions controls how a remote command is run.
type ExecOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Tty    bool
}

// SyncBackend selects the sync mechanism the provider prefers.
type SyncBackend string

const (
	SyncMutagen SyncBackend = "mutagen"
	SyncOCRsync SyncBackend = "oc_rsync"
)

// Provider is the single interface all cloud drivers implement.
type Provider interface {
	// Name returns the human-readable provider name ("gcp").
	Name() string

	// EnsureInstance provisions the instance if it does not exist, or starts it
	// if it is stopped. Must be idempotent.
	EnsureInstance(ctx context.Context) (InstanceState, error)

	// State returns the current lifecycle state without mutating anything.
	State(ctx context.Context) (InstanceState, error)

	// SSHConfig returns the parameters needed to SSH into the instance.
	// May run a cloud CLI command to inject the host into ~/.ssh/config.
	SSHConfig(ctx context.Context) (*SSHConfig, error)

	// Exec runs cmd on the remote instance (via SSH).
	Exec(ctx context.Context, cmd []string, opts ExecOptions) error

	// CopyFile copies a single local file to remotePath on the instance.
	CopyFile(ctx context.Context, localPath, remotePath string) error

	// PreferredSyncBackend returns which sync mechanism the provider wants.
	PreferredSyncBackend() SyncBackend

	// StopInstance stops the instance. Must be idempotent.
	StopInstance(ctx context.Context) error

	// DeleteInstance destroys the instance and all associated resources.
	DeleteInstance(ctx context.Context) error

	// ForwardedWorkspacePath returns the path on the remote host where the
	// workspace should be placed.
	ForwardedWorkspacePath() string
}
