package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
)

func newPRCommand(opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Operações de pull request",
	}
	adopt := &cobra.Command{
		Use:   "adopt <run-id> <pr-url>",
		Short: "Adota um PR existente para uma run",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			p := pipeline.New(cfg)
			defer closeTelemetry(p)
			run, err := p.AdoptPR(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "PR adotado: %s\n", run.PRURL)
			fmt.Fprintf(cmd.OutOrStdout(), "run=%s branch=%s status=%s\n", run.ID, run.Branch, run.Status)
			return nil
		},
	}
	cmd.AddCommand(adopt)
	return cmd
}
