package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/eval"
)

func newEvalCommand(opts *rootOptions) *cobra.Command {
	var suitePath string
	var engineName string
	var budget float64
	var outDir string
	var keep bool
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Executa uma suite de tarefas golden",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			suite, err := eval.Load(suitePath)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			results, summary, runErr := eval.Run(ctx, suite, eval.Options{
				Base:      cfg,
				Engine:    engineName,
				BudgetUSD: budget,
				Keep:      keep,
				OutDir:    outDir,
			})
			out := cmd.OutOrStdout()
			for _, result := range results {
				if asJSON {
					data, err := json.Marshal(result)
					if err != nil {
						return err
					}
					fmt.Fprintf(out, "%s\n", data)
					continue
				}
				extras := ""
				if result.JudgeScore != nil {
					extras += fmt.Sprintf(" judge=%.1f", *result.JudgeScore)
				}
				if result.ReferenceSimilarity != nil {
					extras += fmt.Sprintf(" ref=%.2f", *result.ReferenceSimilarity)
				}
				if result.Status == "passed" {
					fmt.Fprintf(out, "[%s] ok pipeline=%s verify=%d custo=US$%.4f duracao=%s%s\n",
						result.CaseID, result.PipelineStatus, result.VerifyExit, result.CostUSD, formatDuration(time.Duration(result.DurationMS)*time.Millisecond), extras)
				} else {
					fmt.Fprintf(out, "[%s] falhou pipeline=%s stage=%s verify=%d custo=US$%.4f erro=%s%s\n",
						result.CaseID, result.PipelineStatus, result.Stage, result.VerifyExit, result.CostUSD, oneLine(result.Error, 300), extras)
				}
			}
			summaryOut := out
			if asJSON {
				summaryOut = cmd.ErrOrStderr()
			}
			fmt.Fprintf(summaryOut, "[%s] %d/%d ok custo=US$%.4f duracao=%s resultados=%s\n",
				summary.Suite, summary.Passed, summary.Total, summary.CostUSD, formatDuration(time.Duration(summary.DurationMS)*time.Millisecond), summary.OutDir)
			if summary.WorkRoot != "" {
				fmt.Fprintf(summaryOut, "workdir preservado: %s\n", summary.WorkRoot)
			}
			if runErr != nil {
				return runErr
			}
			if summary.Failed > 0 {
				return fmt.Errorf("%d caso(s) falharam", summary.Failed)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&suitePath, "suite", "", "caminho da suite YAML")
	cmd.Flags().StringVar(&engineName, "engine", "", "engine para todos os estágios (sobrepõe a suite)")
	cmd.Flags().Float64Var(&budget, "budget", 0, "orçamento por caso em USD")
	cmd.Flags().StringVar(&outDir, "out", "", "diretório de resultados (default .aiw/evals/<timestamp>)")
	cmd.Flags().BoolVar(&keep, "keep", false, "mantém worktrees e repositórios temporários")
	cmd.Flags().BoolVar(&asJSON, "json", false, "saída JSONL por caso")
	_ = cmd.MarkFlagRequired("suite")
	return cmd
}
