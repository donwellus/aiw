package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
)

func newRunsCommand(opts *rootOptions) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Lista execuções registradas",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			entries, err := os.ReadDir(cfg.RunsDir())
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintln(cmd.OutOrStdout(), "nenhuma execução registrada")
					return nil
				}
				return err
			}
			var runs []*pipeline.RunState
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				run, err := pipeline.LoadRun(cfg, entry.Name())
				if err != nil {
					continue
				}
				runs = append(runs, run)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				encoder := json.NewEncoder(out)
				encoder.SetIndent("", "  ")
				return encoder.Encode(runs)
			}
			writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "RUN\tSTATUS\tESTÁGIO\tCUSTO\tTÍTULO")
			for _, run := range runs {
				fmt.Fprintf(writer, "%s\t%s\t%s\tUS$ %.2f\t%s\n", run.ID, run.Status, run.Stage, run.Usage.CostUSD, run.Task.Title)
			}
			writer.Flush()
			fmt.Fprintf(out, "\n%d execução(ões)\n", len(runs))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "saída em JSON")
	return cmd
}
