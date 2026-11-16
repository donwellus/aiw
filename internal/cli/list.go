package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
)

func newListCommand(opts *rootOptions) *cobra.Command {
	var sourceName string
	var eligibleOnly bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Lista tarefas da fonte",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			src, filter, err := openSource(cfg, sourceName)
			if err != nil {
				return err
			}
			store, err := state.Open(cfg.StateDir())
			if err != nil {
				return err
			}
			ctx := context.Background()
			refs, err := src.List(ctx, filter)
			if err != nil {
				return err
			}
			tasks := make([]*source.Task, 0, len(refs))
			for _, ref := range refs {
				task, err := src.Fetch(ctx, ref)
				if err != nil {
					return err
				}
				eligible := true
				if rec, ok := store.Get(src.Name(), task.ExternalID); ok {
					if rec.Status == "succeeded" && rec.ContentHash == task.ContentHash {
						eligible = false
					}
				}
				if eligibleOnly && !eligible {
					continue
				}
				tasks = append(tasks, task)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				encoder := json.NewEncoder(out)
				encoder.SetIndent("", "  ")
				return encoder.Encode(tasks)
			}
			writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "ID\tPRIORIDADE\tLABELS\tTÍTULO")
			for _, task := range tasks {
				labels := ""
				for i, label := range task.Labels {
					if i > 0 {
						labels += ","
					}
					labels += label
				}
				fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", task.ExternalID, task.Priority, labels, task.Title)
			}
			writer.Flush()
			fmt.Fprintf(out, "\n%d tarefa(s) na fonte %s\n", len(tasks), src.Name())
			return nil
		},
	}
	cmd.Flags().StringVar(&sourceName, "source", "", "fonte de tarefas")
	cmd.Flags().BoolVar(&eligibleOnly, "eligible", false, "mostra apenas tarefas ainda não concluídas com o mesmo conteúdo")
	cmd.Flags().BoolVar(&asJSON, "json", false, "saída em JSON")
	return cmd
}
