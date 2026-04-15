package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func Execute(ctx context.Context) error {
	return newRootCmd(ctx).Execute()
}

func newRootCmd(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "system1",
		Short: "System-1 subconscious runtime",
	}

	cmd.AddCommand(newServeCmd(ctx))
	cmd.AddCommand(newDoctorCmd(ctx))
	cmd.AddCommand(newSessionCmd(ctx))
	cmd.AddCommand(newIntrospectCmd(ctx))
	cmd.AddCommand(newObserveCmd(ctx))
	cmd.AddCommand(newDemoCmd(ctx))

	return cmd
}
