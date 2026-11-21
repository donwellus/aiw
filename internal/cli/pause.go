package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
)

func newPauseCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "pause <run-id>",
		Short: "Solicita pausa no próximo boundary de estágio",
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
			switch run.Status {
			case "succeeded", "failed", "canceled":
				return fmt.Errorf("run %s já foi encerrada com status %s", run.ID, run.Status)
			case "needs_input":
				return fmt.Errorf("run %s aguarda input; pausa não se aplica", run.ID)
			case "paused":
				fmt.Fprintf(cmd.OutOrStdout(), "run %s já está pausada; use aiw resume %s\n", run.ID, run.ID)
				return nil
			}
			marker := filepath.Join(cfg.RunsDir(), run.ID, pipeline.PauseMarker)
			if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pausa solicitada para %s; o pipeline para no próximo boundary de estágio\n", run.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "para forçar agora, envie SIGINT ao processo dono da run\n")
			return nil
		},
	}
}
