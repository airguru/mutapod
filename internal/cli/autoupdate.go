package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mutapod/mutapod/internal/buildinfo"
	"github.com/mutapod/mutapod/internal/shell"
	"github.com/mutapod/mutapod/internal/update"
)

const (
	autoUpdateCheckTimeout    = 5 * time.Second
	autoUpdatePromptTimeout   = 30 * time.Second
	autoUpdateDownloadTimeout = 5 * time.Minute
)

var skippedAutoUpdateCommands = map[string]bool{
	"version":        true,
	"update":         true,
	"idle-heartbeat": true,
	"help":           true,
	"completion":     true,
}

type updatedCommandCompletedError struct {
	exitCode int
}

func (e *updatedCommandCompletedError) Error() string {
	return "updated mutapod command completed"
}

func maybeCheckForUpdate(cmd *cobra.Command) error {
	if cmd == nil || skippedAutoUpdateCommands[cmd.Name()] {
		if cmd != nil {
			shell.Debugf("update: skipping automatic check for command %q", cmd.Name())
		}
		return nil
	}
	if !isReleaseBuild() {
		shell.Debugf("update: skipping automatic check for non-release build %q", buildinfo.DisplayVersion())
		return nil
	}
	if os.Getenv(update.ResumedEnvironmentVariable) == "1" {
		_ = os.Unsetenv(update.ResumedEnvironmentVariable)
		shell.Debugf("update: skipping automatic check for command resumed after update")
		return nil
	}
	if os.Getenv("MUTAPOD_SKIP_UPDATE_CHECK") == "1" {
		shell.Debugf("update: skipping automatic check because MUTAPOD_SKIP_UPDATE_CHECK=1")
		return nil
	}
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		shell.Debugf("update: skipping automatic check because stdin/stdout is not an interactive terminal")
		return nil
	}

	updater, err := update.New()
	if err != nil {
		shell.Debugf("update: skipping automatic check: %v", err)
		return nil
	}

	shell.Debugf("update: checking GitHub releases for updates from %s", buildinfo.DisplayVersion())
	checkCtx, cancelCheck := context.WithTimeout(context.Background(), autoUpdateCheckTimeout)
	defer cancelCheck()
	status, err := updater.Check(checkCtx, buildinfo.DisplayVersion())
	if err != nil {
		shell.Debugf("update: automatic check failed: %v", err)
		return nil
	}
	if status.UpToDate {
		shell.Debugf("update: current version %s is up to date (latest: %s)", buildinfo.DisplayVersion(), status.Latest.TagName)
		return nil
	}
	shell.Debugf("update: newer version available: %s", status.Latest.TagName)

	current := displayCurrentVersion(status)
	fmt.Printf("A new mutapod version is available: %s (current: %s)\n", status.Latest.TagName, current)
	fmt.Printf("Update now? [Y/n] (updating automatically in %ds): ", int(autoUpdatePromptTimeout.Seconds()))

	answer, got := readLineWithTimeout(os.Stdin, autoUpdatePromptTimeout)
	if !got {
		fmt.Println()
		fmt.Println("No response — updating automatically.")
	} else if !shouldInstallUpdate(answer) {
		fmt.Println("Continuing with current version.")
		return nil
	}

	fmt.Println("Downloading update...")
	dctx, dcancel := context.WithTimeout(context.Background(), autoUpdateDownloadTimeout)
	defer dcancel()
	result, err := updater.UpdateForRelaunch(dctx, buildinfo.DisplayVersion())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "Continuing with current version.")
		return nil
	}
	if !result.Updated {
		return nil
	}

	fmt.Printf("Updated mutapod to %s.\n", result.Release.TagName)

	if result.RelaunchPath == "" {
		return fmt.Errorf("update: no executable was prepared for relaunch")
	}
	if result.PendingRestart {
		fmt.Println("Restarting this command with the new version...")
	} else {
		fmt.Println("Relaunching with new version...")
	}

	if err := os.Setenv(update.ResumedEnvironmentVariable, "1"); err != nil {
		return fmt.Errorf("update: mark command for relaunch: %w", err)
	}
	exitCode, err := relaunch(result.RelaunchPath, os.Args[1:])
	_ = os.Unsetenv(update.ResumedEnvironmentVariable)
	if err != nil {
		return fmt.Errorf("update: relaunch updated command: %w", err)
	}
	cmd.Root().SilenceErrors = true
	return &updatedCommandCompletedError{exitCode: exitCode}
}

func shouldInstallUpdate(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}

func readLineWithTimeout(r io.Reader, timeout time.Duration) (string, bool) {
	result := make(chan string, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil && line == "" {
			return
		}
		result <- line
	}()

	select {
	case line := <-result:
		return line, true
	case <-time.After(timeout):
		return "", false
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func isReleaseBuild() bool {
	v := strings.TrimSpace(buildinfo.DisplayVersion())
	return v != "" && v != "dev"
}
