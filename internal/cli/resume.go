package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pause"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/runner"
	"github.com/donwellus/aiw/internal/state"
)

func newResumeCommand(opts *rootOptions) *cobra.Command {
	var auto bool
	var budget float64
	var all bool
	cmd := &cobra.Command{
		Use:   "resume [run-id]",
		Short: "Retoma uma execução ou, com --all, limpa a pausa global e retoma as pausadas/canceladas",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			if all && len(args) > 0 {
				return errors.New("use --all sem run-id")
			}
			if !all && len(args) == 0 {
				return errors.New("informe um run-id ou use --all")
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			store, err := state.Open(cfg.StateDir())
			if err != nil {
				return err
			}
			p := pipeline.New(cfg)
			defer closeTelemetry(p)
			run := runner.New(cfg, store, p)
			out := cmd.OutOrStdout()

			if all {
				if err := pause.Clear(cfg.DataDir()); err != nil {
					return err
				}
				fmt.Fprintln(out, "pausa global removida")
				candidates := resumeCandidates(store)
				if len(candidates) == 0 {
					fmt.Fprintln(out, "nenhuma run pausada/cancelada para retomar")
					return nil
				}
				failures := 0
				for _, runID := range candidates {
					outcome, runErr := run.Resume(ctx, runID, pipeline.Options{
						AutoApprove: auto,
						BudgetUSD:   budget,
					})
					switch {
					case runErr == nil && outcome.Status == "succeeded":
						fmt.Fprintf(out, "[%s] ok run=%s\n", outcome.TaskID, outcome.RunID)
					case errors.Is(runErr, pipeline.ErrPaused) || errors.Is(runErr, pipeline.ErrPauseRequested):
						fmt.Fprintf(out, "[%s] pausada run=%s\n", outcome.TaskID, outcome.RunID)
					case errors.Is(runErr, pipeline.ErrNeedsInput):
						fmt.Fprintf(out, "[%s] needs_input run=%s\n", outcome.TaskID, outcome.RunID)
					case errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
						fmt.Fprintf(out, "[%s] cancelada run=%s\n", outcome.TaskID, outcome.RunID)
					case runErr != nil:
						failures++
						fmt.Fprintf(out, "[%s] falhou run=%s erro=%s\n", outcome.TaskID, outcome.RunID, oneLine(runErr.Error(), 300))
					default:
						fmt.Fprintf(out, "[%s] %s run=%s\n", outcome.TaskID, outcome.Status, outcome.RunID)
					}
					if ctx.Err() != nil {
						break
					}
				}
				if failures > 0 {
					return fmt.Errorf("%d run(s) falharam no resume --all", failures)
				}
				return nil
			}

			if pause.Active(cfg.DataDir()) {
				return errors.New("pausa global ativa; use aiw resume --all")
			}
			outcome, runErr := run.Resume(ctx, args[0], pipeline.Options{
				AutoApprove: auto,
				BudgetUSD:   budget,
			})
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
	cmd.Flags().BoolVar(&all, "all", false, "limpa a pausa global e retoma runs paused/canceled")
	return cmd
}

func resumeCandidates(store *state.Store) []string {
	seen := map[string]bool{}
	var runs []string
	for _, record := range state.LatestByID(store.All()) {
		if record.RunID == "" {
			continue
		}
		if record.Status != "paused" && record.Status != "todo" {
			continue
		}
		if seen[record.RunID] {
			continue
		}
		seen[record.RunID] = true
		runs = append(runs, record.RunID)
	}
	return runs
}
