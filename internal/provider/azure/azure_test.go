package azure

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mutapod/mutapod/internal/config"
	"github.com/mutapod/mutapod/internal/provider"
	"github.com/mutapod/mutapod/internal/shell"
	"github.com/mutapod/mutapod/internal/sshkey"
	"github.com/mutapod/mutapod/internal/sshrun"
)

func testConfig() *config.Config {
	return &config.Config{
		Name:          "myapp",
		InstanceOwner: "tester",
		Provider: config.ProviderConfig{
			Type: "azure",
			Azure: config.AzureConfig{
				Subscription:  "sub-123",
				ResourceGroup: "rg-dev",
				Location:      "westeurope",
				VMSize:        "Standard_D4s_v5",
				DiskSizeGB:    64,
				StorageSKU:    "StandardSSD_LRS",
				Image:         "Ubuntu2204",
				VNet:          "dev-vnet",
				Subnet:        "dev-subnet",
				AdminUsername: "azureuser",
				PublicIPSku:   "Standard",
				Tags:          map[string]string{"managed-by": "mutapod"},
			},
		},
	}
}

func TestState_Running(t *testing.T) {
	f := shell.NewFakeCommander()
	instanceName := testConfig().InstanceName()
	f.Stub("VM running\n", "az",
		"vm", "show",
		"--resource-group", "rg-dev",
		"--name", instanceName,
		"--show-details",
		"--query", "powerState",
		"--output", "tsv",
		"--subscription", "sub-123",
	)

	p := New(testConfig(), f)
	state, err := p.State(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != provider.StateRunning {
		t.Errorf("state: got %q, want %q", state, provider.StateRunning)
	}
}

func TestState_NotFound(t *testing.T) {
	f := shell.NewFakeCommander()
	instanceName := testConfig().InstanceName()
	f.StubErr(errors.New("(ResourceNotFound) The Resource was not found"), "az",
		"vm", "show",
		"--resource-group", "rg-dev",
		"--name", instanceName,
		"--show-details",
		"--query", "powerState",
		"--output", "tsv",
		"--subscription", "sub-123",
	)

	p := New(testConfig(), f)
	state, err := p.State(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != provider.StateNotFound {
		t.Errorf("state: got %q, want %q", state, provider.StateNotFound)
	}
}

func TestEnsureInstance_CreateNew(t *testing.T) {
	tempHome(t)

	f := shell.NewFakeCommander()
	cfg := testConfig()
	instanceName := cfg.InstanceName()
	fingerprint, err := cfg.VMConfigFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	f.StubErr(errors.New("not found"), "az",
		"vm", "show",
		"--resource-group", "rg-dev",
		"--name", instanceName,
		"--show-details",
		"--query", "powerState",
		"--output", "tsv",
		"--subscription", "sub-123",
	)

	p := New(cfg, f)
	identity, err := p.sshIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = p.EnsureInstance(ctx)

	if !f.CalledWith("az", "vm", "create",
		"--resource-group", "rg-dev",
		"--name", instanceName,
		"--size", "Standard_D4s_v5",
		"--image", "Ubuntu2204",
		"--admin-username", "azureuser",
		"--authentication-type", "ssh",
		"--os-disk-size-gb", "64",
		"--os-disk-delete-option", "Delete",
		"--nic-delete-option", "Delete",
		"--storage-sku", "StandardSSD_LRS",
		"--public-ip-address", "",
		"--nsg-rule", "NONE",
		"--location", "westeurope",
		"--ssh-key-values", "@"+identity.PublicPath,
		"--vnet-name", "dev-vnet",
		"--subnet", "dev-subnet",
		"--tags", "managed-by=mutapod", "mutapod-config="+fingerprint,
		"--subscription", "sub-123",
		"--output", "json",
	) {
		t.Error("expected az vm create to be called with correct args")
		for _, c := range f.Calls {
			t.Logf("  call: %s %v", c.Name, c.Args)
		}
	}
}

func TestEnsureInstance_CreateNewWithPublicIP(t *testing.T) {
	tempHome(t)

	cfg := testConfig()
	cfg.Provider.Azure.PublicIP = true
	fingerprint, err := cfg.VMConfigFingerprint()
	if err != nil {
		t.Fatal(err)
	}

	f := shell.NewFakeCommander()
	instanceName := cfg.InstanceName()
	f.StubErr(errors.New("not found"), "az",
		"vm", "show",
		"--resource-group", "rg-dev",
		"--name", instanceName,
		"--show-details",
		"--query", "powerState",
		"--output", "tsv",
		"--subscription", "sub-123",
	)

	p := New(cfg, f)
	identity, err := p.sshIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = p.EnsureInstance(ctx)

	if !f.CalledWith("az", "vm", "create",
		"--resource-group", "rg-dev",
		"--name", instanceName,
		"--size", "Standard_D4s_v5",
		"--image", "Ubuntu2204",
		"--admin-username", "azureuser",
		"--authentication-type", "ssh",
		"--os-disk-size-gb", "64",
		"--os-disk-delete-option", "Delete",
		"--nic-delete-option", "Delete",
		"--storage-sku", "StandardSSD_LRS",
		"--public-ip-sku", "Standard",
		"--nsg-rule", "SSH",
		"--location", "westeurope",
		"--ssh-key-values", "@"+identity.PublicPath,
		"--vnet-name", "dev-vnet",
		"--subnet", "dev-subnet",
		"--tags", "managed-by=mutapod", "mutapod-config="+fingerprint,
		"--subscription", "sub-123",
		"--output", "json",
	) {
		t.Error("expected az vm create to be called with public IP args")
		for _, c := range f.Calls {
			t.Logf("  call: %s %v", c.Name, c.Args)
		}
	}
}

func TestEnsureInstanceRestartsVMThatWasStopping(t *testing.T) {
	cfg := testConfig()
	powerStateCalls := 0
	cmd := &callbackCommander{}
	cmd.output = func(_ context.Context, _ shell.RunOptions, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "az" && strings.Contains(joined, "--query powerState"):
			powerStateCalls++
			switch powerStateCalls {
			case 1:
				return []byte("VM stopping\n"), nil
			case 2:
				return []byte("VM stopped\n"), nil
			default:
				return []byte("VM running\n"), nil
			}
		case name == "az" && strings.Contains(joined, "--scripts "+azureStartupGuardScript):
			return []byte("mutapod:startup-guard-active:restart\n"), nil
		default:
			return nil, nil
		}
	}
	p := New(cfg, cmd)

	state, err := p.EnsureInstance(context.Background())
	if err != nil {
		t.Fatalf("EnsureInstance: %v", err)
	}
	if state != provider.StateRunning {
		t.Fatalf("state: got %q, want %q", state, provider.StateRunning)
	}
	if !cmd.calledWith("az", "vm", "start") {
		t.Fatalf("expected stopping VM to be restarted, calls: %#v", cmd.calls)
	}
}

func TestSSHConfig(t *testing.T) {
	stubSSHFunctions(t, func(context.Context, *sshrun.Client) error { return nil })

	home := tempHome(t)
	f := shell.NewFakeCommander()
	instanceName := testConfig().InstanceName()
	f.Stub("10.1.2.3\n", "az",
		"vm", "show",
		"--resource-group", "rg-dev",
		"--name", instanceName,
		"--show-details",
		"--query", "privateIps",
		"--output", "tsv",
		"--subscription", "sub-123",
	)

	p := New(testConfig(), f)
	identity, err := p.sshIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sshCfg, err := p.SSHConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := instanceName + ".azure"; sshCfg.Host != want {
		t.Errorf("Host: got %q, want %q", sshCfg.Host, want)
	}
	if sshCfg.IP != "10.1.2.3" {
		t.Errorf("IP: got %q, want %q", sshCfg.IP, "10.1.2.3")
	}
	if sshCfg.User != "azureuser" {
		t.Errorf("User: got %q, want azureuser", sshCfg.User)
	}
	if sshCfg.IdentityFile != identity.PrivatePath {
		t.Errorf("IdentityFile: got %q", sshCfg.IdentityFile)
	}

	configData, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		t.Fatalf("read ssh config: %v", err)
	}
	configText := string(configData)
	for _, want := range []string{
		"Host " + instanceName + ".azure",
		"HostName 10.1.2.3",
		"User azureuser",
		"IdentityFile " + filepath.ToSlash(identity.PrivatePath),
		"UserKnownHostsFile " + filepath.ToSlash(filepath.Join(home, ".ssh", "known_hosts")),
		"HostKeyAlias " + instanceName + ".azure",
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("ssh config missing %q:\n%s", want, configText)
		}
	}
}

func TestSSHConfigPreferPrivateIP(t *testing.T) {
	stubSSHFunctions(t, func(context.Context, *sshrun.Client) error { return nil })

	tempHome(t)
	cfg := testConfig()
	cfg.Provider.Azure.PreferPrivateIP = true

	f := shell.NewFakeCommander()
	instanceName := cfg.InstanceName()
	f.Stub("10.1.2.3\n", "az",
		"vm", "show",
		"--resource-group", "rg-dev",
		"--name", instanceName,
		"--show-details",
		"--query", "privateIps",
		"--output", "tsv",
		"--subscription", "sub-123",
	)

	p := New(cfg, f)
	sshCfg, err := p.SSHConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sshCfg.IP != "10.1.2.3" {
		t.Fatalf("IP: got %q, want 10.1.2.3", sshCfg.IP)
	}
}

func TestSSHConfigRepairsRejectedManagedKey(t *testing.T) {
	tempHome(t)
	attempts := 0
	stubSSHFunctions(t, func(context.Context, *sshrun.Client) error {
		attempts++
		if attempts == 1 {
			return errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain")
		}
		return nil
	})

	cfg := testConfig()
	f := shell.NewFakeCommander()
	instanceName := cfg.InstanceName()
	f.Stub("10.1.2.3\n", "az",
		"vm", "show",
		"--resource-group", "rg-dev",
		"--name", instanceName,
		"--show-details",
		"--query", "privateIps",
		"--output", "tsv",
		"--subscription", "sub-123",
	)

	p := New(cfg, f)
	identity, err := p.sshIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	keySum := sha256.Sum256([]byte(identity.PublicKey))
	confirmation := "mutapod:ssh-key-installed:" + identity.Marker + ":" + hex.EncodeToString(keySum[:]) + ":1\n"
	f.Stub(confirmation, "az",
		"vm", "run-command", "invoke",
		"--resource-group", "rg-dev",
		"--name", instanceName,
		"--command-id", "RunShellScript",
		"--scripts", azureAuthorizedKeysScript,
		"--parameters", "azureuser", base64.RawStdEncoding.EncodeToString([]byte(identity.PublicKey)), identity.Marker,
		"--query", "value[0].message",
		"--output", "tsv",
		"--subscription", "sub-123",
	)
	if _, err := p.SSHConfig(context.Background()); err != nil {
		t.Fatalf("SSHConfig: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("SSH probe attempts: got %d, want 2", attempts)
	}

	wantKey := base64.RawStdEncoding.EncodeToString([]byte(identity.PublicKey))
	if strings.Contains(wantKey, "=") {
		t.Fatalf("Azure Run Command transport must not contain '=': %q", wantKey)
	}
	if !f.CalledWith("az",
		"vm", "run-command", "invoke",
		"--resource-group", "rg-dev",
		"--name", instanceName,
		"--command-id", "RunShellScript",
		"--scripts", azureAuthorizedKeysScript,
		"--parameters", "azureuser", wantKey, identity.Marker,
		"--query", "value[0].message",
		"--output", "tsv",
		"--subscription", "sub-123",
	) {
		t.Fatalf("expected Azure Run Command key repair, got %#v", f.Calls)
	}
}

func TestSSHConfigResetsDeadlineAfterSlowKeyRepair(t *testing.T) {
	tempHome(t)
	oldTimeout := sshReadyTimeout
	oldRetryPeriod := sshReadyRetryPeriod
	sshReadyTimeout = 25 * time.Millisecond
	sshReadyRetryPeriod = time.Millisecond
	t.Cleanup(func() {
		sshReadyTimeout = oldTimeout
		sshReadyRetryPeriod = oldRetryPeriod
	})

	attempts := 0
	stubSSHFunctions(t, func(context.Context, *sshrun.Client) error {
		attempts++
		switch attempts {
		case 1:
			return errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain")
		case 2:
			return errors.New("dial tcp 10.1.2.3:22: i/o timeout")
		default:
			return nil
		}
	})

	cfg := testConfig()
	p := New(cfg, nil)
	identity, err := p.sshIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	keySum := sha256.Sum256([]byte(identity.PublicKey))
	confirmation := "mutapod:ssh-key-installed:" + identity.Marker + ":" + hex.EncodeToString(keySum[:]) + ":1\n"
	p.cmd = &callbackCommander{output: func(_ context.Context, _ shell.RunOptions, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "az" && strings.Contains(joined, "--query privateIps"):
			return []byte("10.1.2.3\n"), nil
		case name == "az" && strings.Contains(joined, "--scripts "+azureAuthorizedKeysScript):
			time.Sleep(35 * time.Millisecond)
			return []byte(confirmation), nil
		case name == "az" && strings.Contains(joined, "--query powerState"):
			return []byte("VM running\n"), nil
		default:
			return nil, nil
		}
	}}

	if _, err := p.SSHConfig(context.Background()); err != nil {
		t.Fatalf("SSHConfig after slow repair: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("SSH probe attempts: got %d, want 3", attempts)
	}
}

func TestSSHConfigRestartsVMStoppedDuringKeyVerification(t *testing.T) {
	tempHome(t)
	attempts := 0
	stubSSHFunctions(t, func(context.Context, *sshrun.Client) error {
		attempts++
		switch attempts {
		case 1:
			return errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain")
		case 2:
			return errors.New("dial tcp 10.1.2.3:22: i/o timeout")
		default:
			return nil
		}
	})

	cfg := testConfig()
	p := New(cfg, nil)
	identity, err := p.sshIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	keySum := sha256.Sum256([]byte(identity.PublicKey))
	confirmation := "mutapod:ssh-key-installed:" + identity.Marker + ":" + hex.EncodeToString(keySum[:]) + ":1\n"
	powerStateCalls := 0
	cmd := &callbackCommander{}
	cmd.output = func(_ context.Context, _ shell.RunOptions, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "az" && strings.Contains(joined, "--query privateIps"):
			return []byte("10.1.2.3\n"), nil
		case name == "az" && strings.Contains(joined, "--scripts "+azureAuthorizedKeysScript):
			return []byte(confirmation), nil
		case name == "az" && strings.Contains(joined, "--query powerState"):
			powerStateCalls++
			if powerStateCalls <= 2 {
				return []byte("VM stopped\n"), nil
			}
			return []byte("VM running\n"), nil
		case name == "az" && strings.Contains(joined, "--scripts "+azureStartupGuardScript):
			return []byte("mutapod:startup-guard-active:restart\n"), nil
		default:
			return nil, nil
		}
	}
	p.cmd = cmd

	if _, err := p.SSHConfig(context.Background()); err != nil {
		t.Fatalf("SSHConfig after stopped VM recovery: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("SSH probe attempts: got %d, want 3", attempts)
	}
	if !cmd.calledWith("az", "vm", "start") {
		t.Fatalf("expected stopped VM to be restarted, calls: %#v", cmd.calls)
	}
}

func TestRepairSSHAccessRequiresGuestConfirmation(t *testing.T) {
	p := New(testConfig(), shell.NewFakeCommander())
	err := p.repairSSHAccess(context.Background(), &sshIdentity{
		PublicKey: "ssh-ed25519 AAAAcurrent",
		Marker:    "mutapod-test",
	})
	if err == nil || !strings.Contains(err.Error(), "did not confirm") {
		t.Fatalf("expected missing confirmation error, got %v", err)
	}
}

func TestEnsureStartupGuardUsesIdleMode(t *testing.T) {
	tests := []struct {
		name    string
		enabled *bool
		mode    string
	}{
		{name: "default enabled", mode: "restart"},
		{name: "disabled", enabled: boolPtr(false), mode: "disable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Idle.Enabled = tt.enabled
			f := shell.NewFakeCommander()
			f.Stub("mutapod:startup-guard-active:"+tt.mode+"\n", "az",
				"vm", "run-command", "invoke",
				"--resource-group", "rg-dev",
				"--name", cfg.InstanceName(),
				"--command-id", "RunShellScript",
				"--scripts", azureStartupGuardScript,
				"--parameters", tt.mode,
				"--query", "value[0].message",
				"--output", "tsv",
				"--subscription", "sub-123",
			)
			if err := New(cfg, f).ensureStartupGuard(context.Background()); err != nil {
				t.Fatalf("ensureStartupGuard: %v", err)
			}
		})
	}
}

func TestSSHConfigDoesNotRepairNonAuthenticationFailure(t *testing.T) {
	tempHome(t)
	stubSSHFunctions(t, func(context.Context, *sshrun.Client) error {
		return errors.New("sshrun: parse private key: invalid format")
	})

	cfg := testConfig()
	f := shell.NewFakeCommander()
	f.Stub("10.1.2.3\n", "az",
		"vm", "show",
		"--resource-group", "rg-dev",
		"--name", cfg.InstanceName(),
		"--show-details",
		"--query", "privateIps",
		"--output", "tsv",
		"--subscription", "sub-123",
	)

	_, err := New(cfg, f).SSHConfig(context.Background())
	if err == nil || !strings.Contains(err.Error(), "verify SSH access") {
		t.Fatalf("expected SSH verification error, got %v", err)
	}
	for _, call := range f.Calls {
		if call.Name == "az" && len(call.Args) >= 3 && call.Args[0] == "vm" && call.Args[1] == "run-command" {
			t.Fatalf("non-authentication failure triggered key repair: %#v", call)
		}
	}
}

func TestExecTtyUsesAzureSSH(t *testing.T) {
	tempHome(t)
	f := shell.NewFakeCommander()
	p := New(testConfig(), f)
	identity, err := p.sshIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Exec(context.Background(), nil, provider.ExecOptions{Tty: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !f.CalledWith("az", "ssh", "vm",
		"--resource-group", "rg-dev",
		"--name", testConfig().InstanceName(),
		"--local-user", "azureuser",
		"--private-key-file", identity.PrivatePath,
		"--subscription", "sub-123",
	) {
		t.Error("expected az ssh vm to be called")
		for _, c := range f.Calls {
			t.Logf("  call: %s %v", c.Name, c.Args)
		}
	}
}

func TestSSHIdentityPreservesExplicitPrivateKeyAndDerivesMissingPublicKey(t *testing.T) {
	tempHome(t)
	pair, err := sshkey.EnsureManaged("test", "source-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(pair.PublicPath); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	cfg.Provider.Azure.SSHPrivateKeyFile = pair.PrivatePath
	identity, err := New(cfg, shell.NewFakeCommander()).sshIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if identity.PrivatePath != pair.PrivatePath || identity.PublicPath != pair.PublicPath {
		t.Fatalf("explicit key paths changed: %#v", identity)
	}
	if identity.PublicKey != pair.PublicKey {
		t.Fatal("derived public key does not match explicit private key")
	}
	if _, err := os.Stat(pair.PublicPath); err != nil {
		t.Fatalf("derived public key was not written: %v", err)
	}
}

func TestCopyFile_RequiresSSHConfig(t *testing.T) {
	p := New(testConfig(), shell.NewFakeCommander())
	err := p.CopyFile(context.Background(), "/tmp/bootstrap.sh", "/tmp/bootstrap.sh")
	if err == nil {
		t.Fatal("expected error when SSHConfig not called, got nil")
	}
}

func TestEnsureSSHNSGRuleCreatesRuleWhenMissing(t *testing.T) {
	cfg := testConfig()
	cfg.Provider.Azure.SSHSources = []string{"10.130.1.0/27"}
	f := shell.NewFakeCommander()
	instanceName := cfg.InstanceName()
	f.StubErr(errors.New("not found"), "az",
		"network", "nsg", "rule", "show",
		"--resource-group", "rg-dev",
		"--nsg-name", instanceName+"NSG",
		"--name", "mutapod-ssh",
		"--output", "none",
		"--subscription", "sub-123",
	)

	p := New(cfg, f)
	if err := p.ensureSSHNSGRule(context.Background()); err != nil {
		t.Fatalf("ensureSSHNSGRule: %v", err)
	}

	if !f.CalledWith("az",
		"network", "nsg", "rule", "create",
		"--resource-group", "rg-dev",
		"--nsg-name", instanceName+"NSG",
		"--name", "mutapod-ssh",
		"--priority", "1000",
		"--direction", "Inbound",
		"--access", "Allow",
		"--protocol", "Tcp",
		"--source-address-prefixes", "10.130.1.0/27",
		"--source-port-ranges", "*",
		"--destination-address-prefixes", "*",
		"--destination-port-ranges", "22",
		"--description", "Allow mutapod SSH from configured private sources",
		"--subscription", "sub-123",
	) {
		t.Error("expected az network nsg rule create to be called")
		for _, c := range f.Calls {
			t.Logf("  call: %s %v", c.Name, c.Args)
		}
	}
}

func TestEnsureSSHNSGRuleUpdatesExistingRule(t *testing.T) {
	cfg := testConfig()
	cfg.Provider.Azure.SSHSources = []string{"10.130.1.0/27", "10.130.2.0/27"}
	f := shell.NewFakeCommander()
	instanceName := cfg.InstanceName()

	p := New(cfg, f)
	if err := p.ensureSSHNSGRule(context.Background()); err != nil {
		t.Fatalf("ensureSSHNSGRule: %v", err)
	}

	if !f.CalledWith("az",
		"network", "nsg", "rule", "update",
		"--resource-group", "rg-dev",
		"--nsg-name", instanceName+"NSG",
		"--name", "mutapod-ssh",
		"--priority", "1000",
		"--direction", "Inbound",
		"--access", "Allow",
		"--protocol", "Tcp",
		"--source-address-prefixes", "10.130.1.0/27", "10.130.2.0/27",
		"--source-port-ranges", "*",
		"--destination-address-prefixes", "*",
		"--destination-port-ranges", "22",
		"--description", "Allow mutapod SSH from configured private sources",
		"--subscription", "sub-123",
	) {
		t.Fatalf("expected NSG rule update, got %#v", f.Calls)
	}
}

func TestEnsureSSHNSGRuleRemovesManagedRuleWhenSourcesEmpty(t *testing.T) {
	cfg := testConfig()
	f := shell.NewFakeCommander()
	instanceName := cfg.InstanceName()

	p := New(cfg, f)
	if err := p.ensureSSHNSGRule(context.Background()); err != nil {
		t.Fatalf("ensureSSHNSGRule: %v", err)
	}

	if !f.CalledWith("az",
		"network", "nsg", "rule", "delete",
		"--resource-group", "rg-dev",
		"--nsg-name", instanceName+"NSG",
		"--name", "mutapod-ssh",
		"--subscription", "sub-123",
	) {
		t.Fatalf("expected managed NSG rule delete, got %#v", f.Calls)
	}
}

func TestIsSSHStartupErrorTreatsWindowsConnectTimeoutAsTransient(t *testing.T) {
	err := errors.New("sshrun: connect to capture host key: dial tcp 10.150.170.36:22: connectex: A connection attempt failed because the connected party did not properly respond after a period of time")
	if !isSSHStartupError(err) {
		t.Fatalf("expected Windows connect timeout to be transient")
	}
}

func TestIsRunCommandRetryable(t *testing.T) {
	if !isRunCommandRetryable(errors.New("Conflict: Run command extension execution is in progress")) {
		t.Fatal("expected active Run Command conflict to be retryable")
	}
	if isRunCommandRetryable(errors.New("AuthorizationFailed")) {
		t.Fatal("authorization failure must not be retryable")
	}
}

func TestAuthorizedKeysScriptPreservesUnrelatedKeysAndIsIdempotent(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	bin := filepath.Join(root, "bin")
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := "mutapod-test-client-target"
	authorizedKeys := filepath.Join(sshDir, "authorized_keys")
	initial := "ssh-ed25519 AAAAunrelated admin-key\nssh-ed25519 AAAAstale " + marker + "\n"
	if err := os.WriteFile(authorizedKeys, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}

	writeExecutable(t, filepath.Join(bin, "getent"), "#!/bin/sh\nprintf 'test:x:1000:1000::%s:/bin/sh\\n' \"$MUTAPOD_TEST_HOME\"\n")
	writeExecutable(t, filepath.Join(bin, "id"), "#!/bin/sh\ncase \"$1\" in\n  -gn) printf 'testgroup\\n' ;;\n  -u) printf '1000\\n' ;;\n  *) exit 1 ;;\nesac\n")
	writeExecutable(t, filepath.Join(bin, "chown"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(bin, "install"), "#!/bin/sh\nfor arg do target=\"$arg\"; done\nmkdir -p \"$target\"\nchmod 700 \"$target\"\n")
	writeExecutable(t, filepath.Join(bin, "sshd"), "#!/bin/sh\nprintf 'pubkeyauthentication yes\\nauthorizedkeysfile %%h/.ssh/authorized_keys %%h/.ssh/authorized_keys2\\n'\n")

	publicKey := "ssh-ed25519 AAAAcurrent"
	encoded := base64.RawStdEncoding.EncodeToString([]byte(publicKey))
	if strings.Contains(encoded, "=") {
		t.Fatalf("test transport unexpectedly contains padding: %q", encoded)
	}
	scriptPath := filepath.Join("scripts", "ensure_authorized_key.sh")
	absoluteScriptPath, err := filepath.Abs(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	runScript := func(encodedKey, keyMarker string) ([]byte, error) {
		command := "PATH=" + shellTestQuote(bashPath(bash, bin)+":/usr/bin:/bin") +
			"; export PATH; MUTAPOD_TEST_HOME=" + shellTestQuote(bashPath(bash, home)) +
			"; export MUTAPOD_TEST_HOME; " + shellTestQuote(bashPath(bash, absoluteScriptPath)) +
			" test " + shellTestQuote(encodedKey) + " " + shellTestQuote(keyMarker)
		args := []string{"-c", command}
		executable := bash
		if isWSLBash(bash) {
			wsl, err := exec.LookPath("wsl")
			if err != nil {
				return nil, err
			}
			executable = wsl
			args = append([]string{"--", "bash"}, args...)
		}
		cmd := exec.Command(executable, args...)
		return cmd.CombinedOutput()
	}
	for run := 0; run < 2; run++ {
		if output, err := runScript(encoded, marker); err != nil {
			t.Fatalf("run authorized_keys script: %v\n%s", err, output)
		}
	}
	if output, err := runScript("", marker); err == nil {
		t.Fatalf("empty public key transport unexpectedly succeeded:\n%s", output)
	} else if !strings.Contains(string(output), "empty or invalid") {
		t.Fatalf("unexpected empty-key failure: %v\n%s", err, output)
	}

	data, err := os.ReadFile(authorizedKeys)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "ssh-ed25519 AAAAunrelated admin-key") {
		t.Fatalf("unrelated key was removed:\n%s", text)
	}
	if strings.Contains(text, "AAAAstale") {
		t.Fatalf("stale managed key was preserved:\n%s", text)
	}
	if strings.Count(text, publicKey+" "+marker) != 1 {
		t.Fatalf("managed key is not idempotent:\n%s", text)
	}
	secondData, err := os.ReadFile(filepath.Join(sshDir, "authorized_keys2"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(secondData), publicKey+" "+marker) != 1 {
		t.Fatalf("managed key missing from secondary effective path:\n%s", secondData)
	}
}

func TestEnsureSSHConfigEntryReplacesExistingBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("Host old\n    HostName old.example\n\nHost vm.azure\n    HostName stale\n"), 0600); err != nil {
		t.Fatal(err)
	}

	err := ensureSSHConfigEntry(path, sshConfigEntry{
		Alias:          "vm.azure",
		HostName:       "1.2.3.4",
		User:           "azureuser",
		Port:           22,
		IdentityFile:   filepath.Join(dir, "id_rsa"),
		KnownHostsFile: filepath.Join(dir, "known_hosts"),
		HostKeyAlias:   "vm.azure",
	})
	if err != nil {
		t.Fatalf("ensureSSHConfigEntry: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "HostName stale") {
		t.Fatalf("stale host block was not replaced:\n%s", text)
	}
	if strings.Count(text, "Host vm.azure") != 1 {
		t.Fatalf("expected one vm.azure block:\n%s", text)
	}
	if !strings.Contains(text, "Host old") {
		t.Fatalf("unrelated host block was removed:\n%s", text)
	}
}

func TestInstanceID(t *testing.T) {
	p := New(testConfig(), shell.NewFakeCommander())
	want := "/subscriptions/sub-123/resourceGroups/rg-dev/providers/Microsoft.Compute/virtualMachines/" + testConfig().InstanceName()
	got, err := p.InstanceID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("InstanceID: got %q, want %q", got, want)
	}
}

func TestInstanceIDUsesActiveSubscription(t *testing.T) {
	cfg := testConfig()
	cfg.Provider.Azure.Subscription = ""
	f := shell.NewFakeCommander()
	f.Stub("active-sub\n", "az", "account", "show", "--query", "id", "--output", "tsv")

	got, err := New(cfg, f).InstanceID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "/subscriptions/active-sub/resourceGroups/rg-dev/providers/Microsoft.Compute/virtualMachines/" + cfg.InstanceName()
	if got != want {
		t.Fatalf("InstanceID: got %q, want %q", got, want)
	}
}

func TestInstanceMetadata(t *testing.T) {
	cfg := testConfig()
	f := shell.NewFakeCommander()
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-dev/providers/Microsoft.Compute/virtualMachines/" + cfg.InstanceName()
	f.Stub(`{"id":"`+resourceID+`","tags":{"managed-by":"mutapod","MUTAPOD-CONFIG":"v1-abc"}}`, "az",
		"vm", "show",
		"--resource-group", "rg-dev",
		"--name", cfg.InstanceName(),
		"--query", "{id:id,tags:tags}",
		"--output", "json",
		"--subscription", "sub-123",
	)

	metadata, err := New(cfg, f).InstanceMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ID != resourceID || metadata.ConfigFingerprint != "v1-abc" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
}

func TestAdoptInstance(t *testing.T) {
	cfg := testConfig()
	f := shell.NewFakeCommander()
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-dev/providers/Microsoft.Compute/virtualMachines/" + cfg.InstanceName()

	if err := New(cfg, f).AdoptInstance(context.Background(), "v1-abc"); err != nil {
		t.Fatal(err)
	}
	if !f.CalledWith("az", "tag", "update",
		"--resource-id", resourceID,
		"--operation", "Merge",
		"--tags", "mutapod-config=v1-abc",
		"--subscription", "sub-123",
	) {
		t.Fatalf("unexpected calls: %#v", f.Calls)
	}
}

func tempHome(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func stubSSHFunctions(t *testing.T, probe func(context.Context, *sshrun.Client) error) {
	t.Helper()
	oldTrustHost := trustHostFunc
	oldProbeSSH := probeSSHFunc
	trustHostFunc = func(client *sshrun.Client, knownHostsFile, hostKeyAlias string) error { return nil }
	probeSSHFunc = probe
	t.Cleanup(func() {
		trustHostFunc = oldTrustHost
		probeSSHFunc = oldProbeSSH
	})
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

func bashPath(bash, path string) string {
	path = filepath.ToSlash(path)
	if len(path) >= 3 && path[1] == ':' && path[2] == '/' {
		prefix := "/"
		if isWSLBash(bash) {
			prefix = "/mnt/"
		}
		return prefix + strings.ToLower(path[:1]) + path[2:]
	}
	return path
}

func isWSLBash(bash string) bool {
	return strings.Contains(strings.ToLower(filepath.ToSlash(bash)), "/windows/system32/")
}

func shellTestQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

type callbackCommander struct {
	calls  []shell.Call
	run    func(context.Context, shell.RunOptions, string, ...string) error
	output func(context.Context, shell.RunOptions, string, ...string) ([]byte, error)
}

func (c *callbackCommander) Run(ctx context.Context, opts shell.RunOptions, name string, args ...string) error {
	c.calls = append(c.calls, shell.Call{Name: name, Args: append([]string(nil), args...), Opts: opts})
	if c.run != nil {
		return c.run(ctx, opts, name, args...)
	}
	return nil
}

func (c *callbackCommander) Output(ctx context.Context, opts shell.RunOptions, name string, args ...string) ([]byte, error) {
	c.calls = append(c.calls, shell.Call{Name: name, Args: append([]string(nil), args...), Opts: opts})
	if c.output != nil {
		return c.output(ctx, opts, name, args...)
	}
	return nil, nil
}

func (c *callbackCommander) calledWith(name string, requiredArgs ...string) bool {
	for _, call := range c.calls {
		if call.Name != name {
			continue
		}
		joined := " " + strings.Join(call.Args, " ") + " "
		matches := true
		for _, arg := range requiredArgs {
			if !strings.Contains(joined, " "+arg+" ") {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func boolPtr(value bool) *bool {
	return &value
}
