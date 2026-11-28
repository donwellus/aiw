package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/control"
)

func newPauseCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "pause [run-id]",
		Short: "Pausa uma run ou todo o projeto (sem argumento)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			runID := ""
			if len(args) > 0 {
				runID = args[0]
			}
			result, err := control.Pause(cfg, runID, "manual")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if result.Scope == "global" {
				if result.AlreadyActive {
					fmt.Fprintln(out, "pausa global já está ativa")
					return nil
				}
				fmt.Fprintln(out, "pausa global solicitada")
				fmt.Fprintln(out, "runs em andamento pausam no próximo boundary de estágio; work/daemon param de pegar novas tarefas")
				fmt.Fprintln(out, "para remover a pausa sem retomar runs: aiw unpause")
				fmt.Fprintln(out, "para retomar tudo: aiw resume --all")
				return nil
			}
			if result.AlreadyActive {
				fmt.Fprintf(out, "run %s já está pausada; use aiw resume %s\n", result.RunID, result.RunID)
				return nil
			}
			fmt.Fprintf(out, "pausa solicitada para %s; o pipeline para no próximo boundary de estágio\n", result.RunID)
			fmt.Fprintln(out, "para forçar agora, envie SIGINT ao processo dono da run")
			return nil
		},
	}
}
