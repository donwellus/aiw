package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/runner"
	"github.com/donwellus/aiw/internal/state"
)

type daemonStats struct {
	mu        sync.Mutex
	startedAt time.Time
	interval  time.Duration
	workers   int
	source    string
	cycles    int
	lastCycle time.Time
	lastTasks int
	totals    map[string]int
}

func newDaemonStats(interval time.Duration, workers int, sourceName string) *daemonStats {
	return &daemonStats{
		startedAt: time.Now().UTC(),
		interval:  interval,
		workers:   workers,
		source:    sourceName,
		totals:    map[string]int{},
	}
}

func (s *daemonStats) add(outcome runner.Outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totals[outcome.Status]++
}

func (s *daemonStats) cycle(tasks int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cycles++
	s.lastTasks = tasks
	s.lastCycle = time.Now().UTC()
}

func (s *daemonStats) snapshot(store *state.Store) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	totals := make(map[string]int, len(s.totals))
	for key, value := range s.totals {
		totals[key] = value
	}
	var running []string
	for _, record := range store.All() {
		if record.Status == "running" {
			running = append(running, record.Source+":"+record.ID)
		}
	}
	return map[string]any{
		"pid":              os.Getpid(),
		"started_at":       s.startedAt,
		"uptime_seconds":   int(time.Since(s.startedAt).Seconds()),
		"interval":         s.interval.String(),
		"workers":          s.workers,
		"source":           s.source,
		"cycles":           s.cycles,
		"last_cycle_at":    s.lastCycle,
		"last_cycle_tasks": s.lastTasks,
		"totals":           totals,
		"running":          running,
		"claims":           store.Claims(),
	}
}

func newDaemonCommand(opts *rootOptions) *cobra.Command {
	var workers int
	var interval time.Duration
	var sourceName string
	var repoFlag string
	var engineName string
	var budget float64
	var auto bool
	var force bool
	var createPR bool
	var noWriteBack bool
	var maxTasks int
	var asJSON bool
	var once bool
	var statusAddr string

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Processa tarefas continuamente em intervalos",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			if workers == 0 {
				workers = cfg.Defaults.Workers
			}
			if workers < 1 {
				return fmt.Errorf("--workers precisa ser >= 1")
			}
			if interval <= 0 {
				return fmt.Errorf("--interval precisa ser > 0")
			}
			p := pipeline.New(cfg)
			defer closeTelemetry(p)
			if engineName != "" {
				if _, err := p.Engines.Get(engineName); err != nil {
					return err
				}
			}
			store, err := state.Open(cfg.StateDir())
			if err != nil {
				return err
			}
			src, filter, err := openSource(cfg, sourceName)
			if err != nil {
				return err
			}
			run := runner.New(cfg, store, p)
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			logOut := cmd.OutOrStdout()
			if asJSON {
				logOut = cmd.ErrOrStderr()
			}
			stats := newDaemonStats(interval, workers, src.Name())
			if statusAddr != "" {
				shutdown, addr, err := startDaemonStatus(ctx, statusAddr, stats, store)
				if err != nil {
					return err
				}
				defer shutdown()
				fmt.Fprintf(logOut, "[daemon] status em http://%s/status\n", addr)
			}
			fmt.Fprintf(logOut, "[daemon] iniciado source=%s workers=%d interval=%s\n", src.Name(), workers, interval)

			out := cmd.OutOrStdout()
			warned := map[string]bool{}
			cycleFailures := 0
			for {
				cycleFailures = 0
				candidates, skips, collectErr := run.Collect(ctx, src, filter, repoFlag, force, maxTasks)
				if collectErr != nil {
					if ctx.Err() != nil {
						break
					}
					if once {
						return collectErr
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "[daemon] aviso: %v\n", collectErr)
				}
				for _, skip := range skips {
					key := skip.TaskID + ":" + skip.Status + ":" + skip.Reason
					if warned[key] {
						continue
					}
					warned[key] = true
					if skip.Status == "failed" {
						cycleFailures++
					}
					printWorkOutcome(out, asJSON, skip)
				}
				jobs := make([]runner.Job, 0, len(candidates))
				for _, candidate := range candidates {
					jobs = append(jobs, runner.Job{
						Task:  candidate.Task,
						Ref:   candidate.Ref,
						Repo:  candidate.Repo,
						Force: force,
						Options: pipeline.Options{
							AutoApprove:      auto,
							EngineOverride:   engineName,
							BudgetUSD:        budget,
							CreatePR:         createPR,
							DisableWriteBack: noWriteBack,
						},
					})
				}
				if len(jobs) > 0 {
					fmt.Fprintf(logOut, "[daemon] ciclo: %d tarefa(s)\n", len(jobs))
					run.Work(ctx, jobs, workers, func(outcome runner.Outcome) {
						stats.add(outcome)
						if outcome.Status == "failed" {
							cycleFailures++
						}
						printWorkOutcome(out, asJSON, outcome)
					})
				}
				stats.cycle(len(jobs))

				if once {
					break
				}
				select {
				case <-ctx.Done():
					fmt.Fprintln(logOut, "[daemon] encerrando")
					return nil
				case <-time.After(interval):
				}
			}
			if ctx.Err() != nil {
				fmt.Fprintln(logOut, "[daemon] encerrado")
			}
			if once && cycleFailures > 0 {
				return fmt.Errorf("%d tarefa(s) falharam", cycleFailures)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&workers, "workers", 0, "número de tarefas em paralelo (default do config)")
	cmd.Flags().DurationVar(&interval, "interval", time.Minute, "intervalo entre varreduras da fonte")
	cmd.Flags().StringVar(&sourceName, "source", "", "fonte de tarefas")
	cmd.Flags().StringVar(&repoFlag, "repo", "", "repositório alvo (sobrepõe a tarefa)")
	cmd.Flags().StringVar(&engineName, "engine", "", "engine para todos os estágios")
	cmd.Flags().Float64Var(&budget, "budget", 0, "orçamento por tarefa em USD")
	cmd.Flags().BoolVar(&auto, "auto", false, "aprova gates automaticamente")
	cmd.Flags().BoolVar(&force, "force", false, "ignora deduplicação de tarefas concluídas/pausadas")
	cmd.Flags().BoolVar(&createPR, "pr", false, "faz push e cria PR com gh")
	cmd.Flags().BoolVar(&noWriteBack, "no-write-back", false, "não atualiza o estado na fonte")
	cmd.Flags().IntVar(&maxTasks, "max-tasks", 0, "máximo de tarefas enfileiradas por ciclo")
	cmd.Flags().BoolVar(&asJSON, "json", false, "saída JSONL por tarefa")
	cmd.Flags().BoolVar(&once, "once", false, "executa um único ciclo e encerra")
	cmd.Flags().StringVar(&statusAddr, "status-addr", "", "endereço HTTP para /healthz e /status (ex.: 127.0.0.1:8787)")
	return cmd
}

func startDaemonStatus(ctx context.Context, addr string, stats *daemonStats, store *state.Store) (func(), string, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(stats.snapshot(store))
	})
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", err
	}
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = server.Serve(listener)
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}
	return shutdown, listener.Addr().String(), nil
}
