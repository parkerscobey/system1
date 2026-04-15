package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/XferOps/system1/internal/app"
	"github.com/XferOps/system1/internal/config"
	"github.com/spf13/cobra"
)

func newDoctorCmd(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate local config and print basic readiness info",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ok\nstate_dir=%s\nsqlite=%s\nenabled_types=%v\n", cfg.StateDir, cfg.SQLitePath, cfg.EnabledTypes)

			a, err := app.New()
			if err != nil {
				return err
			}
			status := a.Health.Check(ctx, 0)
			statusJSON, _ := json.MarshalIndent(status, "", "  ")
			fmt.Fprintf(cmd.OutOrStdout(), "\nhealth_check:\n%s\n", statusJSON)
			return nil
		},
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Print current system health status",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.New()
			if err != nil {
				return err
			}
			status := a.Health.Check(ctx, 0)
			statusJSON, _ := json.MarshalIndent(status, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(statusJSON))
			return nil
		},
	})

	return cmd
}
