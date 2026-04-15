package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/XferOps/system1/internal/app"
	"github.com/spf13/cobra"
)

func newIntrospectCmd(ctx context.Context) *cobra.Command {
	var debug bool
	cmd := &cobra.Command{
		Use:   "introspect <query>",
		Short: "Run an Introspection query through the local scaffold",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.New()
			if err != nil {
				return err
			}
			result, err := a.Introspection.Query(ctx, strings.Join(args, " "), debug)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), result.Answer)
			if debug {
				fmt.Fprintf(cmd.OutOrStdout(), "artifact_refs=%v\nevidence=%v\n", result.ArtifactRefs, result.Evidence)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&debug, "debug", false, "include debug evidence output")
	return cmd
}
