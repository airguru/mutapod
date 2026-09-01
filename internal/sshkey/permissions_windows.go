//go:build windows

package sshkey

import (
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

func securePrivateKey(path string) error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolve current Windows user: %w", err)
	}
	principal := strings.TrimSpace(currentUser.Username)
	if principal == "" {
		return fmt.Errorf("current Windows username is empty")
	}

	// os.Chmod does not remove inherited Windows ACEs. OpenSSH rejects a
	// private key readable by broad inherited groups, so replace inheritance
	// with one explicit full-control entry for the user running mutapod.
	out, err := exec.Command("icacls.exe", path,
		"/inheritance:r",
		"/grant:r", principal+":(F)",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
