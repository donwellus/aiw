package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/report"
)

func toolDefinitions() []map[string]any {
	readOnly := map[string]any{
		"readOnlyHint":    true,
		"destructiveHint": false,
		"idempotentHint":  true,
		"openWorldHint":   false,
	}
	return []map[string]any{
		{
			"name":        "status",
			"title":       "Status do aiw",
			"description": "Resumo do estado atual: pausa global, contagem por status, tarefas ativas, claims e alertas.",
			"inputSchema": objectSchema(map[string]any{}),
			"annotations": readOnly,
		},
		{
			"name":        "report",
			"title":       "Relatório do aiw",
			"description": "Relatório completo read-only de tarefas, runs, custos, claims e alertas.",
			"inputSchema": objectSchema(map[string]any{
				"summary_only": map[string]any{"type": "boolean", "description": "Retorna apenas resumo e alertas"},
			}),
			"annotations": readOnly,
		},
		{
			"name":        "runs",
			"title":       "Listar runs",
			"description": "Lista runs registradas, mais recentes primeiro.",
			"inputSchema": objectSchema(map[string]any{
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 20},
				"status": map[string]any{"type": "string", "description": "Filtra por status exato"},
			}),
			"annotations": readOnly,
		},
		{
			"name":        "run_show",
			"title":       "Detalhar run",
			"description": "Mostra run.json e os últimos eventos de uma execução.",
			"inputSchema": objectSchema(map[string]any{
				"run_id":       map[string]any{"type": "string", "description": "ID da run"},
				"events_limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 20},
			}, "run_id"),
			"annotations": readOnly,
		},
		{
			"name":        "events",
			"title":       "Eventos recentes",
			"description": "Lista eventos recentes de uma run ou de todas as runs.",
			"inputSchema": objectSchema(map[string]any{
				"run_id": map[string]any{"type": "string", "description": "Opcional: limita a uma run"},
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 20},
			}),
			"annotations": readOnly,
		},
	}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func (s *Server) callTool(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, invalidParams(err)
	}
	switch params.Name {
	case "status":
		return s.toolStatus(ctx)
	case "report":
		return s.toolReport(ctx, params.Arguments)
	case "runs":
		return s.toolRuns(ctx, params.Arguments)
	case "run_show":
		return s.toolRunShow(ctx, params.Arguments)
	case "events":
		return s.toolEvents(ctx, params.Arguments)
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown tool", Data: params.Name}
	}
}

func (s *Server) toolStatus(ctx context.Context) (any, *rpcError) {
	built, err := report.Build(s.cfg, s.store, report.Options{})
	if err != nil {
		return toolError(err.Error()), nil
	}
	active := make([]report.Task, 0)
	for _, task := range built.Tasks {
		switch task.Status {
		case "running", "paused", "needs_input":
			active = append(active, task)
		}
	}
	result, err := toolResult(map[string]any{
		"generated_at": built.GeneratedAt,
		"root":         built.Root,
		"paused":       built.Paused,
		"summary":      built.Summary,
		"active":       active,
		"claims":       built.Claims,
		"alerts":       built.Alerts,
	})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) toolReport(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		SummaryOnly bool `json:"summary_only"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, invalidParams(err)
		}
	}
	built, err := report.Build(s.cfg, s.store, report.Options{})
	if err != nil {
		return toolError(err.Error()), nil
	}
	var value any = built
	if args.SummaryOnly {
		value = map[string]any{
			"generated_at": built.GeneratedAt,
			"root":         built.Root,
			"paused":       built.Paused,
			"source":       built.Source,
			"summary":      built.Summary,
			"alerts":       built.Alerts,
		}
	}
	result, err := toolResult(value)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

type runSummary struct {
	RunID        string    `json:"run_id"`
	TaskID       string    `json:"task_id"`
	Title        string    `json:"title"`
	Source       string    `json:"source,omitempty"`
	Status       string    `json:"status"`
	Stage        string    `json:"stage,omitempty"`
	Repo         string    `json:"repo,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	Worktree     string    `json:"worktree,omitempty"`
	CostUSD      float64   `json:"cost_usd"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	PRURL        string    `json:"pr_url,omitempty"`
	Error        string    `json:"error,omitempty"`
}

func (s *Server) toolRuns(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		Limit  int    `json:"limit"`
		Status string `json:"status"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, invalidParams(err)
		}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	runs, err := s.loadRuns()
	if err != nil {
		return toolError(err.Error()), nil
	}
	sort.Slice(runs, func(i, j int) bool {
		left := runTimestamp(runs[i])
		right := runTimestamp(runs[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return runs[i].ID > runs[j].ID
	})
	filtered := make([]*pipeline.RunState, 0, len(runs))
	for _, run := range runs {
		if args.Status != "" && run.Status != args.Status {
			continue
		}
		filtered = append(filtered, run)
	}
	total := len(filtered)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	out := make([]runSummary, 0, len(filtered))
	for _, run := range filtered {
		out = append(out, runSummary{
			RunID:        run.ID,
			TaskID:       run.Task.ExternalID,
			Title:        run.Task.Title,
			Source:       run.Source,
			Status:       run.Status,
			Stage:        run.Stage,
			Repo:         run.Repo,
			Branch:       run.Branch,
			Worktree:     run.Worktree,
			CostUSD:      run.Usage.CostUSD,
			InputTokens:  run.Usage.InputTokens,
			OutputTokens: run.Usage.OutputTokens,
			CreatedAt:    run.CreatedAt,
			UpdatedAt:    run.UpdatedAt,
			PRURL:        run.PRURL,
			Error:        run.Error,
		})
	}
	result, err := toolResult(map[string]any{"total": total, "runs": out})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) toolRunShow(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID       string `json:"run_id"`
		EventsLimit int    `json:"events_limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	if !validRunID(args.RunID) {
		return nil, invalidParams(errors.New("run_id inválido"))
	}
	run, err := pipeline.LoadRun(s.cfg, args.RunID)
	if err != nil {
		return toolError("run não encontrada: " + err.Error()), nil
	}
	limit := args.EventsLimit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	events := s.readRunEvents(args.RunID)
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	result, err := toolResult(map[string]any{"run": run, "events": events})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) toolEvents(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID string `json:"run_id"`
		Limit int    `json:"limit"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, invalidParams(err)
		}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	var events []map[string]any
	if args.RunID != "" {
		if !validRunID(args.RunID) {
			return nil, invalidParams(errors.New("run_id inválido"))
		}
		events = s.readRunEvents(args.RunID)
	} else {
		entries, err := os.ReadDir(s.cfg.RunsDir())
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return toolError(err.Error()), nil
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			events = append(events, s.readRunEvents(entry.Name())...)
		}
	}
	sort.Slice(events, func(i, j int) bool {
		left, _ := events[i]["time"].(string)
		right, _ := events[j]["time"].(string)
		if left == right {
			return false
		}
		return left > right
	})
	total := len(events)
	if len(events) > limit {
		events = events[:limit]
	}
	result, err := toolResult(map[string]any{"total": total, "events": events})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) loadRuns() ([]*pipeline.RunState, error) {
	entries, err := os.ReadDir(s.cfg.RunsDir())
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
		run, err := pipeline.LoadRun(s.cfg, entry.Name())
		if err != nil {
			continue
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *Server) readRunEvents(runID string) []map[string]any {
	path := filepath.Join(s.cfg.RunsDir(), runID, "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	events := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		event["run_id"] = runID
		events = append(events, event)
	}
	return events
}

func runTimestamp(run *pipeline.RunState) time.Time {
	if run == nil {
		return time.Time{}
	}
	if !run.CreatedAt.IsZero() {
		return run.CreatedAt
	}
	return run.UpdatedAt
}
