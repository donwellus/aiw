package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/runview"
	"github.com/donwellus/aiw/internal/state"
)

type runJSON struct {
	*pipeline.RunState
	Current      bool   `json:"current"`
	SupersededBy string `json:"superseded_by,omitempty"`
}

func newRunsCommand(opts *rootOptions) *cobra.Command {
	var asJSON bool
	var all bool
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Lista execuções atuais (ou histórico com --all)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			store, err := state.Open(cfg.StateDir())
			if err != nil {
				return err
			}
			entries, err := runview.Build(cfg, store)
			if err != nil {
				return err
			}
			if !all {
				entries = runview.Current(entries)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				payload := make([]runJSON, 0, len(entries))
				for _, entry := range entries {
					payload = append(payload, runJSON{RunState: entry.Run, Current: entry.Current, SupersededBy: entry.SupersededBy})
				}
				encoder := json.NewEncoder(out)
				encoder.SetIndent("", "  ")
				return encoder.Encode(payload)
			}
			if len(entries) == 0 {
				fmt.Fprintln(out, "nenhuma execução atual")
				fmt.Fprintln(out, "use aiw runs --all para ver o histórico")
				return nil
			}
			writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "RUN\tSTATUS\tESTÁGIO\tCUSTO\tATUAL\tSUPERSEDIDA POR\tTÍTULO")
			for _, entry := range entries {
				current := "não"
				if entry.Current {
					current = "sim"
				}
				supersededBy := entry.SupersededBy
				if supersededBy == "" {
					supersededBy = "-"
				}
				fmt.Fprintf(writer, "%s\t%s\t%s\tUS$ %.2f\t%s\t%s\t%s\n",
					entry.Run.ID,
					entry.Run.Status,
					entry.Run.Stage,
					entry.Run.Usage.CostUSD,
					current,
					supersededBy,
					entry.Run.Task.Title,
				)
			}
			writer.Flush()
			fmt.Fprintf(out, "\n%d execução(ões)\n", len(entries))
			if !all {
				fmt.Fprintln(out, "use aiw runs --all para ver tentativas antigas")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "saída em JSON")
	cmd.Flags().BoolVar(&all, "all", false, "inclui tentativas antigas/superadas")
	return cmd
}
