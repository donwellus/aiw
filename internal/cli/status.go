package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/state"
)

func newStatusCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status [run-id]",
		Short: "Mostra execuções em andamento ou o estado de uma execução",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			store, err := state.Open(cfg.StateDir())
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return showActive(cmd, cfg, store)
			}
			run, err := pipeline.LoadRun(cfg, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "run:      %s\n", run.ID)
			fmt.Fprintf(out, "status:   %s\n", run.Status)
			fmt.Fprintf(out, "estágio:  %s\n", run.Stage)
			fmt.Fprintf(out, "tarefa:   %s (%s)\n", run.Task.Title, run.Task.ExternalID)
			fmt.Fprintf(out, "repo:     %s\n", run.Repo)
			fmt.Fprintf(out, "branch:   %s\n", run.Branch)
			fmt.Fprintf(out, "worktree: %s\n", run.Worktree)
			fmt.Fprintf(out, "custo:    US$ %.4f (%d in / %d out tokens)\n", run.Usage.CostUSD, run.Usage.InputTokens, run.Usage.OutputTokens)
			if run.PRURL != "" {
				fmt.Fprintf(out, "PR:       %s\n", run.PRURL)
			}
			if run.Error != "" {
				fmt.Fprintf(out, "erro:     %s\n", run.Error)
			}
			fmt.Fprintf(out, "artefatos: %s\n", filepath.Join(cfg.RunsDir(), run.ID))
			fmt.Fprintf(out, "gates:    intake=%s spec=%s plan=%s\n", run.Gates["intake"], run.Gates["spec"], run.Gates["plan"])

			fmt.Fprintln(out)
			writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "ESTÁGIO\tSTATUS\tENGINE\tRODADAS\tCUSTO")
			for _, stage := range run.Stages {
				fmt.Fprintf(writer, "%s\t%s\t%s\t%d\tUS$ %.4f\n", stage.Name, stage.Status, stage.Engine, stage.Rounds, stage.CostUSD)
			}
			writer.Flush()

			eventsPath := filepath.Join(cfg.RunsDir(), run.ID, "events.jsonl")
			if data, err := os.ReadFile(eventsPath); err == nil {
				lines := strings.Split(strings.TrimSpace(string(data)), "\n")
				start := 0
				if len(lines) > 12 {
					start = len(lines) - 12
				}
				fmt.Fprintln(out, "\núltimos eventos:")
				for _, line := range lines[start:] {
					fmt.Fprintf(out, "  %s\n", line)
				}
			}
			return nil
		},
	}
}

func showActive(cmd *cobra.Command, cfg *config.Config, store *state.Store) error {
	records := state.LatestByID(store.All())
	active := make([]state.TaskRecord, 0)
	for _, record := range records {
		switch record.Status {
		case "running", "queued", "paused", "needs_input":
			active = append(active, record)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].UpdatedAt.After(active[j].UpdatedAt) })
	out := cmd.OutOrStdout()
	if len(active) == 0 {
		fmt.Fprintln(out, "nenhuma tarefa em andamento (running/queued/paused/needs_input)")
		return nil
	}
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "TAREFA\tSTATUS\tESTÁGIO\tRUN\tCUSTO")
	for _, record := range active {
		status := record.Status
		stage := "-"
		cost := 0.0
		if record.RunID != "" {
			if run, err := pipeline.LoadRun(cfg, record.RunID); err == nil {
				status = run.Status
				stage = run.Stage
				cost = run.Usage.CostUSD
			}
		}
		runID := record.RunID
		if runID == "" {
			runID = "-"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\tUS$ %.4f\n", record.ID, status, stage, runID, cost)
	}
	writer.Flush()
	fmt.Fprintf(out, "\n%d tarefa(s) em andamento\n", len(active))
	return nil
}
