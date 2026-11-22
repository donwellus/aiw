package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/control"
)

func newApproveCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "approve <run-id> <gate>",
		Short: "Aprova um gate de uma execução pausada",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			run, err := control.Approve(cfg, args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "gate %s aprovado na run %s\n", args[1], run.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "continue com: aiw resume %s\n", run.ID)
			return nil
		},
	}
}
