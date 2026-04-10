package sshrun

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

func TestTrustHostSucceedsWhenHostKeyCapturedBeforeAuthReady(t *testing.T) {
	hostSigner := mustGenerateSigner(t)

	serverConfig := &gossh.ServerConfig{
		PublicKeyCallback: func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			return nil, errors.New("auth not ready")
		},
	}
	serverConfig.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _, _ = gossh.NewServerConn(conn, serverConfig)
	}()

	identityFile := filepath.Join(t.TempDir(), "id_test")
	writePrivateKeyFile(t, identityFile)
	knownHostsFile := filepath.Join(t.TempDir(), "known_hosts")

	client := New("127.0.0.1", listener.Addr().(*net.TCPAddr).Port, "tester", identityFile)
	if err := client.TrustHost(knownHostsFile, "vm-alias"); err != nil {
		t.Fatalf("TrustHost: %v", err)
	}

	data, err := os.ReadFile(knownHostsFile)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if !strings.Contains(string(data), "vm-alias ") {
		t.Fatalf("known_hosts missing alias entry: %q", string(data))
	}

	<-done
}

func mustGenerateSigner(t *testing.T) gossh.Signer {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := gossh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return signer
}

func writePrivateKeyFile(t *testing.T, path string) {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	keyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatalf("write client key: %v", err)
	}
}
