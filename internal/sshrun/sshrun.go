// Package sshrun runs commands and uploads files on a remote host using
// pure-Go SSH — no system SSH client or PuTTY required.
package sshrun

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Client is a lightweight SSH client.
type Client struct {
	ip           string
	port         int
	user         string
	identityFile string
}

// New creates a Client that connects to ip:port with the given identity file.
func New(ip string, port int, user, identityFile string) *Client {
	return &Client{ip: ip, port: port, user: user, identityFile: identityFile}
}

func (c *Client) dial() (*gossh.Client, error) {
	key, err := os.ReadFile(c.identityFile)
	if err != nil {
		return nil, fmt.Errorf("sshrun: read identity file %q: %w", c.identityFile, err)
	}
	signer, err := gossh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("sshrun: parse private key: %w", err)
	}
	cfg := &gossh.ClientConfig{
		User:            c.user,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec — VM is ours
		Timeout:         30 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", c.ip, c.port)
	return gossh.Dial("tcp", addr, cfg)
}

// Run executes a shell command on the remote host.
func (c *Client) Run(ctx context.Context, cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()

	session, err := conn.NewSession()
	if err != nil {
		return fmt.Errorf("sshrun: new session: %w", err)
	}
	defer session.Close()

	session.Stdin = stdin
	session.Stdout = stdout
	session.Stderr = stderr

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()

	select {
	case <-ctx.Done():
		_ = session.Signal(gossh.SIGTERM)
		return ctx.Err()
	case err := <-done:
		return err
	}
}

// TrustHost scans the remote server's host key and appends it to knownHostsFile
// under hostAlias. This lets external tools (e.g. mutagen) verify the host
// without ever having connected via an interactive SSH client.
// If the alias is already present in the file it does nothing.
func (c *Client) TrustHost(knownHostsFile, hostAlias string) error {
	// Check if already present.
	if data, err := os.ReadFile(knownHostsFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, hostAlias+" ") {
				return nil // already trusted
			}
		}
	}

	keyData, err := os.ReadFile(c.identityFile)
	if err != nil {
		return fmt.Errorf("sshrun: read identity file: %w", err)
	}
	signer, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("sshrun: parse private key: %w", err)
	}

	var captured gossh.PublicKey
	cfg := &gossh.ClientConfig{
		User: c.user,
		Auth: []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: func(_ string, _ net.Addr, key gossh.PublicKey) error {
			captured = key
			return nil
		},
		Timeout: 30 * time.Second,
	}
	conn, err := gossh.Dial("tcp", fmt.Sprintf("%s:%d", c.ip, c.port), cfg)
	if err != nil {
		return fmt.Errorf("sshrun: connect to capture host key: %w", err)
	}
	conn.Close()

	if err := os.MkdirAll(filepath.Dir(knownHostsFile), 0700); err != nil {
		return fmt.Errorf("sshrun: create known_hosts dir: %w", err)
	}
	f, err := os.OpenFile(knownHostsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("sshrun: open known_hosts %s: %w", knownHostsFile, err)
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, knownhosts.Line([]string{hostAlias}, captured))
	return err
}

// Upload copies a local file to remotePath on the remote host by piping
// its content through `cat`. No SFTP or SCP binary required.
func (c *Client) Upload(ctx context.Context, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("sshrun: open %q: %w", localPath, err)
	}
	defer f.Close()

	// Escape single quotes in the remote path.
	safe := strings.ReplaceAll(remotePath, "'", "'\\''")
	return c.Run(ctx, "cat > '"+safe+"'", f, io.Discard, io.Discard)
}
