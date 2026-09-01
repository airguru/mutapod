//go:build !windows

package cli

import (
	"os"
	"syscall"
)

func relaunch(path string, args []string) (int, error) {
	argv := append([]string{path}, args...)
	return 1, syscall.Exec(path, argv, os.Environ())
}
