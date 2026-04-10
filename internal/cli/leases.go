package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/mutapod/mutapod/internal/idle"
	"github.com/mutapod/mutapod/internal/provider"
	"github.com/mutapod/mutapod/internal/shell"
)

func leasesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "leases",
		Short: "Show active mutapod workspace leases on the remote VM",
		RunE:  runLeases,
	}
}

func runLeases(_ *cobra.Command, _ []string) error {
	ctx := context.Background()

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	prov, err := provider.New(cfg, shell.DefaultCommander)
	if err != nil {
		return err
	}
	if _, err := prov.SSHConfig(ctx); err != nil {
		return err
	}

	leases, err := idle.ListLeases(ctx, prov)
	if err != nil {
		return err
	}
	sort.Slice(leases, func(i, j int) bool {
		return leases[i].Workspace < leases[j].Workspace
	})

	if len(leases) == 0 {
		fmt.Println("No mutapod leases found on the remote VM.")
		return nil
	}

	now := time.Now()
	fmt.Println("Remote mutapod leases:")
	for _, lease := range leases {
		expiresAt := time.Unix(lease.ExpiresUnix, 0)
		timeoutMinutes := cfg.Idle.TimeoutMinutes
		if timeoutMinutes <= 0 {
			timeoutMinutes = 30
		}
		lastHeartbeat := expiresAt.Add(-time.Duration(timeoutMinutes) * time.Minute)
		remaining := expiresAt.Sub(now).Round(time.Second)
		if remaining < 0 {
			remaining = 0
		}
		fmt.Fprintf(os.Stdout, "  Workspace:      %s\n", lease.Workspace)
		fmt.Fprintf(os.Stdout, "  Host ID:        %s\n", lease.HostID)
		fmt.Fprintf(os.Stdout, "  Last heartbeat: %s\n", lastHeartbeat.Format(time.RFC3339))
		fmt.Fprintf(os.Stdout, "  Expires:        %s\n", expiresAt.Format(time.RFC3339))
		fmt.Fprintf(os.Stdout, "  Time left:      %s\n", remaining)
		fmt.Fprintln(os.Stdout)
	}

	return nil
}
