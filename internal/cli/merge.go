package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/state"
)

func newMergeCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "merge <run-id>",
		Short: "Adota uma run com PR existente, valida CI e faz merge",
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
			run, err := pipeline.LoadRun(cfg, args[0])
			if err != nil {
				return err
			}
			if run.PRURL == "" {
				return fmt.Errorf("run %s não tem PR", run.ID)
			}
			p := pipeline.New(cfg)
			defer closeTelemetry(p)
			if err := p.MergePR(context.Background(), run.ID); err != nil {
				return err
			}
			run, err = pipeline.LoadRun(cfg, run.ID)
			if err != nil {
				return err
			}
			record, found := store.FindByRunID(run.ID)
			if !found {
				record, found = store.FindByID(run.Task.ExternalID)
			}
			if found {
				record.Status = "succeeded"
				record.RunID = run.ID
				record.ContentHash = run.Task.ContentHash
				if err := store.Put(record); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "PR mergeado: %s\n", run.PRURL)
			if found {
				fmt.Fprintf(out, "estado reconciliado: %s:%s status=succeeded run=%s\n", record.Source, record.ID, run.ID)
			}
			return nil
		},
	}
}
