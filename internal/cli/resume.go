package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/state"
)

func newResumeCommand(opts *rootOptions) *cobra.Command {
	var auto bool
	var budget float64
	cmd := &cobra.Command{
		Use:   "resume <run-id>",
		Short: "Retoma uma execução pausada ou falha",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			run, err := pipeline.LoadRun(cfg, args[0])
			if err != nil {
				return err
			}
			p := pipeline.New(cfg)
			defer closeTelemetry(p)
			p.AutoApprove = auto

			res, runErr := p.Resume(context.Background(), args[0], pipeline.Options{BudgetUSD: budget})
			store, storeErr := state.Open(cfg.StateDir())

			if runErr != nil && errors.Is(runErr, pipeline.ErrPaused) {
				fmt.Fprintf(cmd.OutOrStdout(), "run %s continua pausada\n", res.RunID)
				fmt.Fprintf(cmd.OutOrStdout(), "aprove com: aiw approve %s <spec|plan>\n", res.RunID)
				return nil
			}
			if runErr != nil {
				if storeErr == nil {
					if err := store.Put(state.TaskRecord{Source: run.Task.Source, ID: run.Task.ExternalID, Status: "failed", RunID: run.ID, ContentHash: run.Task.ContentHash}); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "aviso: %v\n", err)
					}
				}
				return runErr
			}
			if storeErr == nil {
				if err := store.Put(state.TaskRecord{Source: run.Task.Source, ID: run.Task.ExternalID, Status: "succeeded", RunID: run.ID, ContentHash: run.Task.ContentHash}); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "aviso: %v\n", err)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "run %s concluída\n", res.RunID)
			fmt.Fprintf(cmd.OutOrStdout(), "branch: %s\n", res.Branch)
			if res.PRURL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "PR: %s\n", res.PRURL)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&auto, "auto", false, "aprova gates automaticamente")
	cmd.Flags().Float64Var(&budget, "budget", 0, "orçamento máximo em USD")
	return cmd
}
