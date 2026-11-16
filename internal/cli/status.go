package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
)

func newStatusCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status <run-id>",
		Short: "Mostra o estado de uma execução",
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
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "run:      %s\n", run.ID)
			fmt.Fprintf(out, "status:   %s\n", run.Status)
			fmt.Fprintf(out, "estágio:  %s\n", run.Stage)
			fmt.Fprintf(out, "tarefa:   %s (%s)\n", run.Task.Title, run.Task.ExternalID)
			fmt.Fprintf(out, "repo:     %s\n", run.Repo)
			fmt.Fprintf(out, "branch:   %s\n", run.Branch)
			fmt.Fprintf(out, "worktree: %s\n", run.Worktree)
			fmt.Fprintf(out, "custo:    US$ %.2f (%d in / %d out tokens)\n", run.Usage.CostUSD, run.Usage.InputTokens, run.Usage.OutputTokens)
			if run.PRURL != "" {
				fmt.Fprintf(out, "PR:       %s\n", run.PRURL)
			}
			if run.Error != "" {
				fmt.Fprintf(out, "erro:     %s\n", run.Error)
			}
			fmt.Fprintf(out, "artefatos: %s\n", filepath.Join(cfg.RunsDir(), run.ID))
			fmt.Fprintf(out, "gates:    spec=%s plan=%s\n", run.Gates["spec"], run.Gates["plan"])

			fmt.Fprintln(out)
			writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "ESTÁGIO\tSTATUS\tENGINE\tRODADAS\tCUSTO")
			for _, stage := range run.Stages {
				fmt.Fprintf(writer, "%s\t%s\t%s\t%d\tUS$ %.2f\n", stage.Name, stage.Status, stage.Engine, stage.Rounds, stage.CostUSD)
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
