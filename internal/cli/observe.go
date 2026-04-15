package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/XferOps/system1/internal/app"
	"github.com/spf13/cobra"
)

func newObserveCmd(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "observe",
		Short: "Observability commands for System-1",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show current health status",
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

	cmd.AddCommand(&cobra.Command{
		Use:   "decisions",
		Short: "Show recent decisions",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.New()
			if err != nil {
				return err
			}
			decisions := a.DecisionLog.Recent(10)
			if len(decisions) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No decisions recorded")
				return nil
			}
			for _, d := range decisions {
				fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s %s: %s (%s)\n", d.Timestamp.Format("15:04:05"), d.ArtifactType, d.Status, d.CandidateID, d.Reason)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "traces",
		Short: "Show recent introspection queries",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.New()
			if err != nil {
				return err
			}
			traces := a.IntrospectionTrace.Recent(10)
			if len(traces) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No introspection traces recorded")
				return nil
			}
			for _, t := range traces {
				fmt.Fprintf(cmd.OutOrStdout(), "[%s] query=%q results=%d\n", t.Timestamp.Format("15:04:05"), t.Query, t.ResultCount)
			}
			return nil
		},
	})

	return cmd
}
