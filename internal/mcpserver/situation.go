package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/control"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/report"
)

type situationSummary struct {
	GeneratedAt time.Time           `json:"generated_at"`
	Root        string              `json:"root"`
	Daemon      situationDaemon     `json:"daemon"`
	Capacity    situationCapacity   `json:"capacity"`
	Summary     report.Summary      `json:"summary"`
	Queued      []situationTask     `json:"queued"`
	Running     []situationTask     `json:"running"`
	PROpen      []situationTask     `json:"pr_open"`
	Paused      []situationTask     `json:"paused"`
	NeedsInput  []situationTask     `json:"needs_input"`
	Failed      []situationTask     `json:"failed"`
	Operations  []control.Operation `json:"operations"`
	Claims      []report.Claim      `json:"claims"`
	Alerts      []report.Alert      `json:"alerts"`
}

type situationDaemon struct {
	Active              bool      `json:"active"`
	PID                 int       `json:"pid,omitempty"`
	WorkerSlots         int       `json:"worker_slots"`
	Interval            string    `json:"interval,omitempty"`
	Source              string    `json:"source,omitempty"`
	Version             string    `json:"version,omitempty"`
	StartedAt           time.Time `json:"started_at,omitempty"`
	HeartbeatAgeSeconds float64   `json:"heartbeat_age_seconds,omitempty"`
}

type situationCapacity struct {
	WorkerSlots             int    `json:"worker_slots"`
	ActiveRuns              int    `json:"active_runs"`
	QueuedTasks             int    `json:"queued_tasks"`
	ActiveControlOperations int    `json:"active_control_operations"`
	QueuedControlOperations int    `json:"queued_control_operations"`
	OverWorkerSlots         int    `json:"over_worker_slots"`
	Explanation             string `json:"explanation"`
}

type situationTask struct {
	ID             string    `json:"id"`
	Title          string    `json:"title,omitempty"`
	Source         string    `json:"source,omitempty"`
	Status         string    `json:"status"`
	Stage          string    `json:"stage,omitempty"`
	Delivery       string    `json:"delivery,omitempty"`
	RunID          string    `json:"run_id,omitempty"`
	Gate           string    `json:"gate,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	Blockers       []string  `json:"blockers,omitempty"`
	PRURL          string    `json:"pr_url,omitempty"`
	CostUSD        float64   `json:"cost_usd"`
	LastEventAt    time.Time `json:"last_event_at,omitempty"`
	ElapsedSeconds int64     `json:"elapsed_seconds,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

func (s *Server) toolSituation() (any, *rpcError) {
	built, err := report.Build(s.cfg, s.store, report.Options{})
	if err != nil {
		return toolError(err.Error()), nil
	}
	daemonInfo, daemonActive := control.DaemonActive(s.cfg, 15*time.Second)
	if !daemonActive {
		daemonInfo, _ = control.ReadDaemonInfo(s.cfg)
	}
	workerSlots := s.cfg.Defaults.Workers
	if daemonActive && daemonInfo.Workers > 0 {
		workerSlots = daemonInfo.Workers
	}
	daemon := situationDaemon{Active: daemonActive, WorkerSlots: workerSlots}
	if daemonInfo.PID != 0 {
		daemon.PID = daemonInfo.PID
		daemon.Interval = daemonInfo.Interval
		daemon.Source = daemonInfo.Source
		daemon.Version = daemonInfo.Version
		daemon.StartedAt = daemonInfo.StartedAt
		if !daemonInfo.HeartbeatAt.IsZero() {
			daemon.HeartbeatAgeSeconds = time.Since(daemonInfo.HeartbeatAt).Seconds()
		}
	}

	operations, activeOps, queuedOps := s.situationOperations()
	snapshot := situationSummary{
		GeneratedAt: time.Now().UTC(),
		Root:        s.cfg.Root,
		Daemon:      daemon,
		Summary:     built.Summary,
		Queued:      []situationTask{},
		Running:     []situationTask{},
		Paused:      []situationTask{},
		NeedsInput:  []situationTask{},
		Failed:      []situationTask{},
		Operations:  operations,
		Claims:      built.Claims,
		Alerts:      built.Alerts,
	}
	for _, task := range built.Tasks {
		entry := s.situationTask(task)
		switch task.Status {
		case "queued":
			entry.Reason = firstNonEmpty(entry.Reason, "aguardando worker")
			snapshot.Queued = append(snapshot.Queued, entry)
		case "running":
			entry.Reason = firstNonEmpty(entry.Reason, "em execução")
			snapshot.Running = append(snapshot.Running, entry)
		case "pr_open":
			entry.Reason = firstNonEmpty(entry.Reason, "PR aberto; aguardando CI/merge")
			entry.PRURL = task.PRURL
			snapshot.PROpen = append(snapshot.PROpen, entry)
		case "paused":
			snapshot.Paused = append(snapshot.Paused, entry)
		case "needs_input":
			entry.Reason = firstNonEmpty(entry.Reason, "aguardando resposta humana")
			snapshot.NeedsInput = append(snapshot.NeedsInput, entry)
		case "failed":
			snapshot.Failed = append(snapshot.Failed, entry)
		}
	}
	activeRuns := len(snapshot.Running)
	over := activeRuns - workerSlots
	if over < 0 {
		over = 0
	}
	snapshot.Capacity = situationCapacity{
		WorkerSlots:             workerSlots,
		ActiveRuns:              activeRuns,
		QueuedTasks:             len(snapshot.Queued),
		ActiveControlOperations: activeOps,
		QueuedControlOperations: queuedOps,
		OverWorkerSlots:         over,
		Explanation:             "worker_slots é apenas o pool de workers; active_runs inclui runs iniciadas por operações de controle",
	}
	result, err := toolResult(snapshot)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) situationOperations() ([]control.Operation, int, int) {
	store, err := control.OpenOperationStore(s.cfg)
	if err != nil {
		return []control.Operation{}, 0, 0
	}
	all, err := store.List(20)
	if err != nil {
		return []control.Operation{}, 0, 0
	}
	active := 0
	queued := 0
	for _, operation := range all {
		switch operation.Status {
		case control.StatusRunning:
			active++
		case control.StatusQueued:
			queued++
		}
	}
	return all, active, queued
}

func (s *Server) situationTask(task report.Task) situationTask {
	result := situationTask{
		ID:        task.ID,
		Title:     task.Title,
		Source:    task.Source,
		Status:    task.Status,
		Stage:     task.Stage,
		Delivery:  task.Delivery,
		RunID:     task.RunID,
		CostUSD:   task.CostUSD,
		Reason:    task.Error,
		UpdatedAt: task.UpdatedAt,
	}
	if task.RunID != "" {
		if run, err := pipeline.LoadRun(s.cfg, task.RunID); err == nil {
			result.Gate = escalatedGate(run)
			if result.Reason == "" && run.Status == "paused" {
				result.Reason = pausedReason(s.cfg, run)
			}
		}
		if events := s.readRunEvents(task.RunID); len(events) > 0 {
			result.LastEventAt, result.Blockers = eventInsights(events)
			if result.ElapsedSeconds == 0 && !result.LastEventAt.IsZero() {
				result.ElapsedSeconds = int64(time.Since(result.LastEventAt).Seconds())
			}
		}
	}
	if task.Status == "paused" && result.Reason == "" {
		result.Reason = "pausada"
	}
	return result
}

func escalatedGate(run *pipeline.RunState) string {
	for _, gate := range []string{"spec", "plan", "intake"} {
		if strings.EqualFold(run.Gates[gate], "escalated") {
			return gate
		}
	}
	return ""
}

func pausedReason(cfg *config.Config, run *pipeline.RunState) string {
	marker := filepath.Join(cfg.DataDir(), "runs", run.ID, pipeline.PauseMarker)
	if _, err := os.Stat(marker); err == nil {
		return "pausa manual solicitada"
	}
	if gate := escalatedGate(run); gate != "" {
		return fmt.Sprintf("gate %s escalado; validação automática falhou", gate)
	}
	return "pausada"
}

func eventInsights(events []map[string]any) (time.Time, []string) {
	var latest time.Time
	var blockers []string
	for _, event := range events {
		if raw, ok := event["time"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil && parsed.After(latest) {
				latest = parsed
			}
		}
		if event["name"] != "validation.rejected" {
			continue
		}
		values, ok := event["blockers"].([]any)
		if !ok {
			continue
		}
		blockers = blockers[:0]
		for _, value := range values {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				blockers = append(blockers, text)
			}
			if len(blockers) >= 8 {
				break
			}
		}
	}
	return latest, blockers
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
