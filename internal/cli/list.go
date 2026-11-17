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

type listEntry struct {
	*source.Task
	Status string `json:"status"`
}

func newListCommand(opts *rootOptions) *cobra.Command {
	var sourceName string
	var eligibleOnly bool
	var asJSON bool
	var statusFilter string
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
			entries := make([]listEntry, 0, len(refs))
			for _, ref := range refs {
				task, err := src.Fetch(ctx, ref)
				if err != nil {
					return err
				}
				status := "todo"
				eligible := true
				if record, ok := store.Get(src.Name(), task.ExternalID); ok {
					status = record.Status
					switch record.Status {
					case "succeeded":
						eligible = record.ContentHash != task.ContentHash
					case "running", "paused":
						eligible = false
					}
				}
				if eligibleOnly && !eligible {
					continue
				}
				if statusFilter != "" && status != statusFilter {
					continue
				}
				entries = append(entries, listEntry{Task: task, Status: status})
			}
			out := cmd.OutOrStdout()
			if asJSON {
				encoder := json.NewEncoder(out)
				encoder.SetIndent("", "  ")
				return encoder.Encode(entries)
			}
			writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "ID\tSTATUS\tPRIORIDADE\tLABELS\tTÍTULO")
			for _, entry := range entries {
				labels := ""
				for i, label := range entry.Labels {
					if i > 0 {
						labels += ","
					}
					labels += label
				}
				fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", entry.ExternalID, entry.Status, entry.Priority, labels, entry.Title)
			}
			writer.Flush()
			fmt.Fprintf(out, "\n%d tarefa(s) na fonte %s\n", len(entries), src.Name())
			return nil
		},
	}
	cmd.Flags().StringVar(&sourceName, "source", "", "fonte de tarefas")
	cmd.Flags().BoolVar(&eligibleOnly, "eligible", false, "mostra apenas tarefas elegíveis para execução")
	cmd.Flags().BoolVar(&asJSON, "json", false, "saída em JSON")
	cmd.Flags().StringVar(&statusFilter, "status", "", "filtra por status (todo, running, paused, succeeded, failed)")
	return cmd
}
