package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/control"
	"github.com/donwellus/aiw/internal/state"
)

func newCancelCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <run-id>",
		Short: "Cancela uma run em andamento ou aguardando input",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			store, err := state.Open(cfg.StateDir())
			if err != nil {
				return err
			}
			result, err := control.Cancel(context.Background(), cfg, store, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if result.Status == "canceling" {
				fmt.Fprintf(out, "cancelamento solicitado para %s; status=%s\n", result.RunID, result.Status)
			} else {
				fmt.Fprintf(out, "run %s cancelada\n", result.RunID)
			}
			if result.Message != "" {
				fmt.Fprintln(out, result.Message)
			}
			return nil
		},
	}
}
