package cli

import (
	"strings"
	"testing"
)

func TestBuildWorkspaceACLScript(t *testing.T) {
	script := buildWorkspaceACLScript("/app")

	for _, needle := range []string{
		"workspace='/app'",
		"uid=$(stat -c %u \"$workspace\")",
		"DEBIAN_FRONTEND=noninteractive dpkg --configure -a >/dev/null",
		"if command -v apt-get >/dev/null 2>&1; then",
		"repair_debian_packages",
		"apt-get install -y -qq acl >/dev/null",
		"apply_workspace_acls() {",
		"find \"$workspace\" -uid 0 -exec setfacl -m \"u:${uid}:rwX\" {} + 2>/dev/null || true",
		"find \"$workspace\" -uid 0 -type d -exec setfacl -m \"d:u:${uid}:rwX\" {} + 2>/dev/null || true",
		"cat > /tmp/mutapod-acl-watch.sh <<EOF",
		"nohup /tmp/mutapod-acl-watch.sh >/tmp/mutapod-acl-watch.log 2>&1 &",
		"echo $! >/tmp/mutapod-acl-watch.pid",
	} {
		if !strings.Contains(script, needle) {
			t.Fatalf("script missing %q:\n%s", needle, script)
		}
	}
}
