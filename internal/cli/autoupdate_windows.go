//go:build windows

package cli

import "fmt"

func relaunch(path string) error {
	return fmt.Errorf("relaunch not supported on Windows (update is staged and takes effect on next run)")
}
