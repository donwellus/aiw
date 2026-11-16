package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newSyncCommand(opts *rootOptions) *cobra.Command {
	var sourceName string
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Busca tarefas da fonte e grava snapshots na fila local",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			src, filter, err := openSource(cfg, sourceName)
			if err != nil {
				return err
			}
			ctx := context.Background()
			refs, err := src.List(ctx, filter)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(cfg.QueueDir(), 0o755); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, ref := range refs {
				task, err := src.Fetch(ctx, ref)
				if err != nil {
					return fmt.Errorf("buscando %s: %w", ref.ID, err)
				}
				path := filepath.Join(cfg.QueueDir(), queueFileName(src.Name(), task.ExternalID))
				if err := writeJSONFile(path, task); err != nil {
					return err
				}
				fmt.Fprintf(out, "%s\t%s\n", task.ExternalID, task.Title)
			}
			fmt.Fprintf(out, "\n%d tarefa(s) sincronizada(s) em %s\n", len(refs), cfg.QueueDir())
			return nil
		},
	}
	cmd.Flags().StringVar(&sourceName, "source", "", "fonte de tarefas (default do config)")
	return cmd
}
