package cli

import (
	"context"
	"fmt"

	"github.com/XferOps/system1/internal/config"
	"github.com/spf13/cobra"
)

func newDoctorCmd(ctx context.Context) *cobra.Command {
	_ = ctx
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate local config and print basic readiness info",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ok\nstate_dir=%s\nsqlite=%s\nenabled_types=%v\n", cfg.StateDir, cfg.SQLitePath, cfg.EnabledTypes)
			return nil
		},
	}
}
