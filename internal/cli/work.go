package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pause"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/runner"
	"github.com/donwellus/aiw/internal/state"
)

type workJSON struct {
	TaskID       string  `json:"task_id"`
	Title        string  `json:"title,omitempty"`
	Status       string  `json:"status"`
	RunID        string  `json:"run_id,omitempty"`
	Stage        string  `json:"stage,omitempty"`
	Branch       string  `json:"branch,omitempty"`
	Worktree     string  `json:"worktree,omitempty"`
	RunDir       string  `json:"run_dir,omitempty"`
	PRURL        string  `json:"pr_url,omitempty"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	DurationMS   int64   `json:"duration_ms"`
	Error        string  `json:"error,omitempty"`
	Reason       string  `json:"reason,omitempty"`
}

func newWorkCommand(opts *rootOptions) *cobra.Command {
	var workers int
	var sourceName string
	var repoFlag string
	var engineName string
	var budget float64
	var auto bool
	var force bool
	var createPR bool
	var noWriteBack bool
	var maxTasks int
	var asJSON bool
	var dryRun bool
	var drainTimeout time.Duration
	var pausePoll time.Duration

	cmd := &cobra.Command{
		Use:   "work",
		Short: "Processa tarefas elegíveis em paralelo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			if workers == 0 {
				workers = cfg.Defaults.Workers
			}
			if workers < 1 {
				return errors.New("--workers precisa ser >= 1")
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
			store, err := state.Open(cfg.StateDir())
			if err != nil {
				return err
			}
			run := runner.New(cfg, store, p)

			drain, ctx := newDrainState(drainTimeout)
			defer drain.Close()
			stopWatch := pause.Watch(ctx, cfg.DataDir(), pausePoll, drain.Pause)
			defer stopWatch()

			src, filter, err := openSource(cfg, sourceName)
			if err != nil {
				return err
			}
			candidates, skips, err := run.Collect(ctx, src, filter, repoFlag, force, maxTasks)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			failures := 0
			canceled := 0
			for _, skip := range skips {
				if skip.Status == "failed" {
					failures++
				}
				printWorkOutcome(out, asJSON, skip)
			}
			jobs := make([]runner.Job, 0, len(candidates))
			for _, candidate := range candidates {
				jobs = append(jobs, runner.Job{
					Task:  candidate.Task,
					Ref:   candidate.Ref,
					Repo:  candidate.Repo,
					Force: force,
					Options: pipeline.Options{
						AutoApprove:      auto,
						EngineOverride:   engineName,
						BudgetUSD:        budget,
						CreatePR:         createPR,
						DisableWriteBack: noWriteBack,
					},
				})
			}

			if dryRun {
				for _, job := range jobs {
					fmt.Fprintf(out, "[%s] pronta repo=%s\n", job.Task.ExternalID, job.Repo)
				}
				fmt.Fprintf(out, "\n%d tarefa(s) pronta(s) para %d worker(s)\n", len(jobs), workers)
				return nil
			}
			if len(jobs) == 0 {
				if failures > 0 {
					return fmt.Errorf("%d tarefa(s) falharam", failures)
				}
				fmt.Fprintln(out, "nenhuma tarefa elegível")
				return nil
			}
			if err := run.MarkQueued(candidates); err != nil {
				return fmt.Errorf("registrando fila: %w", err)
			}

			outcomes := run.Work(ctx, jobs, workers, drain.drain, func(outcome runner.Outcome) {
				switch outcome.Status {
				case "failed":
					failures++
				case "canceled":
					canceled++
				}
				printWorkOutcome(out, asJSON, outcome)
			})

			notStarted := len(jobs) - len(outcomes)
			if drain.Draining() && ctx.Err() == nil {
				if pause.Active(cfg.DataDir()) {
					fmt.Fprintf(cmd.ErrOrStderr(), "pausa global ativa; %d tarefa(s) não iniciada(s)\n", notStarted)
				} else if notStarted > 0 {
					fmt.Fprintf(cmd.ErrOrStderr(), "drenado; %d tarefa(s) não iniciada(s)\n", notStarted)
				}
				if failures > 0 {
					return fmt.Errorf("%d tarefa(s) falharam", failures)
				}
				return nil
			}
			if ctx.Err() != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "drenagem forçada; %d tarefa(s) não iniciada(s)\n", notStarted)
				return errors.New("drenagem forçada")
			}
			if failures > 0 {
				return fmt.Errorf("%d tarefa(s) falharam", failures)
			}
			if canceled > 0 {
				return errors.New("processamento cancelado")
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&workers, "workers", 0, "número de tarefas em paralelo (default do config)")
	cmd.Flags().StringVar(&sourceName, "source", "", "fonte de tarefas")
	cmd.Flags().StringVar(&repoFlag, "repo", "", "repositório alvo (sobrepõe a tarefa)")
	cmd.Flags().StringVar(&engineName, "engine", "", "engine para todos os estágios")
	cmd.Flags().Float64Var(&budget, "budget", 0, "orçamento por tarefa em USD")
	cmd.Flags().BoolVar(&auto, "auto", false, "aprova gates automaticamente (sobrepõe política/humano)")
	cmd.Flags().BoolVar(&force, "force", false, "ignora deduplicação de tarefas concluídas/pausadas")
	cmd.Flags().BoolVar(&createPR, "pr", false, "faz push e cria PR com gh")
	cmd.Flags().BoolVar(&noWriteBack, "no-write-back", false, "não atualiza o estado na fonte")
	cmd.Flags().IntVar(&maxTasks, "max-tasks", 0, "máximo de tarefas a processar nesta execução")
	cmd.Flags().BoolVar(&asJSON, "json", false, "saída JSONL por tarefa")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "apenas lista as tarefas elegíveis")
	cmd.Flags().DurationVar(&drainTimeout, "drain-timeout", 5*time.Minute, "tempo máximo para drenar runs em andamento após SIGINT")
	cmd.Flags().DurationVar(&pausePoll, "pause-poll", 10*time.Second, "intervalo de checagem da pausa global")
	return cmd
}

func printWorkOutcome(out io.Writer, asJSON bool, outcome runner.Outcome) {
	if asJSON {
		payload := workJSON{
			TaskID:       outcome.TaskID,
			Title:        outcome.Title,
			Status:       outcome.Status,
			RunID:        outcome.RunID,
			Stage:        outcome.Stage,
			Branch:       outcome.Branch,
			Worktree:     outcome.Worktree,
			RunDir:       outcome.RunDir,
			PRURL:        outcome.PRURL,
			CostUSD:      outcome.CostUSD,
			InputTokens:  outcome.InputTokens,
			OutputTokens: outcome.OutputTokens,
			DurationMS:   outcome.Duration.Milliseconds(),
			Reason:       outcome.Reason,
		}
		if outcome.Err != nil {
			payload.Error = oneLine(outcome.Err.Error(), 500)
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		fmt.Fprintf(out, "%s\n", data)
		return
	}

	switch outcome.Status {
	case "succeeded":
		fmt.Fprintf(out, "[%s] ok run=%s branch=%s custo=US$%.4f duracao=%s\n",
			outcome.TaskID, outcome.RunID, outcome.Branch, outcome.CostUSD, formatDuration(outcome.Duration))
	case "failed":
		message := ""
		if outcome.Err != nil {
			message = oneLine(outcome.Err.Error(), 300)
		}
		fmt.Fprintf(out, "[%s] falhou stage=%s run=%s erro=%s\n", outcome.TaskID, outcome.Stage, outcome.RunID, message)
	case "paused":
		fmt.Fprintf(out, "[%s] pausada run=%s artefatos=%s\n", outcome.TaskID, outcome.RunID, outcome.RunDir)
	case "needs_input":
		fmt.Fprintf(out, "[%s] needs_input run=%s perguntas=%s\n", outcome.TaskID, outcome.RunID, filepath.Join(outcome.RunDir, "questions.md"))
	case "canceled":
		fmt.Fprintf(out, "[%s] cancelada run=%s\n", outcome.TaskID, outcome.RunID)
	case "pr_open":
		fmt.Fprintf(out, "[%s] PR aberto %s\n", outcome.TaskID, outcome.PRURL)
	case "skipped", "busy":
		fmt.Fprintf(out, "[%s] ignorada: %s\n", outcome.TaskID, outcome.Reason)
	default:
		fmt.Fprintf(out, "[%s] %s\n", outcome.TaskID, outcome.Status)
	}
}

func oneLine(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > max {
		return value[:max] + "..."
	}
	return value
}
