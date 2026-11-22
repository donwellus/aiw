package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/clean"
	"github.com/donwellus/aiw/internal/state"
)

func newCleanCommand(opts *rootOptions) *cobra.Command {
	var dryRun bool
	var force bool
	var keepBranches bool
	var skipSuperseded bool
	var asJSON bool
	var statusValue string
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove worktrees e branches locais de tarefas concluídas",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			statuses, err := clean.ParseStatuses(statusValue)
			if err != nil {
				return err
			}
			store, err := state.Open(cfg.StateDir())
			if err != nil {
				return err
			}
			result, err := clean.Run(context.Background(), cfg, store, clean.Options{
				Statuses:       statuses,
				DryRun:         dryRun,
				Force:          force,
				KeepBranches:   keepBranches,
				SkipSuperseded: skipSuperseded,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				encoder := json.NewEncoder(out)
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(result); err != nil {
					return err
				}
			} else {
				renderCleanTable(out, result)
			}
			if result.Summary.Errors > 0 {
				return fmt.Errorf("%d erro(s) na limpeza", result.Summary.Errors)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "mostra o que seria removido sem alterar nada")
	cmd.Flags().BoolVar(&force, "force", false, "remove worktrees sujos e branches não merged")
	cmd.Flags().BoolVar(&keepBranches, "keep-branches", false, "remove apenas worktrees, preservando branches")
	cmd.Flags().BoolVar(&skipSuperseded, "skip-superseded", false, "não limpa runs needs_input/paused já superadas por uma run terminal mais nova")
	cmd.Flags().BoolVar(&asJSON, "json", false, "saída JSON")
	cmd.Flags().StringVar(&statusValue, "status", "succeeded,failed,canceled", "status terminais a limpar (separados por vírgula)")
	return cmd
}

func renderCleanTable(out io.Writer, result *clean.Result) {
	if result.DryRun {
		fmt.Fprintln(out, "Limpeza (dry-run; nada será alterado)")
	} else {
		fmt.Fprintln(out, "Limpeza de worktrees e branches")
	}
	if len(result.Actions) == 0 {
		fmt.Fprintln(out, "nenhuma run terminal selecionada")
		return
	}
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "RUN\tSTATUS\tSUPERADA POR\tWORKTREE\tBRANCH\tDETALHE")
	for _, action := range result.Actions {
		detail := strings.TrimSpace(action.Reason)
		if action.Error != "" {
			detail = strings.TrimSpace(detail + "; erro: " + action.Error)
		}
		supersededBy := action.SupersededBy
		if supersededBy == "" {
			supersededBy = "-"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
			action.RunID,
			action.Status,
			supersededBy,
			action.WorktreeAction,
			action.BranchAction,
			oneLine(detail, 120),
		)
	}
	writer.Flush()
	fmt.Fprintln(out)
	fmt.Fprintf(out, "runs selecionadas: %d\n", result.Summary.Runs)
	fmt.Fprintf(out, "runs superadas: %d\n", result.Summary.Superseded)
	worktreeLabel := "worktrees removidos"
	branchLabel := "branches apagadas"
	if result.DryRun {
		worktreeLabel = "worktrees a remover"
		branchLabel = "branches a apagar"
	}
	fmt.Fprintf(out, "%s: %d\n", worktreeLabel, result.Summary.WorktreesRemoved)
	fmt.Fprintf(out, "%s: %d\n", branchLabel, result.Summary.BranchesDeleted)
	fmt.Fprintf(out, "pulados: worktrees %d, branches %d\n", result.Summary.WorktreesSkipped, result.Summary.BranchesSkipped)
	fmt.Fprintf(out, "erros: %d\n", result.Summary.Errors)
	for _, err := range result.Errors {
		fmt.Fprintf(out, "  prune: %s\n", oneLine(err, 160))
	}
}
