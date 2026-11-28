package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	opencodeinstall "github.com/donwellus/aiw/internal/opencode"
)

func newOpencodeCommand(opts *rootOptions) *cobra.Command {
	var global bool
	var force bool
	cmd := &cobra.Command{
		Use:   "opencode",
		Short: "Instala integração do aiw no OpenCode",
	}
	install := &cobra.Command{
		Use:   "install",
		Short: "Instala o coordinator e o MCP do aiw",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := opencodeinstall.Install(opencodeinstall.InstallOptions{
				Root:   opts.root,
				Global: global,
				Force:  force,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			state := "atualizado"
			if !result.AgentChanged {
				state = "já estava atualizado"
			}
			fmt.Fprintf(out, "agente: %s (%s)\n", result.AgentPath, state)
			configState := "criado/atualizado"
			if !result.ConfigChanged {
				configState = "preservado"
			}
			fmt.Fprintf(out, "config: %s (%s)\n", result.ConfigPath, configState)
			if result.Message != "" {
				fmt.Fprintf(out, "%s\n", result.Message)
			}
			if !global {
				fmt.Fprintln(out, "commite .opencode/agents/aiw-coordinator.md e opencode.jsonc no repositório")
			}
			return nil
		},
	}
	install.Flags().BoolVar(&global, "global", false, "instala em ~/.config/opencode")
	install.Flags().BoolVar(&force, "force", false, "sobrescreve arquivos existentes")
	cmd.AddCommand(install)
	return cmd
}
