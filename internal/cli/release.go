package cli

import (
	"fmt"

	"github.com/spf13/cobra"

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
			runID := args[0]
			record, recordFound := store.FindByRunID(runID)
			claim, claimFound, err := store.ReleaseClaimByRunID(runID)
			if err != nil {
				return err
			}
			if !recordFound && !claimFound {
				return fmt.Errorf("run %s não encontrada no estado nem nos claims", runID)
			}
			if claimFound {
				fmt.Fprintf(cmd.OutOrStdout(), "claim liberado: %s:%s (pid %d)\n", claim.Source, claim.ID, claim.PID)
			}
			if recordFound {
				record.Status = "todo"
				record.RunID = ""
				if err := store.Put(record); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "tarefa %s:%s marcada como todo\n", record.Source, record.ID)
			}
			return nil
		},
	}
}
