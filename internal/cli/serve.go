package cli

import (
	"context"

	"github.com/XferOps/system1/internal/app"
	"github.com/spf13/cobra"
)

func newServeCmd(ctx context.Context) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the System-1 daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.New()
			if err != nil {
				return err
			}
			return a.Run(ctx)
		},
	}
}
