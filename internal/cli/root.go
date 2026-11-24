package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/sources"
)

var Version = "dev"

type rootOptions struct {
	root       string
	configPath string
}

func NewRoot() *cobra.Command {
	opts := &rootOptions{}
	root := &cobra.Command{
		Use:           "aiw",
		Short:         "Pipeline de desenvolvimento com agentes de IA",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&opts.root, "root", ".", "raiz do projeto")
	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "caminho do arquivo de config")
	root.AddCommand(
		newInitCommand(opts),
		newDoctorCommand(opts),
		newSyncCommand(opts),
		newListCommand(opts),
		newRunCommand(opts),
		newWorkCommand(opts),
		newDaemonCommand(opts),
		newPauseCommand(opts),
		newCancelCommand(opts),
		newEvalCommand(opts),
		newReleaseCommand(opts),
		newIntakeCommand(opts),
		newRunsCommand(opts),
		newStatusCommand(opts),
		newReportCommand(opts),
		newCleanCommand(opts),
		newMCPCommand(opts),
		newApproveCommand(opts),
		newResumeCommand(opts),
		newVersionCommand(),
	)
	return root
}

func (o *rootOptions) load() (*config.Config, error) {
	root := o.root
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return config.Load(abs, o.configPath)
}

func openSource(cfg *config.Config, name string) (source.Source, source.Filter, error) {
	if name == "" {
		name = cfg.DefaultSource
	}
	sc, err := cfg.SourceConfig(name)
	if err != nil {
		return nil, source.Filter{}, err
	}
	src, err := sources.NewRegistry(cfg).Open(name)
	if err != nil {
		return nil, source.Filter{}, err
	}
	filter := source.Filter{
		Labels:        sc.Labels,
		ExcludeLabels: sc.ExcludeLabels,
		Limit:         sc.Limit,
	}
	return src, filter, nil
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Mostra a versão",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), versionString())
			return nil
		},
	}
}

func versionString() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	var revision string
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && len(setting.Value) >= 7 {
			revision = setting.Value[:7]
		}
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		if revision != "" {
			return info.Main.Version + " (" + revision + ")"
		}
		return info.Main.Version
	}
	if revision != "" {
		return "dev (" + revision + ")"
	}
	return Version
}

func resolveTask(ctx context.Context, cfg *config.Config, sourceName, taskArg string) (source.Source, source.Ref, *source.Task, error) {
	if taskArg == "" {
		return nil, source.Ref{}, nil, fmt.Errorf("informe --task (caminho, source:id ou id)")
	}
	taskPath := taskArg
	if !filepath.IsAbs(taskPath) && !fileExists(taskPath) {
		candidate := filepath.Join(cfg.Root, taskPath)
		if fileExists(candidate) {
			taskPath = candidate
		}
	}
	if fileExists(taskPath) {
		abs, err := filepath.Abs(taskPath)
		if err != nil {
			return nil, source.Ref{}, nil, err
		}
		src, _, err := openSource(cfg, sourceName)
		if err != nil {
			return nil, source.Ref{}, nil, err
		}
		ref := source.Ref{Source: src.Name(), ID: filepath.Base(abs), URL: abs}
		task, err := src.Fetch(ctx, ref)
		if err != nil {
			return nil, source.Ref{}, nil, err
		}
		task.Source = src.Name()
		return src, ref, task, nil
	}

	lookupSource := sourceName
	lookupID := taskArg
	if parts := strings.SplitN(taskArg, ":", 2); len(parts) == 2 {
		lookupSource = parts[0]
		lookupID = parts[1]
	}
	src, filter, err := openSource(cfg, lookupSource)
	if err != nil {
		return nil, source.Ref{}, nil, err
	}
	filter.Limit = 0
	refs, err := src.List(ctx, filter)
	if err != nil {
		return nil, source.Ref{}, nil, err
	}
	for _, ref := range refs {
		if ref.ID == lookupID {
			task, err := src.Fetch(ctx, ref)
			if err != nil {
				return nil, source.Ref{}, nil, err
			}
			task.Source = src.Name()
			return src, ref, task, nil
		}
	}
	return nil, source.Ref{}, nil, fmt.Errorf("tarefa %q não encontrada na fonte %s", lookupID, src.Name())
}

func resolveRepo(cfg *config.Config, task *source.Task, repoFlag string) string {
	return cfg.ResolveRepo(task, repoFlag)
}

func queueFileName(sourceName, id string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-")
	return replacer.Replace(sourceName) + "-" + replacer.Replace(id) + ".json"
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func closeTelemetry(p *pipeline.Pipeline) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := p.Telemetry.Close(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "aviso: falha ao finalizar telemetria: %v\n", err)
	}
}
