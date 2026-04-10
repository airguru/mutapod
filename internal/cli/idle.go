package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/mutapod/mutapod/internal/config"
	"github.com/mutapod/mutapod/internal/deps"
	"github.com/mutapod/mutapod/internal/idle"
	"github.com/mutapod/mutapod/internal/provider"
	"github.com/mutapod/mutapod/internal/shell"
	"github.com/mutapod/mutapod/internal/sshrun"
	"github.com/mutapod/mutapod/internal/state"
	mutagensync "github.com/mutapod/mutapod/internal/sync"
)

func idleHeartbeatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "idle-heartbeat",
		Short:  "Internal heartbeat worker for mutapod idle shutdown",
		Hidden: true,
		RunE:   runIdleHeartbeat,
	}
	return cmd
}

func runIdleHeartbeat(_ *cobra.Command, _ []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	lock, err := idle.HeartbeatLock(cfg.Name)
	if err != nil {
		return err
	}
	locked, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer lock.Unlock()

	st, err := state.Load(cfg.Name)
	if err != nil {
		return err
	}
	if st.Instance.LastKnownIP == "" || st.SSH.User == "" || st.SSH.IdentityFile == "" {
		return nil
	}

	mutagenPath, err := deps.MutagenPath()
	if err != nil {
		return err
	}
	sshCfg := &provider.SSHConfig{
		Host:         st.SSH.Host,
		IP:           st.Instance.LastKnownIP,
		Port:         st.SSH.Port,
		User:         st.SSH.User,
		IdentityFile: st.SSH.IdentityFile,
	}
	syncMgr := mutagensync.New(cfg, sshCfg, mutagenPath, shell.DefaultCommander)
	client := sshrun.New(sshCfg.IP, sshCfg.Port, sshCfg.User, sshCfg.IdentityFile)

	hostID, _ := os.Hostname()
	ctx := context.Background()
	interval := idle.HeartbeatInterval(cfg)

	for {
		status, err := syncMgr.SyncStatus(ctx)
		if err != nil {
			return nil
		}
		if !mutagensync.IsActiveSyncStatus(status) {
			return nil
		}

		expiresAt := idle.LeaseExpiry(cfg, time.Now())
		if err := idle.WriteLeaseWithClient(ctx, client, cfg.Name, hostID, expiresAt); err != nil {
			shell.Debugf("idle heartbeat write lease: %v", err)
			return nil
		}

		time.Sleep(interval)
	}
}

func maybeConfigureIdle(ctx context.Context, cfg *config.Config, prov provider.Provider, sshCfg *provider.SSHConfig) error {
	step("Configuring lease tracking...")
	if err := idle.InstallRemote(ctx, prov); err != nil {
		return err
	}

	sshClient := sshrun.New(sshCfg.IP, sshCfg.Port, sshCfg.User, sshCfg.IdentityFile)
	hostID, _ := os.Hostname()
	if err := idle.WriteLeaseWithRetry(ctx, sshClient, cfg.Name, hostID, idle.LeaseExpiry(cfg, time.Now())); err != nil {
		return fmt.Errorf("idle: write initial lease: %w", err)
	}
	if cfg.Idle.IsEnabled() {
		if err := idle.EnableTimer(ctx, prov); err != nil {
			return fmt.Errorf("idle: enable timer: %w", err)
		}
	}
	if err := startIdleHeartbeat(cfg); err != nil {
		return fmt.Errorf("idle: start heartbeat: %w", err)
	}
	if cfg.Idle.IsEnabled() {
		ok("Idle shutdown configured")
	} else {
		ok("Lease tracking configured")
	}
	return nil
}

func startIdleHeartbeat(cfg *config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}

	args := []string{"idle-heartbeat"}
	if cfgFile != "" {
		args = append(args, "--config", cfgFile)
	} else {
		args = append(args, "--config", configPath(cfg))
	}

	cmd := exec.Command(exe, args...)
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()

	cmd.Stdin = nil
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func maybeHandleIdleDown(ctx context.Context, cfg *config.Config, prov provider.Provider) error {
	if err := idle.RemoveLease(ctx, prov, cfg.Name); err != nil {
		shell.Debugf("idle remove lease: %v", err)
	}
	if cfg.Idle.IsEnabled() {
		if err := idle.TriggerCheckNow(ctx, prov); err != nil {
			shell.Debugf("idle trigger check: %v", err)
		}
		return nil
	}
	return prov.StopInstance(ctx)
}

func configPath(cfg *config.Config) string {
	return filepath.Join(cfg.Dir, "mutapod.yaml")
}
