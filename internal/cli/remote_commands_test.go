package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mutapod/mutapod/internal/config"
	"github.com/mutapod/mutapod/internal/provider"
	"github.com/mutapod/mutapod/internal/state"
	"github.com/mutapod/mutapod/internal/vscode"
)

type commandProvider struct {
	sshConfigCalls int
	execCmd        []string
	execOpts       provider.ExecOptions
}

func (p *commandProvider) Name() string { return "test" }

func (p *commandProvider) EnsureInstance(context.Context) (provider.InstanceState, error) {
	return provider.StateRunning, nil
}

func (p *commandProvider) State(context.Context) (provider.InstanceState, error) {
	return provider.StateRunning, nil
}

func (p *commandProvider) InstanceMetadata(context.Context) (provider.InstanceMetadata, error) {
	return provider.InstanceMetadata{}, nil
}

func (p *commandProvider) AdoptInstance(context.Context, string) error { return nil }

func (p *commandProvider) InstanceID(context.Context) (string, error) { return "test-instance", nil }

func (p *commandProvider) SSHConfig(context.Context) (*provider.SSHConfig, error) {
	p.sshConfigCalls++
	return &provider.SSHConfig{Host: "test-host"}, nil
}

func (p *commandProvider) Exec(_ context.Context, cmd []string, opts provider.ExecOptions) error {
	p.execCmd = append([]string(nil), cmd...)
	p.execOpts = opts
	return nil
}

func (p *commandProvider) CopyFile(context.Context, string, string) error { return nil }

func (p *commandProvider) PreferredSyncBackend() provider.SyncBackend {
	return provider.SyncMutagen
}

func (p *commandProvider) StopInstance(context.Context) error { return nil }

func (p *commandProvider) DeleteInstance(context.Context) error { return nil }

func (p *commandProvider) ForwardedWorkspacePath() string { return "/workspace/demo" }

func TestRunSSHWithoutCommandUsesInteractiveVMExec(t *testing.T) {
	prov := &commandProvider{}

	if err := runSSH(context.Background(), prov, nil); err != nil {
		t.Fatalf("runSSH: %v", err)
	}

	if prov.sshConfigCalls != 1 {
		t.Fatalf("SSHConfig calls: got %d, want 1", prov.sshConfigCalls)
	}
	if len(prov.execCmd) != 0 {
		t.Fatalf("command: got %#v, want empty interactive shell command", prov.execCmd)
	}
	if !prov.execOpts.Tty {
		t.Fatal("expected interactive shell to request TTY")
	}
	if prov.execOpts.Stdin == nil || prov.execOpts.Stdout == nil || prov.execOpts.Stderr == nil {
		t.Fatal("expected standard streams to be attached")
	}
}

func TestRunSSHWithCommandUsesNonInteractiveVMExec(t *testing.T) {
	prov := &commandProvider{}

	if err := runSSH(context.Background(), prov, []string{"uname", "-a"}); err != nil {
		t.Fatalf("runSSH: %v", err)
	}

	if prov.sshConfigCalls != 1 {
		t.Fatalf("SSHConfig calls: got %d, want 1", prov.sshConfigCalls)
	}
	if got := strings.Join(prov.execCmd, "\x00"); got != "uname\x00-a" {
		t.Fatalf("command: got %#v", prov.execCmd)
	}
	if prov.execOpts.Tty {
		t.Fatal("expected non-interactive command not to request TTY")
	}
}

func TestCommandScriptQuotesArguments(t *testing.T) {
	got := commandScript([]string{"python", "manage.py", "shell", "print('ok')"})
	want := "exec 'python' 'manage.py' 'shell' 'print('\\''ok'\\'')'"
	if got != want {
		t.Fatalf("commandScript:\ngot  %q\nwant %q", got, want)
	}
}

func TestRunExecUsesPrimaryServiceContainer(t *testing.T) {
	setTempStateHome(t)
	prov := &commandProvider{}
	disabled := false
	cfg := &config.Config{
		Name: "demo",
		Dir:  t.TempDir(),
		Compose: config.ComposeConfig{
			PrimaryService: "web",
		},
		Profiles: config.ProfilesConfig{
			Codex:  config.ProfileSyncConfig{Enabled: &disabled},
			Claude: config.ProfileSyncConfig{Enabled: &disabled},
		},
	}

	if err := runExec(context.Background(), cfg, prov, []string{"python", "manage.py", "check"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}

	if prov.sshConfigCalls != 1 {
		t.Fatalf("SSHConfig calls: got %d, want 1", prov.sshConfigCalls)
	}
	if got := strings.Join(prov.execCmd, "\x00"); !strings.HasPrefix(got, "bash\x00-c\x00") {
		t.Fatalf("expected bash -c remote command, got %#v", prov.execCmd)
	}
	if prov.execOpts.Tty {
		t.Fatal("expected container exec command not to request TTY")
	}
	if prov.execOpts.Stdin == nil || prov.execOpts.Stdout == nil || prov.execOpts.Stderr == nil {
		t.Fatal("expected container exec command to attach standard streams")
	}
	remote := prov.execCmd[2]
	for _, expected := range []string{
		"cd '/workspace/demo'",
		"sudo docker compose exec -T --user root 'web' sh -c",
		"exec '\\''python'\\'' '\\''manage.py'\\'' '\\''check'\\''",
	} {
		if !strings.Contains(remote, expected) {
			t.Fatalf("remote command missing %q:\n%s", expected, remote)
		}
	}
}

func TestRunExecAfterHeadlessLaunchSkipsProfileDetectionAndMounts(t *testing.T) {
	setTempStateHome(t)
	badProfilePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badProfilePath, []byte("test"), 0600); err != nil {
		t.Fatalf("write profile path: %v", err)
	}
	enabled := true
	cfg := &config.Config{
		Name: "headless-exec",
		Dir:  t.TempDir(),
		Compose: config.ComposeConfig{
			PrimaryService: "web",
		},
		Profiles: config.ProfilesConfig{
			Codex: config.ProfileSyncConfig{
				Enabled:   &enabled,
				LocalPath: badProfilePath,
			},
		},
	}
	if err := state.Save(&state.State{
		SchemaVersion: state.SchemaVersion,
		Name:          cfg.Name,
		LaunchMode:    string(vscode.LaunchHeadless),
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	prov := &commandProvider{}

	if err := runExec(context.Background(), cfg, prov, []string{"python", "manage.py", "check"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	remote := prov.execCmd[2]
	if strings.Contains(remote, ".mutapod.compose.override.yaml") {
		t.Fatalf("headless exec unexpectedly selected a profile override:\n%s", remote)
	}
}

func setTempStateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}
