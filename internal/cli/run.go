package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pause"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/runner"
	"github.com/donwellus/aiw/internal/state"
)

func newRunCommand(opts *rootOptions) *cobra.Command {
	var taskArg string
	var sourceName string
	var repoFlag string
	var engineName string
	var budget float64
	var auto bool
	var force bool
	var createPR bool
	var noWriteBack bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Executa o pipeline para uma tarefa",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			if pause.Active(cfg.DataDir()) {
				return errors.New("pausa global ativa; use aiw resume --all")
			}
			p := pipeline.New(cfg)
			defer closeTelemetry(p)
			if engineName != "" {
				if _, err := p.Engines.Get(engineName); err != nil {
					return err
				}
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			_, ref, task, err := resolveTask(ctx, cfg, sourceName, taskArg)
			if err != nil {
				return err
			}
			repo := resolveRepo(cfg, task, repoFlag)
			if repo == "" {
				return errors.New("repositório não resolvido: use --repo, frontmatter repo:, label repo:<nome> ou workspace.default_repo")
			}
			store, err := state.Open(cfg.StateDir())
			if err != nil {
				return err
			}

			run := runner.New(cfg, store, p)
			outcome := run.Execute(ctx, runner.Request{
				Task:  task,
				Ref:   ref,
				Repo:  repo,
				Force: force,
				Options: pipeline.Options{
					AutoApprove:      auto,
					EngineOverride:   engineName,
					BudgetUSD:        budget,
					CreatePR:         createPR,
					DisableWriteBack: noWriteBack,
				},
			})

			output := cmd.OutOrStdout()
			switch outcome.Status {
			case "skipped":
				fmt.Fprintf(output, "tarefa %s ignorada: %s\n", outcome.TaskID, outcome.Reason)
				return nil
			case "busy":
				return fmt.Errorf("tarefa %s está em execução em outro processo", outcome.TaskID)
			case "paused":
				fmt.Fprintf(output, "run %s pausada aguardando aprovação\n", outcome.RunID)
				fmt.Fprintf(output, "artefatos: %s\n", outcome.RunDir)
				if outcome.Reason != "" {
					fmt.Fprintf(output, "%s\n", outcome.Reason)
				}
				fmt.Fprintf(output, "aprove com: aiw approve %s <intake|spec|plan>\n", outcome.RunID)
				fmt.Fprintf(output, "depois rode: aiw resume %s\n", outcome.RunID)
				return nil
			case "needs_input":
				runDir := outcome.RunDir
				if runDir == "" {
					runDir = filepath.Join(cfg.RunsDir(), outcome.RunID)
				}
				fmt.Fprintf(output, "run %s aguardando input\n", outcome.RunID)
				fmt.Fprintf(output, "perguntas: %s\n", filepath.Join(runDir, "questions.md"))
				fmt.Fprintf(output, "intake: %s\n", filepath.Join(runDir, "intake.md"))
				if outcome.Reason != "" {
					fmt.Fprintf(output, "%s\n", outcome.Reason)
				}
				fmt.Fprintf(output, "edite a tarefa na fonte e execute novamente\n")
				return nil
			case "canceled":
				fmt.Fprintf(output, "run %s cancelada\n", outcome.RunID)
				return fmt.Errorf("execução cancelada")
			case "failed":
				if outcome.Err != nil {
					return outcome.Err
				}
				return fmt.Errorf("run %s falhou", outcome.RunID)
			}

			fmt.Fprintf(output, "run %s concluída\n", outcome.RunID)
			fmt.Fprintf(output, "branch: %s\n", outcome.Branch)
			fmt.Fprintf(output, "worktree: %s\n", outcome.Worktree)
			fmt.Fprintf(output, "artefatos: %s\n", outcome.RunDir)
			fmt.Fprintf(output, "custo: US$ %.4f (%d in / %d out tokens)\n", outcome.CostUSD, outcome.InputTokens, outcome.OutputTokens)
			fmt.Fprintf(output, "duração: %s\n", formatDuration(outcome.Duration))
			if outcome.PRURL != "" {
				fmt.Fprintf(output, "PR: %s\n", outcome.PRURL)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&taskArg, "task", "", "tarefa: caminho de arquivo, source:id ou id")
	cmd.Flags().StringVar(&sourceName, "source", "", "fonte de tarefas")
	cmd.Flags().StringVar(&repoFlag, "repo", "", "repositório alvo")
	cmd.Flags().StringVar(&engineName, "engine", "", "engine para todos os estágios")
	cmd.Flags().Float64Var(&budget, "budget", 0, "orçamento máximo em USD (sobrepõe o config)")
	cmd.Flags().BoolVar(&auto, "auto", false, "aprova gates automaticamente")
	cmd.Flags().BoolVar(&force, "force", false, "ignora deduplicação de tarefas concluídas/pausadas")
	cmd.Flags().BoolVar(&createPR, "pr", false, "faz push e cria PR com gh")
	cmd.Flags().BoolVar(&noWriteBack, "no-write-back", false, "não atualiza o estado na fonte")
	return cmd
}

func formatDuration(value time.Duration) string {
	if value < time.Second {
		return value.Round(time.Millisecond).String()
	}
	return value.Round(time.Second).String()
}
