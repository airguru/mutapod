// Package sshkey manages SSH credentials owned by mutapod.
package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gofrs/flock"
	gossh "golang.org/x/crypto/ssh"
)

const clientIDBytes = 16

// Pair is a mutapod-managed SSH key pair for one cloud target.
type Pair struct {
	PrivatePath   string
	PublicPath    string
	PublicKey     string
	Marker        string
	AuthorizedKey string
}

// EnsureManaged creates or loads a dedicated Ed25519 key pair for target.
// The key is isolated from the user's personal OpenSSH identities.
func EnsureManaged(provider, target string) (*Pair, error) {
	provider = safeComponent(provider)
	target = strings.TrimSpace(target)
	if provider == "" || target == "" {
		return nil, fmt.Errorf("sshkey: provider and target are required")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("sshkey: home dir: %w", err)
	}
	root := filepath.Join(home, ".mutapod")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("sshkey: create root: %w", err)
	}
	_ = os.Chmod(root, 0700)

	clientID, err := ensureClientID(root)
	if err != nil {
		return nil, err
	}
	targetSum := sha256.Sum256([]byte(target))
	targetID := hex.EncodeToString(targetSum[:8])
	marker := "mutapod-" + clientID + "-" + targetID
	dir := filepath.Join(root, "keys", provider, targetID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("sshkey: create key dir: %w", err)
	}
	_ = os.Chmod(dir, 0700)

	privatePath := filepath.Join(dir, "id_ed25519")
	publicPath := privatePath + ".pub"
	lock := flock.New(privatePath + ".lock")
	if err := lock.Lock(); err != nil {
		return nil, fmt.Errorf("sshkey: lock managed key: %w", err)
	}
	defer lock.Unlock()

	signer, err := loadManagedSigner(privatePath)
	if err != nil {
		if !os.IsNotExist(err) {
			if archiveErr := archiveInvalidPair(privatePath, publicPath); archiveErr != nil {
				return nil, fmt.Errorf("sshkey: archive invalid managed key: %w", archiveErr)
			}
		}
		signer, err = generateManagedSigner(privatePath, marker)
		if err != nil {
			return nil, err
		}
	}

	publicKey := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey())))
	authorizedKey := publicKey + " " + marker
	if err := os.WriteFile(publicPath, []byte(authorizedKey+"\n"), 0644); err != nil {
		return nil, fmt.Errorf("sshkey: write public key: %w", err)
	}
	if err := securePrivateKey(privatePath); err != nil {
		return nil, fmt.Errorf("sshkey: secure private key: %w", err)
	}
	_ = os.Chmod(publicPath, 0644)

	return &Pair{
		PrivatePath:   privatePath,
		PublicPath:    publicPath,
		PublicKey:     publicKey,
		Marker:        marker,
		AuthorizedKey: authorizedKey,
	}, nil
}

func ensureClientID(root string) (string, error) {
	path := filepath.Join(root, "client-id")
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return "", fmt.Errorf("sshkey: lock client id: %w", err)
	}
	defer lock.Unlock()

	if data, err := os.ReadFile(path); err == nil {
		value := strings.TrimSpace(string(data))
		if len(value) == clientIDBytes*2 {
			if _, decodeErr := hex.DecodeString(value); decodeErr == nil {
				return value, nil
			}
		}
		if renameErr := archiveInvalid(path); renameErr != nil {
			return "", fmt.Errorf("sshkey: archive invalid client id: %w", renameErr)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("sshkey: read client id: %w", err)
	}

	random := make([]byte, clientIDBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("sshkey: generate client id: %w", err)
	}
	value := hex.EncodeToString(random)
	if err := writeNewFile(path, []byte(value+"\n"), 0600); err != nil {
		return "", fmt.Errorf("sshkey: write client id: %w", err)
	}
	return value, nil
}

func loadManagedSigner(path string) (gossh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := gossh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return signer, nil
}

func generateManagedSigner(path, marker string) (gossh.Signer, error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("sshkey: generate Ed25519 key: %w", err)
	}
	block, err := gossh.MarshalPrivateKey(privateKey, marker)
	if err != nil {
		return nil, fmt.Errorf("sshkey: marshal private key: %w", err)
	}
	if err := writeNewFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		return nil, fmt.Errorf("sshkey: write private key: %w", err)
	}
	signer, err := gossh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("sshkey: create signer: %w", err)
	}
	return signer, nil
}

func writeNewFile(path string, data []byte, perm os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return closeErr
	}
	_ = os.Chmod(path, perm)
	return nil
}

func archiveInvalidPair(privatePath, publicPath string) error {
	if err := archiveInvalid(privatePath); err != nil {
		return err
	}
	if _, err := os.Stat(publicPath); err == nil {
		return archiveInvalid(publicPath)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func archiveInvalid(path string) error {
	suffix := time.Now().UTC().Format("20060102T150405.000000000Z")
	return os.Rename(path, path+".invalid-"+suffix)
}

func safeComponent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var result strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			result.WriteRune(r)
		}
	}
	if runtime.GOOS == "windows" {
		return strings.Trim(result.String(), ". ")
	}
	return result.String()
}
