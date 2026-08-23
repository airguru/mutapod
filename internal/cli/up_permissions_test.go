package cli

import (
	"strings"
	"testing"

	"github.com/mutapod/mutapod/internal/profiles"
)

func TestBuildRemoteWorkspaceSetupCommandUsesOpenInheritedPermissions(t *testing.T) {
	command := buildRemoteWorkspaceSetupCommand("/workspace/my app", "alice")

	for _, needle := range []string{
		"sudo usermod -aG docker 'alice'",
		"sudo mkdir -p '/workspace/my app'",
		"sudo chown -R 'alice:alice' '/workspace/my app'",
		"sudo find '/workspace/my app' -type d -exec chmod 0777 {} +",
		"sudo find '/workspace/my app' -type f -exec chmod a+rw {} +",
		"sudo find '/workspace/my app' -type d -exec setfacl -m 'd:u::rwx,d:g::rwx,d:m::rwx,d:o::rwx' {} +",
	} {
		if !strings.Contains(command, needle) {
			t.Fatalf("workspace setup command missing %q:\n%s", needle, command)
		}
	}
	if strings.Contains(command, "-type f -exec chmod 0777") {
		t.Fatalf("workspace setup must not make every regular file executable:\n%s", command)
	}
}

func TestBuildRemoteProfileSetupCommandUsesOpenInheritedPermissions(t *testing.T) {
	command := buildRemoteProfileSetupCommand([]profiles.Spec{
		{
			Name:              "codex",
			SyncRemotePath:    "/var/lib/mutapod/profiles/codex",
			ToolRemotePath:    "/var/lib/mutapod/tools/codex",
			RuntimeRemotePath: "/var/lib/mutapod/runtime/codex-sqlite",
		},
	}, "azureuser")

	for _, path := range []string{
		"/var/lib/mutapod/profiles/codex",
		"/var/lib/mutapod/tools/codex",
		"/var/lib/mutapod/runtime/codex-sqlite",
	} {
		for _, template := range []string{
			"sudo mkdir -p '%s'",
			"sudo chown -R 'azureuser:azureuser' '%s'",
			"sudo find '%s' -type d -exec chmod 0777 {} +",
			"sudo find '%s' -type f -exec chmod a+rw {} +",
			"sudo find '%s' -type d -exec setfacl -m 'd:u::rwx,d:g::rwx,d:m::rwx,d:o::rwx' {} +",
		} {
			needle := strings.Replace(template, "%s", path, 1)
			if !strings.Contains(command, needle) {
				t.Fatalf("profile setup command missing %q:\n%s", needle, command)
			}
		}
	}
}

func TestLegacyWorkspaceACLWatcherCleanupStopsAndRemovesWatcher(t *testing.T) {
	script := legacyWorkspaceACLWatcherCleanupScript()

	for _, needle := range []string{
		"pid_file=/tmp/mutapod-acl-watch.pid",
		"grep -Fq '/tmp/mutapod-acl-watch.sh'",
		"kill \"$old_pid\"",
		"rm -f \"$pid_file\" /tmp/mutapod-acl-watch.sh /tmp/mutapod-acl-watch.log",
	} {
		if !strings.Contains(script, needle) {
			t.Fatalf("cleanup script missing %q:\n%s", needle, script)
		}
	}
	for _, forbidden := range []string{"nohup", "sleep 2", "apply_workspace_acls"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("cleanup script must not contain %q:\n%s", forbidden, script)
		}
	}
}
