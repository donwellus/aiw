package cli

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/engine"
	"github.com/donwellus/aiw/internal/sources"
)

func newDoctorCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Valida config, engines, fontes e telemetria",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			failures := 0
			warns := 0
			ok := func(format string, args ...any) {
				fmt.Fprintf(out, "ok   "+format+"\n", args...)
			}
			warn := func(format string, args ...any) {
				warns++
				fmt.Fprintf(out, "warn "+format+"\n", args...)
			}
			fail := func(format string, args ...any) {
				failures++
				fmt.Fprintf(out, "fail "+format+"\n", args...)
			}

			fmt.Fprintf(out, "config: %s\n", cfg.Path)
			fmt.Fprintf(out, "raiz:   %s\n\n", cfg.Root)

			if _, err := exec.LookPath("git"); err != nil {
				fail("git não encontrado no PATH")
			} else {
				ok("git disponível")
			}
			if _, err := exec.LookPath("gh"); err != nil {
				warn("gh não encontrado (necessário apenas para --pr)")
			} else {
				ok("gh disponível")
			}

			engines := engine.NewRegistry(cfg)
			names := engines.Names()
			sort.Strings(names)
			for _, name := range names {
				if _, err := engines.Get(name); err != nil {
					fail("engine %s: %v", name, err)
				} else {
					ok("engine %s", name)
				}
			}

			sourceRegistry := sources.NewRegistry(cfg)
			sourceNames := sourceRegistry.Names()
			sort.Strings(sourceNames)
			for _, name := range sourceNames {
				if _, err := sourceRegistry.Open(name); err != nil {
					fail("fonte %s: %v", name, err)
					continue
				}
				sc, err := cfg.SourceConfig(name)
				if err != nil {
					fail("fonte %s: %v", name, err)
					continue
				}
				if sc.Driver == "folder" {
					path := cfg.ResolvePath(sc.Path)
					info, statErr := os.Stat(path)
					switch {
					case statErr != nil:
						warn("fonte %s sem pasta %s: %v", name, path, statErr)
					case !info.IsDir():
						warn("fonte %s: %s não é uma pasta", name, path)
					default:
						ok("fonte %s (%s)", name, path)
					}
				} else {
					ok("fonte %s (%s)", name, sc.Driver)
				}
			}

			switch cfg.Telemetry.Provider {
			case "", "noop":
				ok("telemetria noop")
			case "langfuse":
				if cfg.Telemetry.PublicKey == "" || cfg.Telemetry.SecretKey == "" {
					warn("Langfuse sem LANGFUSE_PUBLIC_KEY/LANGFUSE_SECRET_KEY (traces serão descartados)")
				}
				url := strings.TrimRight(cfg.Telemetry.Host, "/") + "/api/public/health"
				client := &http.Client{Timeout: 5 * time.Second}
				resp, err := client.Get(url)
				if err != nil {
					fail("Langfuse inacessível em %s: %v", url, err)
				} else {
					resp.Body.Close()
					if resp.StatusCode >= 400 {
						fail("Langfuse respondeu %d em %s", resp.StatusCode, url)
					} else {
						ok("Langfuse em %s", cfg.Telemetry.Host)
					}
				}
			default:
				fail("provider de telemetria desconhecido: %s", cfg.Telemetry.Provider)
			}

			if failures > 0 {
				return fmt.Errorf("%d verificação(ões) falharam", failures)
			}
			if warns > 0 {
				fmt.Fprintf(out, "\n%d aviso(s)\n", warns)
			}
			fmt.Fprintln(out, "\nambiente pronto")
			return nil
		},
	}
}
