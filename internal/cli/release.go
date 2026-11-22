package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/control"
	"github.com/donwellus/aiw/internal/state"
)

func newReleaseCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "release <run-id>",
		Short: "Libera o claim de uma execução morta e devolve a tarefa para todo",
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
			result, err := control.Release(context.Background(), cfg, store, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if result.Claim != nil {
				fmt.Fprintf(out, "claim liberado: %s:%s (pid %d)\n", result.Claim.Source, result.Claim.ID, result.Claim.PID)
			}
			if result.TaskMarked {
				fmt.Fprintf(out, "tarefa %s:%s marcada como todo\n", result.TaskSource, result.TaskID)
			}
			if result.SourceReturned {
				fmt.Fprintln(out, "tarefa devolvida ao inbox")
			}
			for _, warning := range result.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "aviso: %s\n", warning)
			}
			return nil
		},
	}
}
