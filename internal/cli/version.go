package cli

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mutapod/mutapod/internal/buildinfo"
	"github.com/mutapod/mutapod/internal/update"
)

var updateCheck bool

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the installed mutapod version",
		RunE:  runVersion,
	}
}

func updateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for or install the latest mutapod release",
		RunE:  runUpdate,
	}
	cmd.Flags().BoolVar(&updateCheck, "check", false, "check GitHub releases and report whether an update is available")
	return cmd
}

func runVersion(_ *cobra.Command, _ []string) error {
	fmt.Fprintf(os.Stdout, "mutapod %s\n", buildinfo.DisplayVersion())
	if strings.TrimSpace(buildinfo.Commit) != "" {
		fmt.Fprintf(os.Stdout, "commit: %s\n", buildinfo.Commit)
	}
	if strings.TrimSpace(buildinfo.Date) != "" {
		fmt.Fprintf(os.Stdout, "built: %s\n", buildinfo.Date)
	}
	fmt.Fprintf(os.Stdout, "platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return nil
}

func runUpdate(_ *cobra.Command, _ []string) error {
	ctx := context.Background()

	updater, err := update.New()
	if err != nil {
		return err
	}

	step("Checking GitHub releases...")
	if updateCheck {
		status, err := updater.Check(ctx, buildinfo.DisplayVersion())
		if err != nil {
			return err
		}
		current := displayCurrentVersion(status)
		if status.UpToDate {
			ok("mutapod %s is up to date", status.Latest.TagName)
			return nil
		}
		ok("Update available: %s (current: %s)", status.Latest.TagName, current)
		if status.Latest.DownloadPage != "" {
			fmt.Fprintf(os.Stdout, "Release notes: %s\n", status.Latest.DownloadPage)
		}
		return nil
	}

	result, err := updater.Update(ctx, buildinfo.DisplayVersion())
	if err != nil {
		return err
	}
	if !result.Updated {
		ok("mutapod %s is already up to date", result.Release.TagName)
		return nil
	}
	if result.PendingRestart {
		ok("Update to %s has been staged for %s", result.Release.TagName, result.ExecutablePath)
		fmt.Fprintln(os.Stdout, "Restart your terminal after this command exits to use the new version.")
		return nil
	}

	ok("Updated mutapod to %s", result.Release.TagName)
	return nil
}

func displayCurrentVersion(status *update.Status) string {
	if status.CurrentVersionKnown {
		return "v" + normalizeCLIUpdateVersion(status.CurrentVersion)
	}
	return status.CurrentVersion
}

func normalizeCLIUpdateVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
