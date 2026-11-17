package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
			store, err := state.Open(cfg.StateDir())
			if err != nil {
				return err
			}
			claim, acquired, err := store.Claim(run.Task.Source, run.Task.ExternalID, run.ID, cfg.Defaults.ClaimTTL.Std())
			if err != nil {
				return err
			}
			if !acquired {
				return fmt.Errorf("tarefa %s já está em execução em outro processo; libere com 'aiw release %s' se necessário", run.Task.ExternalID, run.ID)
			}
			defer func() {
				if err := claim.Release(); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "aviso: %v\n", err)
				}
			}()

			p := pipeline.New(cfg)
			defer closeTelemetry(p)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			res, runErr := p.Resume(ctx, args[0], pipeline.Options{
				AutoApprove: auto,
				BudgetUSD:   budget,
			})

			if runErr != nil && errors.Is(runErr, pipeline.ErrPaused) {
				if err := store.Put(state.TaskRecord{Source: run.Task.Source, ID: run.Task.ExternalID, Status: "paused", RunID: run.ID, ContentHash: run.Task.ContentHash}); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "aviso: %v\n", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "run %s continua pausada\n", res.RunID)
				fmt.Fprintf(cmd.OutOrStdout(), "aprove com: aiw approve %s <spec|plan>\n", res.RunID)
				return nil
			}
			if runErr != nil {
				status := "failed"
				if errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
					status = "todo"
				}
				if err := store.Put(state.TaskRecord{Source: run.Task.Source, ID: run.Task.ExternalID, Status: status, RunID: run.ID, ContentHash: run.Task.ContentHash}); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "aviso: %v\n", err)
				}
				return runErr
			}
			if err := store.Put(state.TaskRecord{Source: run.Task.Source, ID: run.Task.ExternalID, Status: "succeeded", RunID: run.ID, ContentHash: run.Task.ContentHash}); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "aviso: %v\n", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "run %s concluída\n", res.RunID)
			fmt.Fprintf(cmd.OutOrStdout(), "branch: %s\n", res.Branch)
			fmt.Fprintf(cmd.OutOrStdout(), "custo: US$ %.4f\n", res.Usage.CostUSD)
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
