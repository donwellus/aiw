package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/daemonrestart"
)

func newDaemonRestartCommand(opts *rootOptions) *cobra.Command {
	var requestID string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Reinicia o daemon aguardando o drain e validando o novo heartbeat",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			id := requestID
			if id == "" {
				id = daemonrestart.NewID()
			}
			if err := daemonrestart.Run(cfg, id, timeout); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "restart %s concluído\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&requestID, "request", "", "ID de uma solicitação assíncrona")
	cmd.Flags().DurationVar(&timeout, "timeout", daemonrestart.DefaultTimeout, "tempo máximo para drenar/iniciar")
	_ = cmd.Flags().MarkHidden("request")
	return cmd
}
