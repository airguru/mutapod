//go:build !windows

package sshkey

import "os"

func securePrivateKey(path string) error {
	return os.Chmod(path, 0600)
}
