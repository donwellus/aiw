package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/runner"
	"github.com/donwellus/aiw/internal/state"
)

func newResumeCommand(opts *rootOptions) *cobra.Command {
	var auto bool
	var budget float64
	cmd := &cobra.Command{
		Use:   "resume <run-id>",
		Short: "Retoma uma execução pausada, cancelada ou falha",
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
			p := pipeline.New(cfg)
			defer closeTelemetry(p)
			run := runner.New(cfg, store, p)
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			outcome, runErr := run.Resume(ctx, args[0], pipeline.Options{
				AutoApprove: auto,
				BudgetUSD:   budget,
			})

			out := cmd.OutOrStdout()
			switch outcome.Status {
			case "busy":
				return fmt.Errorf("tarefa %s está em execução em outro processo", outcome.TaskID)
			case "skipped":
				fmt.Fprintf(out, "run %s ignorada: %s\n", outcome.RunID, outcome.Reason)
				return nil
			case "paused":
				fmt.Fprintf(out, "run %s pausada\n", outcome.RunID)
				fmt.Fprintf(out, "artefatos: %s\n", outcome.RunDir)
				if outcome.Reason != "" {
					fmt.Fprintf(out, "%s\n", outcome.Reason)
				}
				fmt.Fprintf(out, "aprove gates com: aiw approve %s <intake|spec|plan>\n", outcome.RunID)
				fmt.Fprintf(out, "continue com: aiw resume %s\n", outcome.RunID)
				return nil
			case "needs_input":
				fmt.Fprintf(out, "run %s aguarda input; edite a tarefa na fonte e execute novamente\n", outcome.RunID)
				return nil
			case "canceled":
				fmt.Fprintf(out, "run %s cancelada; use aiw resume para continuar\n", outcome.RunID)
				return fmt.Errorf("execução cancelada")
			case "failed":
				if runErr != nil {
					return runErr
				}
				return fmt.Errorf("run %s falhou", outcome.RunID)
			}
			fmt.Fprintf(out, "run %s concluída\n", outcome.RunID)
			fmt.Fprintf(out, "branch: %s\n", outcome.Branch)
			fmt.Fprintf(out, "custo: US$ %.4f\n", outcome.CostUSD)
			if outcome.PRURL != "" {
				fmt.Fprintf(out, "PR: %s\n", outcome.PRURL)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&auto, "auto", false, "aprova gates automaticamente")
	cmd.Flags().Float64Var(&budget, "budget", 0, "orçamento máximo em USD")
	return cmd
}
