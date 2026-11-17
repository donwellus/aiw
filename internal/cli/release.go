package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/sources"
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
			run, runErr := pipeline.LoadRun(cfg, runID)
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

			sourceName := ""
			ref := source.Ref{}
			if runErr == nil {
				sourceName = run.Source
				ref = run.Ref
			}
			if sourceName == "" && claimFound {
				sourceName = claim.Source
				ref = source.Ref{Source: claim.Source, ID: claim.ID}
			}
			if sourceName != "" {
				src, err := sources.NewRegistry(cfg).Open(sourceName)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "aviso: fonte indisponível para devolver tarefa: %v\n", err)
				} else if releaser, ok := src.(source.Releaser); ok {
					if err := releaser.Release(context.Background(), ref); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "aviso: falha ao devolver tarefa ao inbox: %v\n", err)
					} else {
						fmt.Fprintln(cmd.OutOrStdout(), "tarefa devolvida ao inbox")
					}
				}
			}
			return nil
		},
	}
}
