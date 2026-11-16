package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
)

func newApproveCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "approve <run-id> <gate>",
		Short: "Aprova um gate de uma execução pausada",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			run, err := pipeline.LoadRun(cfg, args[0])
			if err != nil {
				return err
			}
			gate := args[1]
			if gate != "spec" && gate != "plan" {
				return fmt.Errorf("gate inválido %q (use spec ou plan)", gate)
			}
			if run.Gates == nil {
				run.Gates = map[string]string{}
			}
			run.Gates[gate] = "approved"
			if err := pipeline.SaveRun(cfg, run); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "gate %s aprovado na run %s\n", gate, run.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "continue com: aiw resume %s\n", run.ID)
			return nil
		},
	}
}
