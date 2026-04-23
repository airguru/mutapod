//go:build !windows

package cli

import (
	"os"
	"syscall"
)

func relaunch(path string) error {
	return syscall.Exec(path, os.Args, os.Environ())
}
