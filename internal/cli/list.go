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
	Status   string `json:"status"`
	Location string `json:"location,omitempty"`
}

func newListCommand(opts *rootOptions) *cobra.Command {
	var sourceName string
	var eligibleOnly bool
	var asJSON bool
	var statusFilter string
	var all bool
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
			var refs []source.Ref
			if all {
				lister, ok := src.(source.AllLister)
				if !ok {
					return fmt.Errorf("--all disponível apenas para fontes com histórico local (folder); para runs use 'aiw runs'")
				}
				refs, err = lister.ListAll(ctx, filter)
			} else {
				refs, err = src.List(ctx, filter)
			}
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
				record, found := store.FindByID(task.ExternalID)
				location := ref.Meta["location"]
				if location == "" {
					location = "inbox"
				}
				if !found || record.Status == "todo" {
					switch location {
					case "done":
						status = "succeeded"
					case "failed":
						status = "failed"
					case "doing":
						status = "running"
					}
				}
				if found {
					if record.Status != "todo" || status == "todo" {
						status = record.Status
					}
					switch record.Status {
					case "succeeded":
						eligible = record.ContentHash != task.ContentHash
					case "running", "paused", "needs_input":
						eligible = false
					}
				}
				if location != "inbox" {
					eligible = false
				}
				if eligibleOnly && !eligible {
					continue
				}
				if statusFilter != "" && status != statusFilter {
					continue
				}
				entries = append(entries, listEntry{Task: task, Status: status, Location: location})
			}
			out := cmd.OutOrStdout()
			if asJSON {
				encoder := json.NewEncoder(out)
				encoder.SetIndent("", "  ")
				return encoder.Encode(entries)
			}
			writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "ID\tSTATUS\tLOCAL\tPRIORIDADE\tLABELS\tTÍTULO")
			for _, entry := range entries {
				labels := ""
				for i, label := range entry.Labels {
					if i > 0 {
						labels += ","
					}
					labels += label
				}
				fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", entry.ExternalID, entry.Status, entry.Location, entry.Priority, labels, entry.Title)
			}
			writer.Flush()
			fmt.Fprintf(out, "\n%d tarefa(s) na fonte %s\n", len(entries), src.Name())
			if len(entries) == 0 && (statusFilter == "running" || statusFilter == "paused" || statusFilter == "needs_input") {
				fmt.Fprintln(out, "tarefas nesses estados ficam fora do inbox (doing/estado); use 'aiw status' para ver o que está em andamento")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sourceName, "source", "", "fonte de tarefas")
	cmd.Flags().BoolVar(&eligibleOnly, "eligible", false, "mostra apenas tarefas elegíveis para execução")
	cmd.Flags().BoolVar(&all, "all", false, "inclui doing/done/failed (histórico local)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "saída em JSON")
	cmd.Flags().StringVar(&statusFilter, "status", "", "filtra por status (todo, running, paused, succeeded, failed)")
	return cmd
}
