package cli

import (
	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/mcpserver"
	"github.com/donwellus/aiw/internal/state"
)

func newMCPCommand(opts *rootOptions) *cobra.Command {
	var allowWrite bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Servidor MCP para o coordenador",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			store, err := state.Open(cfg.StateDir())
			if err != nil {
				return err
			}
			server := mcpserver.New(cfg, store, versionString(), allowWrite, cmd.InOrStdin(), cmd.OutOrStdout())
			return server.Serve(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&allowWrite, "allow-write", false, "habilita pause/resume/approve/release/clean")
	return cmd
}
