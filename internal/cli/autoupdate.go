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

func maybeCheckForUpdate(cmd *cobra.Command) {
	if cmd == nil || skippedAutoUpdateCommands[cmd.Name()] {
		return
	}
	if !isReleaseBuild() {
		return
	}
	if os.Getenv("MUTAPOD_SKIP_UPDATE_CHECK") == "1" {
		return
	}
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		return
	}

	updater, err := update.New()
	if err != nil {
		return
	}

	checkCtx, cancelCheck := context.WithTimeout(context.Background(), autoUpdateCheckTimeout)
	defer cancelCheck()
	status, err := updater.Check(checkCtx, buildinfo.DisplayVersion())
	if err != nil || status.UpToDate {
		return
	}

	current := displayCurrentVersion(status)
	fmt.Printf("A new mutapod version is available: %s (current: %s)\n", status.Latest.TagName, current)
	fmt.Printf("Update now? [y/N] (continuing in %ds if no response): ", int(autoUpdatePromptTimeout.Seconds()))

	answer, got := readLineWithTimeout(os.Stdin, autoUpdatePromptTimeout)
	if !got {
		fmt.Println()
		fmt.Println("No response — continuing with current version.")
		return
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return
	}

	fmt.Println("Downloading update...")
	dctx, dcancel := context.WithTimeout(context.Background(), autoUpdateDownloadTimeout)
	defer dcancel()
	result, err := updater.Update(dctx, buildinfo.DisplayVersion())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "Continuing with current version.")
		return
	}
	if !result.Updated {
		return
	}

	fmt.Printf("Updated mutapod to %s.\n", result.Release.TagName)

	if result.PendingRestart {
		fmt.Println("The new version will be used the next time you run mutapod. Continuing with current version...")
		return
	}

	fmt.Println("Relaunching with new version...")
	if err := relaunch(result.ExecutablePath); err != nil {
		fmt.Fprintf(os.Stderr, "Relaunch failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "Please re-run the command to use the new version.")
		os.Exit(0)
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
