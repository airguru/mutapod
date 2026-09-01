package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mutapod/mutapod/internal/agents"
	"github.com/mutapod/mutapod/internal/bootstrap"
	"github.com/mutapod/mutapod/internal/compose"
	"github.com/mutapod/mutapod/internal/config"
	"github.com/mutapod/mutapod/internal/deps"
	"github.com/mutapod/mutapod/internal/dockerctx"
	"github.com/mutapod/mutapod/internal/ignore"
	"github.com/mutapod/mutapod/internal/portrelay"
	"github.com/mutapod/mutapod/internal/profiles"
	"github.com/mutapod/mutapod/internal/provider"
	"github.com/mutapod/mutapod/internal/shell"
	"github.com/mutapod/mutapod/internal/sshforward"
	"github.com/mutapod/mutapod/internal/state"
	mutagensync "github.com/mutapod/mutapod/internal/sync"
	"github.com/mutapod/mutapod/internal/vscode"
)

func upCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up [local|container|headless]",
		Short: "Provision VM, sync files, and start services",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runUp,
	}
	cmd.Flags().Bool("build", false, "force docker compose to rebuild images before starting services")
	cmd.Flags().Bool("replace", false, "approve VM replacement when its declarative configuration changed")
	cmd.Flags().Bool("adopt", false, "adopt an existing legacy VM without recreating it")
	return cmd
}

func runUp(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	launchMode, err := parseUpLaunchMode(args)
	if err != nil {
		return err
	}

	buildImages, err := cmd.Flags().GetBool("build")
	if err != nil {
		return err
	}
	replaceVM, err := cmd.Flags().GetBool("replace")
	if err != nil {
		return err
	}
	adoptVM, err := cmd.Flags().GetBool("adopt")
	if err != nil {
		return err
	}
	if replaceVM && adoptVM {
		return fmt.Errorf("--replace and --adopt cannot be used together")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	return runUpWithConfig(ctx, cfg, launchMode, buildImages, vmUpOptions{
		Replace:     replaceVM,
		Adopt:       adoptVM,
		Interactive: isTerminal(os.Stdin) && isTerminal(os.Stdout),
		In:          os.Stdin,
		Out:         os.Stdout,
	})
}

func runUpWithConfig(ctx context.Context, cfg *config.Config, launchMode vscode.LaunchMode, buildImages bool, vmOpts vmUpOptions) error {
	step("Loaded config: %s (%s)", cfg.Name, cfg.Provider.Type)
	leaseOpts := leaseOptionsForLaunchMode(launchMode)

	if err := confirmMissingIgnoreFile(os.Stdin, os.Stdout, cfg); err != nil {
		return err
	}

	step("Updating AGENTS.md...")
	agentsPath, ensured, err := ensureAgentsForStartup(vmOpts.In, vmOpts.Out, vmOpts.Interactive, cfg)
	if err != nil {
		return err
	}
	if ensured {
		ok("AGENTS.md ready: %s", agentsPath)
	} else {
		ok("AGENTS.md mutapod block skipped")
	}

	step("Checking local dependencies...")
	mutagenPath, err := deps.MutagenPath()
	if err != nil {
		return fmt.Errorf("deps: %w", err)
	}
	shell.Debugf("mutagen: %s", mutagenPath)

	st, err := state.Load(cfg.Name)
	if err != nil {
		return err
	}

	prov, err := provider.New(cfg, shell.DefaultCommander)
	if err != nil {
		return err
	}

	st, err = prepareDeclarativeVM(ctx, cfg, prov, st, vmOpts)
	if err != nil {
		return err
	}

	step("Ensuring VM is running...")
	instanceState, err := prov.EnsureInstance(ctx)
	if err != nil {
		return err
	}
	ok("VM running: %s (%s)", cfg.InstanceName(), instanceState)

	instanceMetadata, err := prov.InstanceMetadata(ctx)
	if err != nil {
		return err
	}
	desiredFingerprint, err := cfg.VMConfigFingerprint()
	if err != nil {
		return err
	}
	if instanceMetadata.ConfigFingerprint != desiredFingerprint {
		return fmt.Errorf("VM configuration fingerprint was not applied correctly")
	}

	step("Configuring SSH access...")
	sshCfg, err := prov.SSHConfig(ctx)
	if err != nil {
		return err
	}
	ok("SSH access: %s", sshCfg.Host)

	idleRefresher, err := maybeConfigureIdleLease(ctx, cfg, prov, sshCfg, leaseOpts)
	if err != nil {
		return err
	}
	defer func() {
		if idleRefresher != nil {
			idleRefresher.Stop()
		}
	}()

	ipChanged := st.Instance.LastKnownIP != "" && st.Instance.LastKnownIP != sshCfg.IP

	activeProfiles, err := activeProfilesForLaunchMode(cfg, launchMode)
	if err != nil {
		return err
	}

	step("Bootstrapping VM (docker, docker compose)...")
	if err := bootstrap.Run(ctx, prov); err != nil {
		return err
	}
	ok("Bootstrap complete")

	step("Preparing remote workspace...")
	if err := ensureRemoteWorkspace(ctx, prov, cfg.WorkspacePath(), sshCfg.User); err != nil {
		return err
	}
	ok("Remote workspace ready: %s", cfg.WorkspacePath())

	if len(activeProfiles) > 0 {
		step("Preparing personal AI profile directories...")
		if err := ensureRemoteProfilePaths(ctx, prov, activeProfiles, sshCfg.User); err != nil {
			return err
		}
		ok("Personal AI profiles ready: %s", strings.Join(profileNames(activeProfiles), ", "))
	}

	step("Starting Mutagen daemon...")
	if err := mutagensync.DaemonStart(ctx, mutagenPath, shell.DefaultCommander); err != nil {
		shell.Debugf("mutagen daemon start: %v (may already be running)", err)
	}

	step("Starting file sync...")
	syncMgr := mutagensync.New(cfg, sshCfg, mutagenPath, shell.DefaultCommander)

	ignorePatterns, err := ignore.Load(cfg.Dir)
	if err != nil {
		return fmt.Errorf("sync: load ignore patterns: %w", err)
	}
	ignoreSignature := ignorePatterns.Signature()
	sessionConfigSignature, err := syncMgr.SessionConfigSignature(ctx)
	if err != nil {
		return err
	}

	if ipChanged {
		forwardPorts, reversePorts, err := portsForSessionCleanup(cfg, st)
		if err != nil {
			return err
		}
		shell.Debugf("IP changed (%s -> %s), recreating Mutagen sessions", st.Instance.LastKnownIP, sshCfg.IP)
		syncMgr.TerminateAllSessions(ctx, forwardPorts, reversePorts)
		if launchModeUsesProfiles(launchMode) {
			terminateSavedProfileSyncs(ctx, mutagenPath, shell.DefaultCommander, st.Profiles)
		}
	}
	if !launchModeUsesProfiles(launchMode) && len(st.Profiles) > 0 {
		step("Stopping personal AI profile syncs for headless mode...")
		terminateSavedProfileSyncs(ctx, mutagenPath, shell.DefaultCommander, st.Profiles)
		ok("Personal AI profile syncs stopped")
	}
	if st.Sync.IgnoreSignature != "" && st.Sync.IgnoreSignature != ignoreSignature {
		shell.Debugf("ignore rules changed, recreating Mutagen sync session")
		if err := syncMgr.TerminateSync(ctx); err != nil {
			shell.Debugf("terminate sync for ignore refresh: %v", err)
		}
	} else if st.Sync.IgnoreSignature == "" && st.Sync.SessionName != "" {
		shell.Debugf("ignore signature missing from state, recreating Mutagen sync session once")
		if err := syncMgr.TerminateSync(ctx); err != nil {
			shell.Debugf("terminate sync for ignore refresh: %v", err)
		}
	} else if st.Sync.SessionConfig != "" && st.Sync.SessionConfig != sessionConfigSignature {
		shell.Debugf("sync session settings changed, recreating Mutagen sync session")
		if err := syncMgr.TerminateSync(ctx); err != nil {
			shell.Debugf("terminate sync for config refresh: %v", err)
		}
	} else if st.Sync.SessionConfig == "" && st.Sync.SessionName != "" {
		shell.Debugf("sync session config signature missing from state, recreating Mutagen sync session once")
		if err := syncMgr.TerminateSync(ctx); err != nil {
			shell.Debugf("terminate sync for config refresh: %v", err)
		}
	}

	if err := syncMgr.EnsureSync(ctx); err != nil {
		return err
	}
	localPath, err := cfg.LocalSyncPath()
	if err != nil {
		return err
	}
	ok("Sync active: %s -> %s:%s", localPath, sshCfg.Host, cfg.WorkspacePath())

	step("Waiting for initial sync...")
	if err := waitForInitialSync(ctx, prov, syncMgr, cfg); err != nil {
		return err
	}
	ok("Initial sync complete")

	profileStates := make([]state.ProfileSyncState, 0, len(activeProfiles))
	if len(activeProfiles) > 0 {
		step("Syncing personal AI profiles...")
		existingProfileState := make(map[string]state.ProfileSyncState, len(st.Profiles))
		for _, profileState := range st.Profiles {
			existingProfileState[profileState.Name] = profileState
		}
		activeProfileSet := make(map[string]bool, len(activeProfiles))
		for _, name := range profileStateKeys(activeProfiles) {
			activeProfileSet[name] = true
		}
		for _, profileState := range st.Profiles {
			if activeProfileSet[profileState.Name] {
				continue
			}
			if profileState.SessionName == "" {
				continue
			}
			if err := mutagensync.TerminateSyncSession(ctx, mutagenPath, shell.DefaultCommander, profileState.SessionName); err != nil {
				shell.Debugf("terminate stale profile sync %s: %v", profileState.Name, err)
			}
		}
		for _, spec := range activeProfiles {
			if spec.SessionName == "" || spec.LocalPath == "" || spec.SyncRemotePath == "" {
				if prior, ok := existingProfileState[spec.Name]; ok && prior.SessionName != "" {
					if err := mutagensync.TerminateSyncSession(ctx, mutagenPath, shell.DefaultCommander, prior.SessionName); err != nil {
						shell.Debugf("terminate profile sync for no-local-state refresh: %v", err)
					}
				}
				profileStates = append(profileStates, state.ProfileSyncState{
					Name:       spec.Name,
					LocalPath:  spec.LocalPath,
					RemotePath: spec.SyncRemotePath,
				})
				continue
			}
			session := mutagensync.NewSidecar(mutagensync.SidecarSpec{
				SessionName:    spec.SessionName,
				Label:          "mutapod-name=" + cfg.Name + "-profile-" + spec.Name,
				LocalPath:      spec.LocalPath,
				RemotePath:     spec.SyncRemotePath,
				Mode:           effectiveProfileSyncMode(spec.SyncMode, cfg.Sync.Mode),
				IgnorePatterns: spec.IgnorePatterns,
			}, sshCfg, mutagenPath, shell.DefaultCommander)
			signature := session.ConfigSignature()
			if prior, ok := existingProfileState[spec.Name]; shouldRefreshProfileSession(prior, ok, signature) {
				if ok {
					shell.Debugf("profile %s sync settings changed, recreating Mutagen session", spec.Name)
				} else {
					shell.Debugf("profile %s has no saved sync state, recreating Mutagen session once", spec.Name)
				}
				sessionName := spec.SessionName
				if ok && prior.SessionName != "" {
					sessionName = prior.SessionName
				}
				if err := mutagensync.TerminateSyncSession(ctx, mutagenPath, shell.DefaultCommander, sessionName); err != nil {
					shell.Debugf("terminate profile sync for refresh: %v", err)
				}
			}
			if err := session.Ensure(ctx); err != nil {
				return err
			}
			if err := session.Flush(ctx); err != nil {
				shell.Debugf("profile %s sync flush: %v", spec.Name, err)
			}
			if err := session.VerifyReady(ctx); err != nil {
				return err
			}
			if spec.Name == "codex" {
				if err := migrateRemoteCodexProfile(ctx, prov, spec.SyncRemotePath, spec.RuntimeRemotePath); err != nil {
					return fmt.Errorf("profile codex portable-data migration: %w", err)
				}
			}
			profileStates = append(profileStates, state.ProfileSyncState{
				Name:          spec.Name,
				SessionName:   spec.SessionName,
				LocalPath:     spec.LocalPath,
				RemotePath:    spec.SyncRemotePath,
				SessionConfig: signature,
			})
			for _, extra := range spec.SupplementalSyncs {
				extraSession := mutagensync.NewSidecar(mutagensync.SidecarSpec{
					SessionName:    extra.SessionName,
					Label:          "mutapod-name=" + cfg.Name + "-profile-" + extra.Name,
					LocalPath:      extra.LocalPath,
					RemotePath:     extra.RemotePath,
					Mode:           effectiveProfileSyncMode(extra.SyncMode, cfg.Sync.Mode),
					IgnorePatterns: extra.IgnorePatterns,
				}, sshCfg, mutagenPath, shell.DefaultCommander)
				extraSignature := extraSession.ConfigSignature()
				if prior, ok := existingProfileState[extra.Name]; shouldRefreshProfileSession(prior, ok, extraSignature) {
					if ok {
						shell.Debugf("profile %s sync settings changed, recreating Mutagen session", extra.Name)
					} else {
						shell.Debugf("profile %s has no saved sync state, recreating Mutagen session once", extra.Name)
					}
					sessionName := extra.SessionName
					if ok && prior.SessionName != "" {
						sessionName = prior.SessionName
					}
					if err := mutagensync.TerminateSyncSession(ctx, mutagenPath, shell.DefaultCommander, sessionName); err != nil {
						shell.Debugf("terminate profile sync for refresh: %v", err)
					}
				}
				if err := extraSession.Ensure(ctx); err != nil {
					return err
				}
				if err := extraSession.Flush(ctx); err != nil {
					shell.Debugf("profile %s sync flush: %v", extra.Name, err)
				}
				if err := extraSession.VerifyReady(ctx); err != nil {
					return err
				}
				profileStates = append(profileStates, state.ProfileSyncState{
					Name:          extra.Name,
					SessionName:   extra.SessionName,
					LocalPath:     extra.LocalPath,
					RemotePath:    extra.RemotePath,
					SessionConfig: extraSignature,
				})
			}
		}
		ok("Personal AI profiles synced: %s", strings.Join(profileNames(activeProfiles), ", "))
	}

	if launchModeUsesVSCode(launchMode) {
		if err := removeRemoteWorkspaceWrapper(ctx, prov, cfg); err != nil {
			shell.Debugf("remove remote workspace wrapper: %v", err)
		}
	}

	if len(cfg.Compose.ReverseForwards) > 0 {
		step("Exposing local services to the remote VM: %v...", cfg.Compose.ReverseForwards)
		for _, port := range cfg.Compose.ReverseForwards {
			if err := syncMgr.EnsureReverseForward(ctx, port); err != nil {
				return fmt.Errorf("reverse forward %d: %w", port, err)
			}
		}
		ok("Local services exposed: %v", cfg.Compose.ReverseForwards)
	}

	step("Preparing compose overrides...")
	overrideApplied, err := compose.EnsureRemoteOverride(ctx, prov, cfg, activeProfiles)
	if err != nil {
		return err
	}
	if overrideApplied {
		ok("Compose override ready for service %s", cfg.Compose.PrimaryService)
	} else {
		ok("Compose overrides ready")
	}

	step("Starting services (docker compose up)...")
	if err := compose.Up(ctx, prov, cfg, activeProfiles, buildImages); err != nil {
		return err
	}
	ok("Services running")

	if cfg.Compose.PrimaryService != "" {
		step("Finalizing workspace permissions...")
		if err := cleanupLegacyWorkspaceACLWatcher(ctx, prov, cfg, activeProfiles); err != nil {
			return err
		}
		ok("Workspace permissions ready")

		step("Configuring git safe directory in the main container...")
		if err := compose.ConfigureGitSafeDirectory(ctx, prov, cfg, activeProfiles); err != nil {
			shell.Debugf("git safe.directory: %v", err)
			fmt.Fprintf(os.Stderr, "  warning: could not configure git safe.directory in container: %v\n", err)
		} else {
			ok("Git safe directory configured")
		}
	}

	if len(activeProfiles) > 0 && cfg.Compose.PrimaryService != "" {
		step("Configuring personal AI tools in the main container...")
		if err := profiles.EnsureRemoteTools(ctx, composeProfileRunner{prov: prov}, cfg, activeProfiles); err != nil {
			return err
		}
		ok("Personal AI tools ready: %s", strings.Join(profileNames(activeProfiles), ", "))
	}

	step("Configuring local Docker context...")
	dockerContext, err := dockerctx.EnsureContext(ctx, cfg, sshCfg, shell.DefaultCommander)
	if err != nil {
		return err
	}
	ok("Docker context configured: %s", dockerContext)

	var ports []int
	composePath, err := compose.DetectFile(cfg)
	if err != nil {
		shell.Debugf("compose file not found, skipping port forwarding: %v", err)
	} else {
		ports, err = compose.ParsePorts(composePath, cfg.Compose.ExtraPorts)
		if err != nil {
			return fmt.Errorf("port config: %w", err)
		}
		if len(ports) > 0 {
			sshForwardMgr := sshforward.New(cfg, sshCfg)
			switch cfg.Compose.ForwardBackend {
			case "ssh":
				if cfg.Compose.PrimaryService != "" {
					relayPorts, err := compose.ParsePrimaryServiceTargetPorts(composePath, cfg)
					if err != nil {
						fmt.Fprintf(os.Stderr, "  warning: primary-service port relay setup skipped: %v\n", err)
					} else if len(relayPorts) > 0 {
						step("Preparing primary-service loopback relays: %v...", relayPorts)
						if err := portrelay.Ensure(ctx, prov, cfg, activeProfiles, relayPorts); err != nil {
							fmt.Fprintf(os.Stderr, "  warning: primary-service port relay setup failed: %v\n", err)
						} else {
							ok("Primary-service loopback relays ready: %v", relayPorts)
						}
					}
				}
				step("Forwarding ports with SSH compression: %v...", ports)
				for _, p := range ports {
					syncMgr.TerminateForwardVariants(ctx, p)
					if err := sshForwardMgr.Ensure(p); err != nil {
						fmt.Fprintf(os.Stderr, "  warning: SSH port %d forward failed: %v\n", p, err)
					}
				}
				ok("SSH ports forwarded: %v", ports)
			default:
				if cfg.Compose.ForwardToPrimaryService {
					containerID, err := compose.PrimaryServiceContainerID(ctx, cfg, dockerContext, shell.DefaultCommander)
					if err != nil {
						return fmt.Errorf("forward target: %w", err)
					}
					dockerHost := fmt.Sprintf("ssh://%s@%s", sshCfg.User, sshCfg.Host)
					syncMgr.ForwardToContainer(dockerHost, containerID)
					step("Forwarding ports to primary service %s: %v...", cfg.Compose.PrimaryService, ports)
				} else {
					step("Forwarding ports: %v...", ports)
				}
				for _, p := range ports {
					_ = sshForwardMgr.Stop(p)
					if err := syncMgr.EnsureForward(ctx, p); err != nil {
						fmt.Fprintf(os.Stderr, "  warning: port %d forward failed: %v\n", p, err)
					}
				}
				if cfg.Compose.ForwardToPrimaryService {
					ok("Ports forwarded to primary service: %v", ports)
				} else {
					ok("Ports forwarded: %v", ports)
				}
			}
		}
	}

	st.Name = cfg.Name
	st.ProviderType = cfg.Provider.Type
	st.LaunchMode = string(launchMode)
	st.Instance.ID = instanceMetadata.ID
	st.Instance.Name = cfg.InstanceName()
	st.Instance.TargetScope = targetScope(cfg, instanceMetadata.ID)
	st.Instance.ConfigFingerprint = instanceMetadata.ConfigFingerprint
	st.Instance.LastKnownIP = sshCfg.IP
	st.Instance.Status = string(instanceState)
	st.SSH = state.SSHState{
		Host:         sshCfg.Host,
		Port:         sshCfg.Port,
		User:         sshCfg.User,
		IdentityFile: sshCfg.IdentityFile,
	}
	st.Sync = state.SyncState{
		Backend:                "mutagen",
		SessionName:            syncMgr.SessionName(),
		LocalPath:              localPath,
		RemotePath:             cfg.WorkspacePath(),
		SessionConfig:          sessionConfigSignature,
		IgnoreSignature:        ignoreSignature,
		ForwardBackend:         cfg.Compose.ForwardBackend,
		ForwardSessions:        buildForwardSessionMap(cfg, syncMgr, ports),
		ReverseForwardSessions: buildReverseForwardSessionMap(syncMgr, cfg.Compose.ReverseForwards),
	}
	st.Profiles = profileStates
	if err := state.Save(st); err != nil {
		shell.Debugf("warning: save state: %v", err)
	}

	if launchModeUsesVSCode(launchMode) {
		step("Configuring local VS Code workspace...")
		workspaceFile, err := vscode.ConfigureWorkspace(cfg, sshCfg, dockerContext)
		if err != nil {
			return err
		}
		ok("VS Code workspace configured: %s", workspaceFile)

		attachedConfigPath, err := vscode.ConfigureAttachedContainer(ctx, cfg, dockerContext, activeProfiles, shell.DefaultCommander)
		if err != nil {
			return err
		}
		if attachedConfigPath != "" {
			ok("Attached-container defaults configured: %s", attachedConfigPath)
			if shouldPrepareAttachedContainerExtensionInstall(cfg) {
				step("Preparing attached-container extension install...")
				if err := prepareAttachedContainerExtensionInstall(ctx, prov, cfg, activeProfiles); err != nil {
					shell.Debugf("attached-container extension install prep: %v", err)
					fmt.Fprintf(os.Stderr, "  warning: could not prepare attached-container extension install: %v\n", err)
				} else {
					ok("Attached-container extension install ready")
				}
			}
		}
	}

	if err := maybeStartIdleHeartbeat(cfg, leaseOpts); err != nil {
		return err
	}
	idleRefresher.Stop()
	idleRefresher = nil

	if !launchModeUsesVSCode(launchMode) {
		ok("Headless environment ready; use mutapod ssh or mutapod exec")
	} else {
		vscode.PrintInstructions(cfg, sshCfg, ports, launchMode)
		step("Opening VS Code (%s)...", launchMode)
		if err := vscode.Launch(ctx, cfg, dockerContext, launchMode, shell.DefaultCommander); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: VS Code launch failed: %v\n", err)
		} else {
			ok("VS Code opened (%s)", launchMode)
		}
	}
	return nil
}

func downCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop services, pause sync, and stop the VM",
		RunE:  runDown,
	}
}

func runDown(_ *cobra.Command, _ []string) error {
	ctx := context.Background()

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	st, err := state.Load(cfg.Name)
	if err != nil {
		return err
	}

	mutagenPath, err := deps.MutagenPath()
	if err != nil {
		return err
	}

	sshCfg := &provider.SSHConfig{
		Host: st.SSH.Host,
		Port: st.SSH.Port,
		User: st.SSH.User,
	}
	syncMgr := mutagensync.New(cfg, sshCfg, mutagenPath, shell.DefaultCommander)

	prov, err := provider.New(cfg, shell.DefaultCommander)
	if err != nil {
		return err
	}
	if _, err := prov.SSHConfig(ctx); err != nil {
		shell.Debugf("ssh config for compose down: %v", err)
	}

	step("Stopping services (docker compose down)...")
	activeProfiles, err := activeProfilesForLaunchMode(cfg, vscode.LaunchMode(st.LaunchMode))
	if err != nil {
		shell.Debugf("compose: profile detection for down: %v", err)
		activeProfiles = nil
	}
	if err := compose.Down(ctx, prov, cfg, activeProfiles); err != nil {
		shell.Debugf("compose down: %v", err)
	}

	step("Pausing file sync...")
	if err := syncMgr.PauseSync(ctx); err != nil {
		shell.Debugf("pause sync: %v", err)
	}
	for _, profileState := range st.Profiles {
		if profileState.SessionName == "" {
			continue
		}
		if err := mutagensync.PauseSyncSession(ctx, mutagenPath, shell.DefaultCommander, profileState.SessionName); err != nil {
			shell.Debugf("pause profile sync %s: %v", profileState.Name, err)
		}
	}

	forwardPorts, reversePorts, _ := portsForSessionCleanup(cfg, st)
	if len(forwardPorts) > 0 {
		step("Pausing port forwards...")
		sshForwardMgr := sshforward.New(cfg, sshCfg)
		if activeForwardBackend(cfg, st) == "ssh" {
			sshForwardMgr.StopAll(forwardPorts)
		} else {
			syncMgr.PauseAllForwards(ctx, forwardPorts)
		}
	}
	if len(reversePorts) > 0 {
		step("Pausing reverse forwards...")
		syncMgr.PauseAllReverseForwards(ctx, reversePorts)
	}

	step("Stopping VM...")
	if err := maybeHandleIdleDown(ctx, cfg, prov); err != nil {
		return err
	}
	if cfg.Idle.IsEnabled() {
		ok("Lease released for %s; VM stops immediately if unused, otherwise after idle timeout", cfg.InstanceName())
	} else {
		ok("VM stopped: %s", cfg.InstanceName())
	}

	st.Instance.Status = string(provider.StateStopped)
	_ = state.Save(st)
	return nil
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current state of the workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			st, err := state.Load(cfg.Name)
			if err != nil {
				return err
			}
			prov, err := provider.New(cfg, shell.DefaultCommander)
			if err != nil {
				return err
			}
			instanceState, err := prov.State(ctx)
			if err != nil {
				return err
			}
			configStatus := "absent"
			if instanceState != provider.StateNotFound {
				metadata, err := prov.InstanceMetadata(ctx)
				if err != nil {
					return err
				}
				desiredFingerprint, err := cfg.VMConfigFingerprint()
				if err != nil {
					return err
				}
				switch {
				case metadata.ConfigFingerprint == "":
					configStatus = "legacy/untracked"
				case metadata.ConfigFingerprint != desiredFingerprint:
					configStatus = "replacement required"
				default:
					configStatus = "current"
				}
			}
			fmt.Printf("Workspace:  %s\n", cfg.Name)
			fmt.Printf("Provider:   %s\n", cfg.Provider.Type)
			fmt.Printf("VM:         %s (%s)\n", cfg.InstanceName(), instanceState)
			fmt.Printf("VM config:  %s\n", configStatus)
			if st.SSH.Host != "" {
				fmt.Printf("SSH host:   %s\n", st.SSH.Host)
			}
			if st.Sync.SessionName != "" {
				fmt.Printf("Sync:       %s\n", st.Sync.SessionName)
			}
			return nil
		},
	}
}

func sshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh [-- command...]",
		Short: "Open a shell or run a command on the remote VM",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			prov, err := provider.New(cfg, shell.DefaultCommander)
			if err != nil {
				return err
			}
			return runSSH(ctx, prov, args)
		},
	}
}

func runSSH(ctx context.Context, prov provider.Provider, args []string) error {
	if _, err := prov.SSHConfig(ctx); err != nil {
		return err
	}
	opts := provider.ExecOptions{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	if len(args) == 0 {
		opts.Tty = true
	}
	return prov.Exec(ctx, args, opts)
}

func execCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec -- command...",
		Short: "Run a command in the primary service container",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			prov, err := provider.New(cfg, shell.DefaultCommander)
			if err != nil {
				return err
			}
			return runExec(ctx, cfg, prov, args)
		},
	}
}

func runExec(ctx context.Context, cfg *config.Config, prov provider.Provider, args []string) error {
	if cfg.Compose.PrimaryService == "" {
		return fmt.Errorf("exec requires compose.primary_service in mutapod.yaml")
	}
	if _, err := prov.SSHConfig(ctx); err != nil {
		return err
	}
	st, err := state.Load(cfg.Name)
	if err != nil {
		return err
	}
	activeProfiles, err := activeProfilesForLaunchMode(cfg, vscode.LaunchMode(st.LaunchMode))
	if err != nil {
		return err
	}
	return compose.ExecInPrimaryServiceWithOptions(ctx, prov, cfg, activeProfiles, commandScript(args), compose.PrimaryServiceExecOptions{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
}

func commandScript(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return "exec " + strings.Join(quoted, " ")
}

func step(format string, args ...any) {
	fmt.Printf("-> "+format+"\n", args...)
}

func ok(format string, args ...any) {
	fmt.Printf("OK "+format+"\n", args...)
}

func collectPorts(sessions map[string]string) []int {
	var ports []int
	for k := range sessions {
		var p int
		fmt.Sscanf(k, "%d", &p)
		if p > 0 {
			ports = append(ports, p)
		}
	}
	return ports
}

func portsForSessionCleanup(cfg *config.Config, st *state.State) ([]int, []int, error) {
	forwardPorts := collectPorts(st.Sync.ForwardSessions)
	if len(forwardPorts) == 0 {
		composePath, err := compose.DetectFile(cfg)
		if err != nil {
			forwardPorts = nil
		} else {
			forwardPorts, err = compose.ParsePorts(composePath, cfg.Compose.ExtraPorts)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	reversePorts := collectPorts(st.Sync.ReverseForwardSessions)
	if len(reversePorts) == 0 {
		reversePorts = append(reversePorts, cfg.Compose.ReverseForwards...)
	}
	return forwardPorts, reversePorts, nil
}

func buildForwardSessionMap(cfg *config.Config, syncMgr *mutagensync.Manager, ports []int) map[string]string {
	if len(ports) == 0 {
		return nil
	}

	forwardSessions := make(map[string]string, len(ports))
	for _, port := range ports {
		if cfg.Compose.ForwardBackend == "ssh" {
			forwardSessions[fmt.Sprintf("%d", port)] = fmt.Sprintf("mutapod-%s-ssh-%d", cfg.Name, port)
		} else {
			forwardSessions[fmt.Sprintf("%d", port)] = syncMgr.ForwardSessionName(port)
		}
	}
	return forwardSessions
}

func buildReverseForwardSessionMap(syncMgr *mutagensync.Manager, ports []int) map[string]string {
	if len(ports) == 0 {
		return nil
	}

	forwardSessions := make(map[string]string, len(ports))
	for _, port := range ports {
		forwardSessions[fmt.Sprintf("%d", port)] = syncMgr.ReverseForwardSessionName(port)
	}
	return forwardSessions
}

func activeForwardBackend(cfg *config.Config, st *state.State) string {
	if st.Sync.ForwardBackend != "" {
		return st.Sync.ForwardBackend
	}
	for _, session := range st.Sync.ForwardSessions {
		if strings.Contains(session, "-ssh-") {
			return "ssh"
		}
	}
	if len(st.Sync.ForwardSessions) > 0 {
		return "mutagen"
	}
	return cfg.Compose.ForwardBackend
}

func ensureRemoteWorkspace(ctx context.Context, prov provider.Provider, workspacePath, user string) error {
	cmd := buildRemoteWorkspaceSetupCommand(workspacePath, user)
	return prov.Exec(ctx, []string{"bash", "-c", cmd}, provider.ExecOptions{})
}

func buildRemoteWorkspaceSetupCommand(workspacePath, user string) string {
	quotedWorkspace := shellQuote(workspacePath)
	return strings.Join([]string{
		fmt.Sprintf("sudo usermod -aG docker %s", shellQuote(user)),
		fmt.Sprintf("sudo mkdir -p %s", quotedWorkspace),
		fmt.Sprintf("sudo chown -R %s %s", shellQuote(user+":"+user), quotedWorkspace),
		fmt.Sprintf("sudo find %s -type d -exec chmod 0777 {} +", quotedWorkspace),
		fmt.Sprintf("sudo find %s -type f -exec chmod a+rw {} +", quotedWorkspace),
		fmt.Sprintf(
			"sudo find %s -type d -exec setfacl -m %s {} +",
			quotedWorkspace,
			shellQuote("d:u::rwx,d:g::rwx,d:m::rwx,d:o::rwx"),
		),
	}, " && ")
}

func ensureRemoteProfilePaths(ctx context.Context, prov provider.Provider, activeProfiles []profiles.Spec, user string) error {
	if len(activeProfiles) == 0 {
		return nil
	}
	return prov.Exec(ctx, []string{"bash", "-c", buildRemoteProfileSetupCommand(activeProfiles, user)}, provider.ExecOptions{})
}

func buildRemoteProfileSetupCommand(activeProfiles []profiles.Spec, user string) string {
	parts := []string{fmt.Sprintf("sudo usermod -aG docker %s", shellQuote(user))}
	for _, profile := range activeProfiles {
		for _, remotePath := range profile.RemoteDirectories() {
			quotedPath := shellQuote(remotePath)
			parts = append(parts,
				fmt.Sprintf("sudo mkdir -p %s", quotedPath),
				fmt.Sprintf("sudo chown -R %s %s", shellQuote(user+":"+user), quotedPath),
				fmt.Sprintf("sudo find %s -type d -exec chmod 0777 {} +", quotedPath),
				fmt.Sprintf("sudo find %s -type f -exec chmod a+rw {} +", quotedPath),
				fmt.Sprintf(
					"sudo find %s -type d -exec setfacl -m %s {} +",
					quotedPath,
					shellQuote("d:u::rwx,d:g::rwx,d:m::rwx,d:o::rwx"),
				),
			)
		}
	}
	return strings.Join(parts, " && ")
}

type composeProfileRunner struct {
	prov provider.Provider
}

func (r composeProfileRunner) RunProfileSetup(ctx context.Context, cfg *config.Config, active []profiles.Spec, spec profiles.Spec) error {
	return compose.ExecInPrimaryService(ctx, r.prov, cfg, active, spec.SetupScript())
}

func profileNames(activeProfiles []profiles.Spec) []string {
	names := make([]string, 0, len(activeProfiles))
	for _, profile := range activeProfiles {
		names = append(names, profile.Name)
	}
	return names
}

func profileStateKeys(activeProfiles []profiles.Spec) []string {
	keys := make([]string, 0, len(activeProfiles))
	for _, profile := range activeProfiles {
		keys = append(keys, profile.Name)
		for _, extra := range profile.SupplementalSyncs {
			keys = append(keys, extra.Name)
		}
	}
	return keys
}

func activeProfilesForLaunchMode(cfg *config.Config, launchMode vscode.LaunchMode) ([]profiles.Spec, error) {
	if !launchModeUsesProfiles(launchMode) {
		return nil, nil
	}
	return profiles.Active(cfg)
}

func launchModeUsesProfiles(launchMode vscode.LaunchMode) bool {
	return launchMode != vscode.LaunchHeadless
}

func launchModeUsesVSCode(launchMode vscode.LaunchMode) bool {
	return launchMode != vscode.LaunchHeadless
}

func terminateSavedProfileSyncs(ctx context.Context, mutagenPath string, cmd shell.Commander, profileStates []state.ProfileSyncState) {
	for _, profileState := range profileStates {
		if profileState.SessionName == "" {
			continue
		}
		if err := mutagensync.TerminateSyncSession(ctx, mutagenPath, cmd, profileState.SessionName); err != nil {
			shell.Debugf("terminate profile sync %s: %v", profileState.Name, err)
		}
	}
}

func shouldRefreshProfileSession(prior state.ProfileSyncState, found bool, signature string) bool {
	if !found {
		return true
	}
	return prior.SessionConfig == "" || prior.SessionConfig != signature
}

func effectiveProfileSyncMode(profileMode, workspaceMode string) string {
	if mode := strings.TrimSpace(profileMode); mode != "" {
		return mode
	}
	return workspaceMode
}

func shouldPrepareAttachedContainerExtensionInstall(cfg *config.Config) bool {
	return cfg.Compose.CopyLocalExtensionsEnabled() || len(cfg.Compose.Extensions) > 0
}

func prepareAttachedContainerExtensionInstall(ctx context.Context, prov provider.Provider, cfg *config.Config, activeProfiles []profiles.Spec) error {
	if cfg.Compose.PrimaryService == "" {
		return nil
	}
	return compose.ExecInPrimaryService(ctx, prov, cfg, activeProfiles, attachedContainerExtensionInstallPrepScript())
}

func attachedContainerExtensionInstallPrepScript() string {
	return `set -eu
needs_restart=0
for home in /root /home/*; do
  [ -d "$home/.vscode-server/data/Machine" ] || continue
  marker="$home/.vscode-server/data/Machine/.installExtensionsMarker"
  extensions_dir="$home/.vscode-server/extensions"
  if [ ! -f "$marker" ]; then
    continue
  fi
  has_extensions=0
  if [ -d "$extensions_dir" ] && find "$extensions_dir" -mindepth 1 -maxdepth 1 -type d | read _; then
    has_extensions=1
  fi
  if [ "$has_extensions" -eq 0 ]; then
    rm -f "$marker"
    needs_restart=1
  fi
done
if [ "$needs_restart" -eq 1 ]; then
  if command -v pkill >/dev/null 2>&1; then
    pkill -f '[/]\.vscode-server/bin/' 2>/dev/null || true
  else
    ps -eo pid=,args= 2>/dev/null | while read -r pid args; do
      case "$args" in
        *"/.vscode-server/bin/"*) kill "$pid" 2>/dev/null || true ;;
      esac
    done
  fi
fi`
}

func migrateRemoteCodexProfile(ctx context.Context, prov provider.Provider, profilePath, runtimePath string) error {
	profilePath = strings.TrimSpace(profilePath)
	runtimePath = strings.TrimSpace(runtimePath)
	if profilePath == "" || runtimePath == "" {
		return nil
	}
	return prov.Exec(ctx, []string{"bash", "-c", codexProfileMigrationCommand(profilePath, runtimePath)}, provider.ExecOptions{})
}

func codexProfileMigrationCommand(profilePath, runtimePath string) string {
	return fmt.Sprintf(`set -eu
profile=%s
runtime=%s
marker="$runtime/.portable-profile-v1"
if [ -e "$marker" ]; then
  exit 0
fi
sudo mkdir -p "$profile" "$runtime"
backup=''
for entry in "$profile"/* "$profile"/.[!.]* "$profile"/..?*; do
  if [ ! -e "$entry" ] && [ ! -L "$entry" ]; then
    continue
  fi
  name=${entry##*/}
  case "$name" in
    sessions|archived_sessions|attachments|generated_images|visualizations|memories|rules|skills|AGENTS.md|auth.json|config.toml)
      continue
      ;;
  esac
  if [ -z "$backup" ]; then
    backup="/var/lib/mutapod/profile-backups/codex-runtime/$(date -u +%%Y%%m%%dT%%H%%M%%SZ)-$$"
    sudo mkdir -p "$backup"
  fi
  sudo mv "$entry" "$backup"/
done
sudo touch "$marker"
`, shellQuote(profilePath), shellQuote(runtimePath))
}

func cleanupLegacyWorkspaceACLWatcher(ctx context.Context, prov provider.Provider, cfg *config.Config, activeProfiles []profiles.Spec) error {
	return compose.ExecInPrimaryService(ctx, prov, cfg, activeProfiles, legacyWorkspaceACLWatcherCleanupScript())
}

func legacyWorkspaceACLWatcherCleanupScript() string {
	return `set -eu
pid_file=/tmp/mutapod-acl-watch.pid
if [ -f "$pid_file" ]; then
  old_pid=$(cat "$pid_file" 2>/dev/null || true)
  case "$old_pid" in
    ''|*[!0-9]*) ;;
    *)
      if [ -r "/proc/$old_pid/cmdline" ] &&
         tr '\000' ' ' <"/proc/$old_pid/cmdline" | grep -Fq '/tmp/mutapod-acl-watch.sh'; then
        kill "$old_pid" 2>/dev/null || true
      fi
      ;;
  esac
fi
rm -f "$pid_file" /tmp/mutapod-acl-watch.sh /tmp/mutapod-acl-watch.log`
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func waitForInitialSync(ctx context.Context, prov provider.Provider, syncMgr *mutagensync.Manager, cfg *config.Config) error {
	if err := syncMgr.FlushSyncWithProgress(ctx, os.Stdout); err != nil {
		shell.Debugf("sync flush: %v", err)
	}
	if err := syncMgr.VerifySyncReady(ctx); err != nil {
		return err
	}

	remoteComposePath, err := compose.RemoteComposePath(cfg)
	if err != nil {
		shell.Debugf("remote compose path: %v", err)
		return nil
	}

	deadline := time.Now().Add(45 * time.Second)
	for {
		err := remotePathExists(ctx, prov, remoteComposePath)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("initial sync did not place %s on the remote host: %w", remoteComposePath, err)
		}
		shell.Debugf("waiting for remote file %s: %v", remoteComposePath, err)
		time.Sleep(2 * time.Second)
	}
}

func remotePathExists(ctx context.Context, prov provider.Provider, remotePath string) error {
	cmd := fmt.Sprintf("test -f %s", shellQuote(remotePath))
	return prov.Exec(ctx, []string{"bash", "-c", cmd}, provider.ExecOptions{})
}

func removeRemoteWorkspaceWrapper(ctx context.Context, prov provider.Provider, cfg *config.Config) error {
	remotePath := strings.TrimSuffix(cfg.WorkspacePath(), "/") + "/" + vscode.WorkspaceFilename()
	cmd := fmt.Sprintf("rm -f %s", shellQuote(remotePath))
	return prov.Exec(ctx, []string{"bash", "-c", cmd}, provider.ExecOptions{})
}

func loadConfig() (*config.Config, error) {
	opts := config.LoadOptions{ProviderOverride: providerOverride}
	if cfgFile != "" {
		return config.LoadFileWithOptions(cfgFile, opts)
	}
	cwd, err := currentDir()
	if err != nil {
		return nil, err
	}
	return config.LoadWithOptions(cwd, opts)
}

func ensureAgentsForStartup(in io.Reader, out io.Writer, interactive bool, cfg *config.Config) (string, bool, error) {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}

	status, err := agents.Inspect(cfg)
	if err != nil {
		return "", false, err
	}
	if status.Exists && status.HasManagedBlock {
		path, err := agents.Ensure(cfg)
		return path, true, err
	}

	if !interactive {
		if !status.Exists {
			fmt.Fprintf(out, "AGENTS.md was not found in %s; adding the mutapod-managed block.\n", cfg.Dir)
		} else {
			fmt.Fprintln(out, "AGENTS.md does not contain the mutapod-managed block; adding it at the top.")
		}
		path, err := agents.Ensure(cfg)
		return path, true, err
	}

	prompt := "AGENTS.md does not contain the mutapod-managed block. Add it at the top? [Y/n]: "
	if !status.Exists {
		prompt = "AGENTS.md was not found. Create it with the mutapod-managed block? [Y/n]: "
	}
	confirmed, err := confirmYesNoDefault(in, out, prompt, true)
	if err != nil {
		return "", false, err
	}
	if !confirmed {
		fmt.Fprintln(out, "Skipped the mutapod-managed AGENTS.md block.")
		return status.Path, false, nil
	}
	path, err := agents.Ensure(cfg)
	return path, true, err
}

func confirmMissingIgnoreFile(in io.Reader, out io.Writer, cfg *config.Config) error {
	path := filepath.Join(cfg.Dir, ignore.Filename)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check %s: %w", ignore.Filename, err)
	}

	fmt.Fprintf(out, "Warning: %s was not found in %s.\n", ignore.Filename, cfg.Dir)
	fmt.Fprintln(out, "mutapod will continue with only its built-in minimal ignores, which can cause large uploads.")
	fmt.Fprint(out, "Continue without .mutapodignore? [y/N]: ")

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("read confirmation: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return nil
	}
	return fmt.Errorf("up cancelled because %s is missing", ignore.Filename)
}

func parseUpLaunchMode(args []string) (vscode.LaunchMode, error) {
	if len(args) == 0 {
		return vscode.LaunchAttached, nil
	}

	switch args[0] {
	case string(vscode.LaunchLocal):
		return vscode.LaunchLocal, nil
	case string(vscode.LaunchAttached):
		return vscode.LaunchAttached, nil
	case string(vscode.LaunchHeadless):
		return vscode.LaunchHeadless, nil
	default:
		return "", fmt.Errorf("up: unsupported mode %q (expected: local, container, or headless)", args[0])
	}
}

func leaseOptionsForLaunchMode(mode vscode.LaunchMode) leaseOptions {
	if mode == vscode.LaunchHeadless {
		return leaseOptions{MinimumExpiry: headlessMinimumLease}
	}
	return leaseOptions{}
}
