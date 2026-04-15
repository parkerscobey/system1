package cli

import (
	"context"
	"fmt"

	"github.com/XferOps/system1/internal/app"
	"github.com/spf13/cobra"
)

func newSessionCmd(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Session lifecycle helpers for development and debugging",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start a System-1 session",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.New()
			if err != nil {
				return err
			}
			result, err := a.SessionService.Start(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "waking_mind=%s\nambient_items=%d\n", result.WakingMind, len(result.AmbientContext))
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "end",
		Short: "End a System-1 session",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.New()
			if err != nil {
				return err
			}
			return a.SessionService.End(ctx)
		},
	})

	return cmd
}
