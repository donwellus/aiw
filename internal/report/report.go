package report

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pause"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
	"github.com/donwellus/aiw/internal/taskkey"
)

type SourceTask struct {
	Task     *source.Task
	Location string
}

type Options struct {
	Source      string
	SourceTasks []SourceTask
}

type Report struct {
	GeneratedAt time.Time `json:"generated_at" yaml:"generated_at"`
	Root        string    `json:"root" yaml:"root"`
	Paused      bool      `json:"paused" yaml:"paused"`
	Source      string    `json:"source,omitempty" yaml:"source,omitempty"`
	Summary     Summary   `json:"summary" yaml:"summary"`
	Tasks       []Task    `json:"tasks" yaml:"tasks"`
	Claims      []Claim   `json:"claims" yaml:"claims"`
	Alerts      []Alert   `json:"alerts" yaml:"alerts"`
}

type Summary struct {
	Tasks        int            `json:"tasks" yaml:"tasks"`
	ByStatus     map[string]int `json:"by_status" yaml:"by_status"`
	Runs         int            `json:"runs" yaml:"runs"`
	CostUSD      float64        `json:"cost_usd" yaml:"cost_usd"`
	TaskCostUSD  float64        `json:"task_cost_usd" yaml:"task_cost_usd"`
	InputTokens  int            `json:"input_tokens" yaml:"input_tokens"`
	OutputTokens int            `json:"output_tokens" yaml:"output_tokens"`
	ActiveClaims int            `json:"active_claims" yaml:"active_claims"`
	StaleClaims  int            `json:"stale_claims" yaml:"stale_claims"`
}

type StatusCount struct {
	Status string `json:"status" yaml:"status"`
	Count  int    `json:"count" yaml:"count"`
}

type Task struct {
	ID           string    `json:"id" yaml:"id"`
	Title        string    `json:"title" yaml:"title"`
	Source       string    `json:"source" yaml:"source"`
	Status       string    `json:"status" yaml:"status"`
	Stage        string    `json:"stage,omitempty" yaml:"stage,omitempty"`
	RunID        string    `json:"run_id,omitempty" yaml:"run_id,omitempty"`
	Location     string    `json:"location,omitempty" yaml:"location,omitempty"`
	CostUSD      float64   `json:"cost_usd" yaml:"cost_usd"`
	InputTokens  int       `json:"input_tokens" yaml:"input_tokens"`
	OutputTokens int       `json:"output_tokens" yaml:"output_tokens"`
	UpdatedAt    time.Time `json:"updated_at" yaml:"updated_at"`
	PRURL        string    `json:"pr_url,omitempty" yaml:"pr_url,omitempty"`
	Error        string    `json:"error,omitempty" yaml:"error,omitempty"`
}

type Claim struct {
	TaskID     string `json:"task_id" yaml:"task_id"`
	RunID      string `json:"run_id,omitempty" yaml:"run_id,omitempty"`
	PID        int    `json:"pid" yaml:"pid"`
	AgeSeconds int64  `json:"age_seconds" yaml:"age_seconds"`
	Stale      bool   `json:"stale" yaml:"stale"`
}

type Alert struct {
	Level   string `json:"level" yaml:"level"`
	Kind    string `json:"kind" yaml:"kind"`
	TaskID  string `json:"task_id,omitempty" yaml:"task_id,omitempty"`
	RunID   string `json:"run_id,omitempty" yaml:"run_id,omitempty"`
	Message string `json:"message" yaml:"message"`
}

var statusOrder = map[string]int{
	"running":     0,
	"paused":      1,
	"needs_input": 2,
	"failed":      3,
	"todo":        4,
	"canceled":    5,
	"succeeded":   6,
}

func (s Summary) OrderedStatuses() []StatusCount {
	statuses := make([]StatusCount, 0, len(s.ByStatus))
	for status, count := range s.ByStatus {
		statuses = append(statuses, StatusCount{Status: status, Count: count})
	}
	sort.Slice(statuses, func(i, j int) bool {
		left, lok := statusOrder[statuses[i].Status]
		right, rok := statusOrder[statuses[j].Status]
		if !lok {
			left = len(statusOrder)
		}
		if !rok {
			right = len(statusOrder)
		}
		if left != right {
			return left < right
		}
		return statuses[i].Status < statuses[j].Status
	})
	return statuses
}

func Build(cfg *config.Config, store *state.Store, opts Options) (*Report, error) {
	now := time.Now().UTC()
	keys := taskkey.New(cfg)
	report := &Report{
		GeneratedAt: now,
		Root:        cfg.Root,
		Paused:      pause.Active(cfg.DataDir()),
		Source:      opts.Source,
		Summary:     Summary{ByStatus: map[string]int{}},
		Tasks:       []Task{},
		Claims:      []Claim{},
		Alerts:      []Alert{},
	}
	runs, err := loadRuns(cfg)
	if err != nil {
		return nil, err
	}
	runsByID := make(map[string]*pipeline.RunState, len(runs))
	latestByKey := make(map[string]*pipeline.RunState, len(runs))
	for _, run := range runs {
		report.Summary.Runs++
		report.Summary.CostUSD += run.Usage.CostUSD
		report.Summary.InputTokens += run.Usage.InputTokens
		report.Summary.OutputTokens += run.Usage.OutputTokens
		runsByID[run.ID] = run
		key := keys.Key(firstNonEmpty(run.Task.Source, run.Source), run.Task.ExternalID, run.Task.ClaimKey)
		if current, ok := latestByKey[key]; !ok || runTime(run).After(runTime(current)) {
			latestByKey[key] = run
		}
	}

	tasks := make(map[string]Task)
	records := latestRecords(store.All(), keys)
	var missing []Alert
	for key, record := range records {
		row := Task{
			ID:        record.ID,
			Source:    record.Source,
			Status:    record.Status,
			UpdatedAt: record.UpdatedAt,
		}
		var run *pipeline.RunState
		if record.RunID != "" {
			run = runsByID[record.RunID]
			if run == nil {
				missing = append(missing, Alert{
					Level:   "error",
					Kind:    "missing_run",
					TaskID:  record.ID,
					RunID:   record.RunID,
					Message: "run.json não encontrado para o estado atual",
				})
			}
		} else {
			run = latestByKey[key]
		}
		applyRun(&row, run)
		if record.Status == "todo" && run != nil && run.Status != "" {
			row.Status = run.Status
		}
		if row.Status == "" {
			row.Status = "todo"
		}
		tasks[key] = row
	}

	for _, snapshot := range opts.SourceTasks {
		if snapshot.Task == nil {
			continue
		}
		sourceName := firstNonEmpty(snapshot.Task.Source, opts.Source)
		key := keys.Key(sourceName, snapshot.Task.ExternalID, snapshot.Task.ClaimKey)
		row, ok := tasks[key]
		if !ok {
			row = Task{
				ID:        snapshot.Task.ExternalID,
				Title:     snapshot.Task.Title,
				Source:    sourceName,
				Status:    statusFromLocation(snapshot.Location),
				UpdatedAt: snapshot.Task.UpdatedAt,
			}
			if run := latestByKey[key]; run != nil {
				applyRun(&row, run)
				if run.Status != "" {
					row.Status = run.Status
				}
			}
		}
		if row.Title == "" {
			row.Title = snapshot.Task.Title
		}
		if row.Location == "" {
			row.Location = snapshot.Location
		}
		tasks[key] = row
	}

	ttl := cfg.Defaults.ClaimTTL.Std()
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	for _, claim := range store.Claims() {
		age := now.Sub(claim.CreatedAt)
		if age < 0 {
			age = 0
		}
		stale := age > ttl
		if stale {
			report.Summary.StaleClaims++
		}
		report.Claims = append(report.Claims, Claim{
			TaskID:     claim.ID,
			RunID:      claim.RunID,
			PID:        claim.PID,
			AgeSeconds: int64(age.Seconds()),
			Stale:      stale,
		})
	}
	sort.Slice(report.Claims, func(i, j int) bool {
		if report.Claims[i].Stale != report.Claims[j].Stale {
			return report.Claims[i].Stale
		}
		return report.Claims[i].AgeSeconds > report.Claims[j].AgeSeconds
	})
	report.Summary.ActiveClaims = len(report.Claims)

	for _, row := range tasks {
		report.Summary.Tasks++
		report.Summary.ByStatus[row.Status]++
		report.Summary.TaskCostUSD += row.CostUSD
		report.Tasks = append(report.Tasks, row)
	}
	sortTasks(report.Tasks)
	report.Alerts = append(report.Alerts, missing...)
	report.Alerts = append(report.Alerts, buildAlerts(report, report.Tasks)...)
	sortAlerts(report.Alerts)
	return report, nil
}

func loadRuns(cfg *config.Config) ([]*pipeline.RunState, error) {
	entries, err := os.ReadDir(cfg.RunsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	runs := make([]*pipeline.RunState, 0, len(entries))
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
	return runs, nil
}

func latestRecords(records []state.TaskRecord, keys *taskkey.Keyer) map[string]state.TaskRecord {
	latest := make(map[string]state.TaskRecord, len(records))
	for _, record := range records {
		key := keys.Key(record.Source, record.ID, record.Key)
		current, ok := latest[key]
		if !ok || record.UpdatedAt.After(current.UpdatedAt) {
			latest[key] = record
		}
	}
	return latest
}

func runTime(run *pipeline.RunState) time.Time {
	if run == nil {
		return time.Time{}
	}
	if !run.CreatedAt.IsZero() {
		return run.CreatedAt
	}
	return run.UpdatedAt
}

func applyRun(row *Task, run *pipeline.RunState) {
	if run == nil {
		return
	}
	row.RunID = run.ID
	row.Stage = run.Stage
	if row.Title == "" {
		row.Title = run.Task.Title
	}
	if row.Source == "" {
		row.Source = firstNonEmpty(run.Task.Source, run.Source)
	}
	row.CostUSD = run.Usage.CostUSD
	row.InputTokens = run.Usage.InputTokens
	row.OutputTokens = run.Usage.OutputTokens
	row.PRURL = run.PRURL
	row.Error = run.Error
	if run.UpdatedAt.After(row.UpdatedAt) {
		row.UpdatedAt = run.UpdatedAt
	}
}

func statusFromLocation(location string) string {
	switch location {
	case "done":
		return "succeeded"
	case "failed":
		return "failed"
	case "doing":
		return "running"
	default:
		return "todo"
	}
}

func sortTasks(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		left, lok := statusOrder[tasks[i].Status]
		right, rok := statusOrder[tasks[j].Status]
		if !lok {
			left = len(statusOrder)
		}
		if !rok {
			right = len(statusOrder)
		}
		if left != right {
			return left < right
		}
		if !tasks[i].UpdatedAt.Equal(tasks[j].UpdatedAt) {
			return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt)
		}
		if tasks[i].ID != tasks[j].ID {
			return tasks[i].ID < tasks[j].ID
		}
		return tasks[i].Source < tasks[j].Source
	})
}

func buildAlerts(report *Report, tasks []Task) []Alert {
	alerts := make([]Alert, 0)
	if report.Paused {
		alerts = append(alerts, Alert{
			Level:   "warn",
			Kind:    "global_pause",
			Message: "pausa global ativa (.aiw/PAUSE)",
		})
	}
	for _, task := range tasks {
		switch task.Status {
		case "failed":
			message := task.Error
			if message == "" {
				message = "execução falhou"
			}
			alerts = append(alerts, Alert{
				Level:   "error",
				Kind:    "failed",
				TaskID:  task.ID,
				RunID:   task.RunID,
				Message: message,
			})
		case "needs_input":
			alerts = append(alerts, Alert{
				Level:   "warn",
				Kind:    "needs_input",
				TaskID:  task.ID,
				RunID:   task.RunID,
				Message: "tarefa aguarda input",
			})
		case "paused":
			alerts = append(alerts, Alert{
				Level:   "warn",
				Kind:    "paused",
				TaskID:  task.ID,
				RunID:   task.RunID,
				Message: fmt.Sprintf("pausada no estágio %s", firstNonEmpty(task.Stage, "-")),
			})
		}
	}
	for _, claim := range report.Claims {
		if !claim.Stale {
			continue
		}
		alerts = append(alerts, Alert{
			Level:   "warn",
			Kind:    "stale_claim",
			TaskID:  claim.TaskID,
			RunID:   claim.RunID,
			Message: fmt.Sprintf("claim vencido há %s", formatAge(claim.AgeSeconds)),
		})
	}
	return alerts
}

func sortAlerts(alerts []Alert) {
	levelOrder := map[string]int{"error": 0, "warn": 1, "info": 2}
	sort.SliceStable(alerts, func(i, j int) bool {
		left, lok := levelOrder[alerts[i].Level]
		right, rok := levelOrder[alerts[j].Level]
		if !lok {
			left = len(levelOrder)
		}
		if !rok {
			right = len(levelOrder)
		}
		if left != right {
			return left < right
		}
		if alerts[i].Kind != alerts[j].Kind {
			return alerts[i].Kind < alerts[j].Kind
		}
		if alerts[i].TaskID != alerts[j].TaskID {
			return alerts[i].TaskID < alerts[j].TaskID
		}
		return alerts[i].Message < alerts[j].Message
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func formatAge(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%dh%dm", seconds/3600, (seconds%3600)/60)
}
