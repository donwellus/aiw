package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/control"
	"github.com/donwellus/aiw/internal/pause"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/runner"
	"github.com/donwellus/aiw/internal/state"
	"github.com/donwellus/aiw/internal/webhook"
)

type daemonStats struct {
	mu        sync.Mutex
	startedAt time.Time
	interval  time.Duration
	workers   int
	source    string
	dataDir   string
	cycles    int
	lastCycle time.Time
	lastTasks int
	totals    map[string]int
}

func newDaemonStats(interval time.Duration, workers int, sourceName, dataDir string) *daemonStats {
	return &daemonStats{
		startedAt: time.Now().UTC(),
		interval:  interval,
		workers:   workers,
		source:    sourceName,
		dataDir:   dataDir,
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
		"paused":           pause.Active(s.dataDir),
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
	var webhookAddr string
	var drainTimeout time.Duration
	var pausePoll time.Duration

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
			resolvedSource := sourceName
			if resolvedSource == "" {
				resolvedSource = cfg.DefaultSource
			}
			run := runner.New(cfg, store, p)
			drain, ctx := newDrainState(drainTimeout)
			defer drain.Close()

			logOut := cmd.OutOrStdout()
			if asJSON {
				logOut = cmd.ErrOrStderr()
			}
			wake := make(chan struct{}, 1)
			if webhookAddr != "" {
				sc, err := cfg.SourceConfig(resolvedSource)
				if err != nil {
					return err
				}
				if sc.Driver != "github" {
					return fmt.Errorf("webhook disponível apenas para source github nesta versão (driver atual: %s)", sc.Driver)
				}
				secretEnv := sc.WebhookSecretEnv
				if secretEnv == "" {
					secretEnv = "GITHUB_WEBHOOK_SECRET"
				}
				secret := os.Getenv(secretEnv)
				if secret == "" {
					return fmt.Errorf("variável %s não definida para validar webhooks", secretEnv)
				}
				handler := webhook.NewGitHub(sc.Repo, secret, func(event webhook.GitHubEvent) {
					fmt.Fprintf(logOut, "[daemon] webhook github: #%d %s\n", event.Number, event.Action)
					select {
					case wake <- struct{}{}:
					default:
					}
				})
				stopWebhook, addr, err := startWebhookServer(ctx, webhookAddr, handler)
				if err != nil {
					return err
				}
				defer stopWebhook()
				fmt.Fprintf(logOut, "[daemon] webhook em http://%s/webhooks/github\n", addr)
			}

			if pausePoll <= 0 {
				pausePoll = 10 * time.Second
			}
			stats := newDaemonStats(interval, workers, src.Name(), cfg.DataDir())
			if statusAddr != "" {
				shutdown, addr, err := startDaemonStatus(ctx, statusAddr, stats, store)
				if err != nil {
					return err
				}
				defer shutdown()
				fmt.Fprintf(logOut, "[daemon] status em http://%s/status\n", addr)
			}
			fmt.Fprintf(logOut, "[daemon] iniciado source=%s workers=%d interval=%s\n", src.Name(), workers, interval)

			ops, err := control.OpenOperationStore(cfg)
			if err != nil {
				return err
			}
			executor := control.NewExecutor(cfg, store, ops, run, slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelInfo})))
			execCtx, cancelExec := context.WithCancel(ctx)
			var execWG sync.WaitGroup
			execWG.Add(1)
			go func() {
				defer execWG.Done()
				executor.Run(execCtx)
			}()
			defer func() {
				cancelExec()
				execWG.Wait()
			}()

			daemonInfo := control.DaemonInfo{
				PID:        os.Getpid(),
				StartedAt:  time.Now().UTC(),
				Workers:    workers,
				Interval:   interval.String(),
				Source:     src.Name(),
				StatusAddr: statusAddr,
				Version:    versionString(),
			}
			if err := control.WriteDaemonInfo(cfg, daemonInfo); err != nil {
				return err
			}
			heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
			var heartbeatWG sync.WaitGroup
			heartbeatWG.Add(1)
			go func() {
				defer heartbeatWG.Done()
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-heartbeatCtx.Done():
						return
					case <-ticker.C:
						if err := control.WriteDaemonInfo(cfg, daemonInfo); err != nil {
							fmt.Fprintf(cmd.ErrOrStderr(), "[daemon] aviso: heartbeat: %v\n", err)
						}
					}
				}
			}()
			defer func() {
				cancelHeartbeat()
				heartbeatWG.Wait()
				control.RemoveDaemonInfo(cfg, os.Getpid())
			}()

			out := cmd.OutOrStdout()
			warned := map[string]bool{}
			cycleFailures := 0
			idleLogged := false
		loop:
			for !drain.Draining() {
				if pause.Active(cfg.DataDir()) {
					if once {
						fmt.Fprintln(logOut, "[daemon] pausa global ativa; --once sem ciclo")
						break
					}
					if !idleLogged {
						fmt.Fprintf(logOut, "[daemon] pausa global ativa; idle (checando a cada %s)\n", pausePoll)
						idleLogged = true
					}
					select {
					case <-ctx.Done():
					case <-drain.drain:
					case <-time.After(pausePoll):
					}
					continue
				}
				idleLogged = false
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
				if len(jobs) > 0 && !drain.Draining() && !pause.Active(cfg.DataDir()) {
					if err := run.MarkQueued(candidates); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "[daemon] aviso: falha ao registrar fila: %v\n", err)
					}
					fmt.Fprintf(logOut, "[daemon] ciclo: %d tarefa(s)\n", len(jobs))
					cycleCh, stopCycle := cycleDrain(ctx, drain.drain, cfg.DataDir(), pausePoll)
					run.Work(ctx, jobs, workers, cycleCh, func(outcome runner.Outcome) {
						stats.add(outcome)
						if outcome.Status == "failed" {
							cycleFailures++
						}
						printWorkOutcome(out, asJSON, outcome)
					})
					stopCycle()
				}
				stats.cycle(len(jobs))

				if drain.Draining() {
					break
				}
				if once {
					break
				}
				select {
				case <-ctx.Done():
					break loop
				case <-drain.drain:
					break loop
				case <-wake:
					continue
				case <-time.After(interval):
				}
			}
			if ctx.Err() != nil {
				fmt.Fprintln(logOut, "[daemon] drenagem forçada; encerrado")
				return fmt.Errorf("drenagem forçada")
			}
			if drain.Draining() {
				fmt.Fprintln(logOut, "[daemon] drenado; encerrado")
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
	cmd.Flags().StringVar(&webhookAddr, "webhook-addr", "", "endereço HTTP para webhooks (github) - ex.: 127.0.0.1:8788")
	cmd.Flags().DurationVar(&drainTimeout, "drain-timeout", 5*time.Minute, "tempo máximo para drenar runs em andamento após SIGINT")
	cmd.Flags().DurationVar(&pausePoll, "pause-poll", 10*time.Second, "intervalo de checagem da pausa global no modo idle")
	return cmd
}

func cycleDrain(ctx context.Context, signal <-chan struct{}, dataDir string, interval time.Duration) (<-chan struct{}, func()) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ch := make(chan struct{})
	done := make(chan struct{})
	var closeOnce sync.Once
	closeCh := func() { closeOnce.Do(func() { close(ch) }) }
	go func() {
		if pause.Active(dataDir) {
			closeCh()
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				closeCh()
				return
			case <-signal:
				closeCh()
				return
			case <-done:
				return
			case <-ticker.C:
				if pause.Active(dataDir) {
					closeCh()
					return
				}
			}
		}
	}()
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(done) }) }
	return ch, stop
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

func startWebhookServer(ctx context.Context, addr string, handler http.Handler) (func(), string, error) {
	mux := http.NewServeMux()
	mux.Handle("/webhooks/github", handler)
	mux.Handle("/webhooks", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", err
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
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
