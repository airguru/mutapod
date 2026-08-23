package profiles

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mutapod/mutapod/internal/config"
)

func TestClaudeProfileIncludesCompanionFileAndWrapperEnv(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{\"ok\":true}"), 0644); err != nil {
		t.Fatalf("write .claude.json: %v", err)
	}

	oldHome := userHomeDir
	oldLookPath := lookPath
	t.Cleanup(func() {
		userHomeDir = oldHome
		lookPath = oldLookPath
	})
	userHomeDir = func() (string, error) { return home, nil }
	lookPath = func(name string) (string, error) {
		return filepath.Join(tmp, name), nil
	}

	cfg := &config.Config{
		Name: "demo",
		Dir:  tmp,
		Profiles: config.ProfilesConfig{
			Claude: config.ProfileSyncConfig{},
		},
	}

	active, err := Active(cfg)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("Active: got %d profiles, want 2", len(active))
	}

	var claude Spec
	found := false
	for _, spec := range active {
		if spec.Name == "claude" {
			claude = spec
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("claude profile not detected")
	}
	if got := len(claude.Mounts); got != 3 {
		t.Fatalf("Mounts: got %d, want 3", got)
	}
	if got := len(claude.SupplementalSyncs); got != 1 {
		t.Fatalf("SupplementalSyncs: got %d, want 1", got)
	}
	if claude.Mounts[2].ContainerPath != RootHomeDir+"/.claude.json" {
		t.Fatalf("expected supplemental .claude.json mount, got %#v", claude.Mounts[2])
	}

	script := claude.SetupScript()
	if !strings.Contains(script, "export CLAUDE_CONFIG_DIR='"+RootHomeDir+"/.claude'") {
		t.Fatalf("SetupScript missing CLAUDE_CONFIG_DIR export:\n%s", script)
	}
}

func TestCodexProfileSyncsOnlyPortableDataAndSeparatesRuntimeState(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0755); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}

	oldHome := userHomeDir
	oldLookPath := lookPath
	t.Cleanup(func() {
		userHomeDir = oldHome
		lookPath = oldLookPath
	})
	userHomeDir = func() (string, error) { return home, nil }
	lookPath = func(name string) (string, error) {
		return filepath.Join(tmp, name), nil
	}

	cfg := &config.Config{Name: "demo", Dir: tmp}
	active, err := Active(cfg)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}

	var codex Spec
	found := false
	for _, spec := range active {
		if spec.Name == "codex" {
			codex = spec
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("codex profile not detected")
	}
	if !codex.NeedsSandboxNamespaces {
		t.Fatalf("codex profile should request sandbox namespace support")
	}
	if codex.SyncMode != "two-way-safe" {
		t.Fatalf("SyncMode: got %q, want two-way-safe", codex.SyncMode)
	}
	if codex.RuntimeRemotePath != "/var/lib/mutapod/runtime/codex-sqlite" {
		t.Fatalf("RuntimeRemotePath: got %q", codex.RuntimeRemotePath)
	}
	if got := len(codex.Mounts); got != 3 {
		t.Fatalf("Mounts: got %d, want 3", got)
	}
	if codex.Mounts[2].ContainerPath != "/var/lib/mutapod/runtime/codex-sqlite" {
		t.Fatalf("runtime mount: got %#v", codex.Mounts[2])
	}

	patterns := make(map[string]bool, len(codex.IgnorePatterns))
	for _, pattern := range codex.IgnorePatterns {
		patterns[pattern] = true
	}
	for _, expected := range []string{
		"/*",
		"!/sessions/",
		"!/archived_sessions/",
		"!/attachments/",
		"!/generated_images/",
		"!/visualizations/",
		"!/memories/",
		"!/rules/",
		"!/skills/",
		"!/AGENTS.md",
		"!/auth.json",
		"!/config.toml",
	} {
		if !patterns[expected] {
			t.Fatalf("IgnorePatterns missing %q", expected)
		}
	}
	script := codex.SetupScript()
	if !strings.Contains(script, "export CODEX_SQLITE_HOME='/var/lib/mutapod/runtime/codex-sqlite'") {
		t.Fatalf("SetupScript missing CODEX_SQLITE_HOME export:\n%s", script)
	}
	env := codex.AttachedContainerRemoteEnv()
	if env["CODEX_SQLITE_HOME"] != "/var/lib/mutapod/runtime/codex-sqlite" {
		t.Fatalf("CODEX_SQLITE_HOME: got %q", env["CODEX_SQLITE_HOME"])
	}
}

func TestCodexProfileFallsBackToVSCodeExtensionBinary(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0755); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}
	extensionBin := filepath.Join(home, ".vscode", "extensions", "openai.chatgpt-1.2.3-win32-x64", "bin", "windows-x86_64")
	if err := os.MkdirAll(extensionBin, 0755); err != nil {
		t.Fatalf("mkdir extension bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extensionBin, "codex.exe"), []byte(""), 0644); err != nil {
		t.Fatalf("write codex.exe: %v", err)
	}

	oldHome := userHomeDir
	oldLookPath := lookPath
	t.Cleanup(func() {
		userHomeDir = oldHome
		lookPath = oldLookPath
	})
	userHomeDir = func() (string, error) { return home, nil }
	lookPath = func(name string) (string, error) {
		return "", exec.ErrNotFound
	}

	cfg := &config.Config{Name: "demo", Dir: tmp}
	active, err := Active(cfg)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}

	var codex Spec
	found := false
	for _, spec := range active {
		if spec.Name == "codex" {
			codex = spec
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("codex profile not detected")
	}
	if !strings.HasSuffix(strings.ToLower(codex.LocalBinaryPath), filepath.Join("bin", "windows-x86_64", "codex.exe")) {
		t.Fatalf("LocalBinaryPath: got %q", codex.LocalBinaryPath)
	}
}

func TestNodeProfileSetupScriptEmitsHeartbeatWhileInstalling(t *testing.T) {
	script := nodeProfileSetupScript(nodeProfileSetup{
		PackageName: "@example/tool",
		ToolPrefix:  "/var/lib/mutapod/tools/example",
		BinaryName:  "example",
		WrapperName: "example",
		WrapperBody: "exec /var/lib/mutapod/tools/example/bin/example \"$@\"",
	})

	for _, expected := range []string{
		"start_mutapod_profile_heartbeat",
		"profile setup still running for $package_name",
		"trap stop_mutapod_profile_heartbeat EXIT INT TERM",
		"DEBIAN_FRONTEND=noninteractive dpkg --configure -a >/dev/null || true",
		"DEBIAN_FRONTEND=noninteractive apt-get install -f -y -qq >/dev/null",
		"repair_debian_packages",
		"mutapod: installing $package_name in $tool_prefix",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("SetupScript missing %q:\n%s", expected, script)
		}
	}
}

func TestEnsureRemoteToolsRetriesMissingRemoteExitStatus(t *testing.T) {
	oldRetryDelay := retryDelay
	t.Cleanup(func() { retryDelay = oldRetryDelay })
	retryDelay = time.Nanosecond

	runner := &profileSetupRunner{
		errs: []error{
			errors.New("wait: remote command exited without exit status or exit signal"),
			nil,
		},
	}
	cfg := &config.Config{Name: "demo"}
	active := []Spec{{Name: "codex"}}

	if err := EnsureRemoteTools(context.Background(), runner, cfg, active); err != nil {
		t.Fatalf("EnsureRemoteTools: %v", err)
	}
	if runner.calls != 2 {
		t.Fatalf("RunProfileSetup calls: got %d, want 2", runner.calls)
	}
}

type profileSetupRunner struct {
	calls int
	errs  []error
}

func (r *profileSetupRunner) RunProfileSetup(context.Context, *config.Config, []Spec, Spec) error {
	err := r.errs[r.calls]
	r.calls++
	return err
}
