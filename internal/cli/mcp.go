package cli

import (
	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/mcpserver"
	"github.com/donwellus/aiw/internal/state"
)

func newMCPCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Servidor MCP read-only para o coordenador",
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
			server := mcpserver.New(cfg, store, versionString(), cmd.InOrStdin(), cmd.OutOrStdout())
			return server.Serve(cmd.Context())
		},
	}
}
