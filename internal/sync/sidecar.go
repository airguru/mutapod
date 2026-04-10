package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/mutapod/mutapod/internal/provider"
	"github.com/mutapod/mutapod/internal/shell"
)

// SidecarSpec describes an additional Mutagen sync outside the main workspace.
type SidecarSpec struct {
	SessionName    string
	Label          string
	LocalPath      string
	RemotePath     string
	Mode           string
	IgnoreVCS      bool
	IgnorePatterns []string
}

// SidecarSession manages an extra Mutagen sync session.
type SidecarSession struct {
	spec        SidecarSpec
	sshCfg      *provider.SSHConfig
	mutagenPath string
	cmd         shell.Commander
}

// NewSidecar creates a SidecarSession manager.
func NewSidecar(spec SidecarSpec, sshCfg *provider.SSHConfig, mutagenPath string, cmd shell.Commander) *SidecarSession {
	return &SidecarSession{
		spec:        spec,
		sshCfg:      sshCfg,
		mutagenPath: mutagenPath,
		cmd:         cmd,
	}
}

func (s *SidecarSession) SessionName() string { return s.spec.SessionName }

func (s *SidecarSession) ConfigSignature() string {
	parts := []string{
		"v1",
		"session=" + s.spec.SessionName,
		"label=" + s.spec.Label,
		"mode=" + s.spec.Mode,
		"local=" + s.spec.LocalPath,
		"remote=" + s.remote(),
	}
	if s.spec.IgnoreVCS {
		parts = append(parts, "ignore-vcs=true")
	}
	parts = append(parts, "ignores="+strings.Join(s.spec.IgnorePatterns, "\n"))
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func (s *SidecarSession) Ensure(ctx context.Context) error {
	status, err := s.syncStatus(ctx, s.spec.SessionName)
	if err != nil {
		return err
	}
	switch status {
	case "watching", "scanning", "reconciling", "staging", "transitioning":
		return nil
	case "paused":
		if err := s.cmd.Run(ctx, shell.RunOptions{}, s.mutagenPath, "sync", "resume", s.spec.SessionName); err != nil {
			return fmt.Errorf("sync: resume sidecar session: %w", err)
		}
		return s.waitForWatching(ctx)
	case "":
		return s.create(ctx)
	default:
		_ = s.Terminate(ctx)
		return s.create(ctx)
	}
}

func (s *SidecarSession) Flush(ctx context.Context) error {
	return s.cmd.Run(ctx, shell.RunOptions{}, s.mutagenPath, "sync", "flush", s.spec.SessionName)
}

func (s *SidecarSession) VerifyReady(ctx context.Context) error {
	out, err := s.cmd.Output(ctx, shell.RunOptions{}, s.mutagenPath, "sync", "list", s.spec.SessionName)
	if err != nil {
		return fmt.Errorf("sync: inspect sidecar session: %w", err)
	}
	status := parseSyncStatus(out)
	if !IsActiveSyncStatus(status) {
		return fmt.Errorf("sync: sidecar session is not active after flush (status: %s)", status)
	}
	if problems := parseMutagenCount(out, "Transition problems:"); problems > 0 {
		return fmt.Errorf("sync: sidecar session has %d transition problem(s) after flush", problems)
	}
	return nil
}

func (s *SidecarSession) Pause(ctx context.Context) error {
	return PauseSyncSession(ctx, s.mutagenPath, s.cmd, s.spec.SessionName)
}

func (s *SidecarSession) Terminate(ctx context.Context) error {
	return TerminateSyncSession(ctx, s.mutagenPath, s.cmd, s.spec.SessionName)
}

func PauseSyncSession(ctx context.Context, mutagenPath string, cmd shell.Commander, name string) error {
	return cmd.Run(ctx, shell.RunOptions{}, mutagenPath, "sync", "pause", name)
}

func TerminateSyncSession(ctx context.Context, mutagenPath string, cmd shell.Commander, name string) error {
	return cmd.Run(ctx, shell.RunOptions{}, mutagenPath, "sync", "terminate", name)
}

func (s *SidecarSession) create(ctx context.Context) error {
	args := []string{
		"sync", "create",
		"--name", s.spec.SessionName,
		"--label", s.spec.Label,
		"--no-global-configuration",
		"--sync-mode", s.spec.Mode,
	}
	if s.spec.IgnoreVCS {
		args = append(args, "--ignore-vcs")
	}
	for _, pattern := range s.spec.IgnorePatterns {
		args = append(args, "--ignore", pattern)
	}
	args = append(args, s.spec.LocalPath, s.remote())
	if err := s.cmd.Run(ctx, shell.RunOptions{}, s.mutagenPath, args...); err != nil {
		return fmt.Errorf("sync: create sidecar session: %w", err)
	}
	return s.waitForWatching(ctx)
}

func (s *SidecarSession) waitForWatching(ctx context.Context) error {
	deadline := time.Now().Add(90 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		status, err := s.syncStatus(ctx, s.spec.SessionName)
		if err != nil {
			return err
		}
		if status == "watching" || status == "scanning" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("sync: timed out waiting for sidecar session to become active (status: %s)", status)
		}
		time.Sleep(2 * time.Second)
	}
}

func (s *SidecarSession) syncStatus(ctx context.Context, name string) (string, error) {
	out, err := s.cmd.Output(ctx, shell.RunOptions{}, s.mutagenPath, "sync", "list", name)
	if err != nil {
		if isNoSessions(err) {
			return "", nil
		}
		return "", fmt.Errorf("sync: list sidecar sessions: %w", err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return "", nil
	}
	return parseSyncStatus(out), nil
}

func (s *SidecarSession) remote() string {
	return fmt.Sprintf("%s@%s:%s", s.sshCfg.User, s.sshCfg.Host, s.spec.RemotePath)
}
