package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/control"
)

func newUnpauseCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "unpause",
		Short: "Remove a pausa global sem retomar runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			result, err := control.Unpause(cfg)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if result.AlreadyInactive {
				fmt.Fprintln(out, "pausa global já está inativa")
				return nil
			}
			fmt.Fprintln(out, "pausa global removida; runs pausadas continuam pausadas")
			return nil
		},
	}
}
