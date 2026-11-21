package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/report"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
)

func newReportCommand(opts *rootOptions) *cobra.Command {
	var asJSON bool
	var asYAML bool
	var asMD bool
	var asHTML bool
	var sourceName string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Gera um relatório completo do estado atual",
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
			reportOpts := report.Options{}
			if sourceName != "" {
				resolved, snapshots, err := collectReportSource(cfg, sourceName)
				if err != nil {
					return err
				}
				reportOpts.Source = resolved
				reportOpts.SourceTasks = snapshots
			}
			built, err := report.Build(cfg, store, reportOpts)
			if err != nil {
				return err
			}
			format := report.FormatTable
			switch {
			case asJSON:
				format = report.FormatJSON
			case asYAML:
				format = report.FormatYAML
			case asMD:
				format = report.FormatMD
			case asHTML:
				format = report.FormatHTML
			}
			return report.Render(cmd.OutOrStdout(), format, built)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "saída JSON")
	cmd.Flags().BoolVar(&asYAML, "yaml", false, "saída YAML")
	cmd.Flags().BoolVar(&asMD, "md", false, "saída Markdown")
	cmd.Flags().BoolVar(&asHTML, "html", false, "saída HTML autocontida")
	cmd.Flags().StringVar(&sourceName, "source", "", "inclui snapshot da fonte (folder: histórico completo; remoto: inbox)")
	cmd.MarkFlagsMutuallyExclusive("json", "yaml", "md", "html")
	return cmd
}

func collectReportSource(cfg *config.Config, name string) (string, []report.SourceTask, error) {
	src, filter, err := openSource(cfg, name)
	if err != nil {
		return "", nil, err
	}
	ctx := context.Background()
	var refs []source.Ref
	if lister, ok := src.(source.AllLister); ok {
		refs, err = lister.ListAll(ctx, filter)
	} else {
		refs, err = src.List(ctx, filter)
	}
	if err != nil {
		return "", nil, fmt.Errorf("listando fonte %s: %w", src.Name(), err)
	}
	snapshots := make([]report.SourceTask, 0, len(refs))
	for _, ref := range refs {
		task, err := src.Fetch(ctx, ref)
		if err != nil {
			return "", nil, fmt.Errorf("buscando tarefa %s na fonte %s: %w", ref.ID, src.Name(), err)
		}
		snapshots = append(snapshots, report.SourceTask{Task: task, Location: ref.Meta["location"]})
	}
	return src.Name(), snapshots, nil
}
