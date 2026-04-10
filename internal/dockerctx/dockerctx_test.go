package dockerctx

import (
	"context"
	"errors"
	"testing"

	"github.com/mutapod/mutapod/internal/config"
	"github.com/mutapod/mutapod/internal/provider"
	"github.com/mutapod/mutapod/internal/shell"
)

func testConfig() *config.Config {
	return &config.Config{
		Name:          "testproject",
		InstanceOwner: "tester",
	}
}

func testSSHConfig() *provider.SSHConfig {
	return &provider.SSHConfig{
		Host: "mutapod-tester-testproject.example",
		User: "pavel",
	}
}

func TestEnsureContext_CreatesContextWhenMissing(t *testing.T) {
	fake := shell.NewFakeCommander()
	fake.StubErr(errors.New("context not found"), "docker", "context", "inspect", "mutapod-tester-testproject")

	name, err := EnsureContext(context.Background(), testConfig(), testSSHConfig(), fake)
	if err != nil {
		t.Fatalf("EnsureContext: %v", err)
	}
	if name != "mutapod-tester-testproject" {
		t.Fatalf("context name: got %q", name)
	}
	if !fake.CalledWith("docker", "context", "create",
		"mutapod-tester-testproject",
		"--description", "mutapod workspace testproject",
		"--docker", "host=ssh://pavel@mutapod-tester-testproject.example",
	) {
		t.Fatalf("expected docker context create call, got %#v", fake.Calls)
	}
}

func TestEnsureContext_UpdatesExistingContext(t *testing.T) {
	fake := shell.NewFakeCommander()
	fake.Stub("[]", "docker", "context", "inspect", "mutapod-tester-testproject")

	name, err := EnsureContext(context.Background(), testConfig(), testSSHConfig(), fake)
	if err != nil {
		t.Fatalf("EnsureContext: %v", err)
	}
	if name != "mutapod-tester-testproject" {
		t.Fatalf("context name: got %q", name)
	}
	if !fake.CalledWith("docker", "context", "update",
		"mutapod-tester-testproject",
		"--description", "mutapod workspace testproject",
		"--docker", "host=ssh://pavel@mutapod-tester-testproject.example",
	) {
		t.Fatalf("expected docker context update call, got %#v", fake.Calls)
	}
}
