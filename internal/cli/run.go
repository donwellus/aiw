package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
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
			p := pipeline.New(cfg)
			defer closeTelemetry(p)
			if engineName != "" {
				if _, err := p.Engines.Get(engineName); err != nil {
					return err
				}
			}

			ctx := context.Background()
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
			previous, found := store.Get(task.Source, task.ExternalID)
			if found && !force {
				switch previous.Status {
				case "succeeded":
					if previous.ContentHash == task.ContentHash {
						fmt.Fprintf(cmd.OutOrStdout(), "tarefa %s já concluída com o mesmo conteúdo (run %s); use --force para repetir\n", task.ExternalID, previous.RunID)
						return nil
					}
				case "paused":
					if previous.ContentHash == task.ContentHash {
						return fmt.Errorf("tarefa pausada na run %s; use 'aiw resume %s' ou --force", previous.RunID, previous.RunID)
					}
				case "running":
					if time.Since(previous.UpdatedAt) < 2*time.Hour {
						return fmt.Errorf("tarefa já em execução na run %s; use --force se a execução morreu", previous.RunID)
					}
				}
			}

			if err := store.Put(state.TaskRecord{
				Source:      task.Source,
				ID:          task.ExternalID,
				Status:      "running",
				ContentHash: task.ContentHash,
			}); err != nil {
				return err
			}

			p.AutoApprove = auto
			res, runErr := p.Run(ctx, task, ref, repo, pipeline.Options{
				AutoApprove:      auto,
				EngineOverride:   engineName,
				BudgetUSD:        budget,
				CreatePR:         createPR,
				DisableWriteBack: noWriteBack,
			})

			if runErr != nil && errors.Is(runErr, pipeline.ErrPaused) {
				if err := store.Put(state.TaskRecord{
					Source:      task.Source,
					ID:          task.ExternalID,
					Status:      "paused",
					RunID:       res.RunID,
					ContentHash: task.ContentHash,
				}); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "aviso: %v\n", err)
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "run %s pausada aguardando aprovação\n", res.RunID)
				fmt.Fprintf(out, "artefatos: %s\n", res.RunDir)
				fmt.Fprintf(out, "aprove com: aiw approve %s <spec|plan>\n", res.RunID)
				fmt.Fprintf(out, "depois rode: aiw resume %s\n", res.RunID)
				return nil
			}
			if runErr != nil {
				if err := store.Put(state.TaskRecord{
					Source:      task.Source,
					ID:          task.ExternalID,
					Status:      "failed",
					RunID:       res.RunID,
					ContentHash: task.ContentHash,
				}); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "aviso: %v\n", err)
				}
				return runErr
			}

			if err := store.Put(state.TaskRecord{
				Source:      task.Source,
				ID:          task.ExternalID,
				Status:      "succeeded",
				RunID:       res.RunID,
				ContentHash: task.ContentHash,
			}); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "run %s concluída\n", res.RunID)
			fmt.Fprintf(out, "branch: %s\n", res.Branch)
			fmt.Fprintf(out, "worktree: %s\n", res.Worktree)
			fmt.Fprintf(out, "artefatos: %s\n", res.RunDir)
			if res.PRURL != "" {
				fmt.Fprintf(out, "PR: %s\n", res.PRURL)
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
	cmd.Flags().BoolVar(&force, "force", false, "ignora deduplicação e locks")
	cmd.Flags().BoolVar(&createPR, "pr", false, "faz push e cria PR com gh")
	cmd.Flags().BoolVar(&noWriteBack, "no-write-back", false, "não atualiza o estado na fonte")
	return cmd
}
