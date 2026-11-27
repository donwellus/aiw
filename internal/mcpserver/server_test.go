package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/control"
	"github.com/donwellus/aiw/internal/engine"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
)

type testResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func newTestServer(t *testing.T) (*Server, *config.Config, *state.Store) {
	t.Helper()
	cfg := &config.Config{
		Root:     t.TempDir(),
		Defaults: config.Defaults{ClaimTTL: config.Duration(time.Hour)},
	}
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	run := &pipeline.RunState{
		ID:        "run-1",
		Task:      source.Task{Source: "folder", ExternalID: "task-1", Title: "Tarefa 1", ClaimKey: "folder:task-1"},
		Source:    "folder",
		Repo:      cfg.Root,
		Worktree:  filepath.Join(cfg.WorktreesDir(), "run-1"),
		Branch:    "aiw/run-1",
		Status:    "succeeded",
		Stage:     "done",
		Usage:     engine.Usage{CostUSD: 1.25, InputTokens: 10, OutputTokens: 5},
		CreatedAt: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC),
	}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "task-1", Status: "succeeded", RunID: run.ID, Key: "folder:task-1"}); err != nil {
		t.Fatal(err)
	}
	event := `{"time":"2026-11-22T12:00:00Z","name":"run.start","run_id":"old"}` + "\n" +
		`{"time":"2026-11-22T12:01:00Z","name":"run.succeeded"}` + "\n"
	if err := os.WriteFile(filepath.Join(cfg.RunsDir(), run.ID, "events.jsonl"), []byte(event), 0o644); err != nil {
		t.Fatal(err)
	}
	return New(cfg, store, "test", false, strings.NewReader(""), &bytes.Buffer{}), cfg, store
}

func serve(t *testing.T, server *Server, input string) []testResponse {
	t.Helper()
	var out bytes.Buffer
	server.in = strings.NewReader(input)
	server.out = &out
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	responses := make([]testResponse, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var response testResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("resposta inválida %q: %v", line, err)
		}
		responses = append(responses, response)
	}
	return responses
}

func TestInitializeListAndReadTools(t *testing.T) {
	server, _, _ := newTestServer(t)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"status","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"runs","arguments":{"limit":5}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"run_show","arguments":{"run_id":"run-1"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"events","arguments":{"limit":10}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"run_show","arguments":{"run_id":"../escape"}}}`,
	}, "\n") + "\n"
	responses := serve(t, server, input)
	if len(responses) != 7 {
		t.Fatalf("respostas = %d, esperado 7", len(responses))
	}
	if string(responses[0].ID) != "1" || responses[0].Error != nil {
		t.Fatalf("initialize inesperado: %+v", responses[0])
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(responses[0].Result, &init); err != nil {
		t.Fatal(err)
	}
	if init.ProtocolVersion != protocolVersion || init.ServerInfo.Name != "aiw" {
		t.Fatalf("initialize = %+v", init)
	}

	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(responses[1].Result, &listed); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"status", "report", "runs", "run_show", "events", "refine_preview", "sources", "prepare_issue"} {
		if !names[want] {
			t.Fatalf("tool %q ausente: %+v", want, names)
		}
	}

	status := toolStructured(t, responses[2])
	summary := status["summary"].(map[string]any)
	if int(summary["tasks"].(float64)) != 1 {
		t.Fatalf("status summary = %+v", summary)
	}
	runs := toolStructured(t, responses[3])
	if int(runs["total"].(float64)) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
	runShow := toolStructured(t, responses[4])
	if runShow["run"] == nil {
		t.Fatalf("run_show sem run: %+v", runShow)
	}
	events := toolStructured(t, responses[5])
	if int(events["total"].(float64)) != 2 {
		t.Fatalf("events = %+v", events)
	}
	if responses[6].Error == nil || responses[6].Error.Code != -32602 {
		t.Fatalf("run_id inválido deveria falhar: %+v", responses[6])
	}
}

func TestParseErrorAndUnknownMethod(t *testing.T) {
	server, _, _ := newTestServer(t)
	input := "{not-json}\n" +
		`{"jsonrpc":"2.0","id":2,"method":"missing"}` + "\n"
	responses := serve(t, server, input)
	if len(responses) != 2 {
		t.Fatalf("respostas = %d", len(responses))
	}
	if responses[0].Error == nil || responses[0].Error.Code != -32700 {
		t.Fatalf("parse error inesperado: %+v", responses[0])
	}
	if responses[1].Error == nil || responses[1].Error.Code != -32601 {
		t.Fatalf("method not found inesperado: %+v", responses[1])
	}
}

func toolStructured(t *testing.T, response testResponse) map[string]any {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("erro inesperado: %+v", response.Error)
	}
	var result struct {
		StructuredContent map[string]any `json:"structuredContent"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.StructuredContent == nil {
		t.Fatalf("sem structuredContent: %s", response.Result)
	}
	return result.StructuredContent
}

func TestWriteTools(t *testing.T) {
	server, cfg, _ := newTestServer(t)
	server.allowWrite = true
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"pause","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"clean_preview","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"resume","arguments":{"run_id":"run-1"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"clean_apply","arguments":{}}}`,
	}, "\n") + "\n"
	responses := serve(t, server, input)
	if len(responses) != 5 {
		t.Fatalf("respostas = %d", len(responses))
	}
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(responses[0].Result, &listed); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"pause", "resume", "approve", "release", "clean_apply", "operation_cancel", "cancel", "answer", "refine", "create_issue", "questions", "situation"} {
		if !names[want] {
			t.Fatalf("tool %q ausente", want)
		}
	}
	paused := toolStructured(t, responses[1])
	if paused["scope"] != "global" {
		t.Fatalf("pause = %+v", paused)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir(), "PAUSE")); err != nil {
		t.Fatalf("marker global ausente: %v", err)
	}
	toolStructured(t, responses[2])
	if responses[3].Error != nil {
		t.Fatalf("resume deveria retornar erro de ferramenta, não protocolo: %+v", responses[3].Error)
	}
	var resumeResult struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(responses[3].Result, &resumeResult); err != nil {
		t.Fatal(err)
	}
	if !resumeResult.IsError {
		t.Fatal("resume sem daemon deveria falhar")
	}
	if responses[4].Error == nil || responses[4].Error.Code != -32602 {
		t.Fatalf("clean_apply sem confirm deveria falhar: %+v", responses[4])
	}
}

func TestRunsCurrentAndAll(t *testing.T) {
	server, cfg, _ := newTestServer(t)
	old := &pipeline.RunState{
		ID:        "run-old",
		Task:      source.Task{Source: "folder", ExternalID: "task-1", Title: "Tarefa 1", ClaimKey: "folder:task-1"},
		Source:    "folder",
		Status:    "needs_input",
		Stage:     "intake",
		CreatedAt: time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC),
	}
	if err := pipeline.SaveRun(cfg, old); err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"runs","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"runs","arguments":{"all":true}}}`,
	}, "\n") + "\n"
	responses := serve(t, server, input)
	current := toolStructured(t, responses[0])
	if int(current["total"].(float64)) != 1 {
		t.Fatalf("runs atuais = %+v", current)
	}
	all := toolStructured(t, responses[1])
	if int(all["total"].(float64)) != 2 {
		t.Fatalf("runs histórico = %+v", all)
	}
}

func TestSituationIncludesQueuePauseAndCapacity(t *testing.T) {
	server, cfg, store := newTestServer(t)
	now := time.Now().UTC()
	paused := &pipeline.RunState{
		ID:        "run-paused",
		Task:      source.Task{Source: "folder", ExternalID: "task-paused", Title: "Pausada"},
		Source:    "folder",
		Status:    "paused",
		Stage:     "requirements",
		Gates:     map[string]string{"spec": "escalated", "plan": "pending"},
		CreatedAt: now.Add(-time.Hour),
	}
	if err := pipeline.SaveRun(cfg, paused); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "task-paused", Status: "paused", RunID: paused.ID}); err != nil {
		t.Fatal(err)
	}
	events := `{"time":"` + now.Add(-2*time.Minute).Format(time.RFC3339Nano) + `","name":"validation.rejected","blockers":["BLOCKING: falta critério","BLOCKING: contrato ambíguo"]}` + "\n"
	if err := os.WriteFile(filepath.Join(cfg.RunsDir(), paused.ID, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "task-queued", Status: "queued"}); err != nil {
		t.Fatal(err)
	}
	if err := control.WriteDaemonInfo(cfg, control.DaemonInfo{PID: os.Getpid(), Workers: 2, Interval: "1m", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	ops, err := control.OpenOperationStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Enqueue(control.ActionResume, paused.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.ClaimNext(); err != nil {
		t.Fatal(err)
	}

	responses := serve(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"situation","arguments":{}}}`+"\n")
	situation := toolStructured(t, responses[0])
	daemon := situation["daemon"].(map[string]any)
	if daemon["active"] != true || int(daemon["worker_slots"].(float64)) != 2 {
		t.Fatalf("daemon = %+v", daemon)
	}
	capacity := situation["capacity"].(map[string]any)
	if int(capacity["active_control_operations"].(float64)) != 1 || int(capacity["queued_tasks"].(float64)) != 1 {
		t.Fatalf("capacity = %+v", capacity)
	}
	queued := situation["queued"].([]any)
	if len(queued) != 1 {
		t.Fatalf("queued = %+v", queued)
	}
	pausedTasks := situation["paused"].([]any)
	if len(pausedTasks) != 1 {
		t.Fatalf("paused = %+v", pausedTasks)
	}
	pausedTask := pausedTasks[0].(map[string]any)
	if pausedTask["gate"] != "spec" || !strings.Contains(pausedTask["reason"].(string), "gate spec") {
		t.Fatalf("paused task = %+v", pausedTask)
	}
	blockers := pausedTask["blockers"].([]any)
	if len(blockers) != 2 {
		t.Fatalf("blockers = %+v", blockers)
	}
}

func TestRefinePreviewRequiresConfirmationToken(t *testing.T) {
	server, cfg, _ := newTestServer(t)
	worktree := filepath.Join(cfg.WorktreesDir(), "run-refine")
	if err := os.MkdirAll(filepath.Join(worktree, ".aiw-run"), 0o755); err != nil {
		t.Fatal(err)
	}
	paused := &pipeline.RunState{
		ID:        "run-refine",
		Task:      source.Task{Source: "folder", ExternalID: "task-refine", Title: "Refine"},
		Source:    "folder",
		Repo:      cfg.Root,
		Worktree:  worktree,
		Branch:    "aiw/run-refine",
		Status:    "paused",
		Stage:     "architect",
		Gates:     map[string]string{"intake": "approved", "spec": "approved", "plan": "escalated"},
		CreatedAt: time.Now().UTC(),
	}
	for _, name := range []string{"intake", "requirements", "architect", "implement", "test", "review", "deliver"} {
		status := "pending"
		switch name {
		case "intake", "requirements":
			status = "done"
		case "architect":
			status = "gated"
		}
		paused.Stages = append(paused.Stages, pipeline.StageState{Name: name, Status: status})
	}
	if err := pipeline.SaveRun(cfg, paused); err != nil {
		t.Fatal(err)
	}

	previewResponse := serve(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"refine_preview","arguments":{"run_id":"run-refine","target":"plan","feedback":"Ajuste o plano."}}}`+"\n")[0]
	preview := toolStructured(t, previewResponse)
	previewID, _ := preview["preview_id"].(string)
	if previewID == "" || preview["target"] != "plan" || preview["feedback"] != "Ajuste o plano." {
		t.Fatalf("preview = %+v", preview)
	}
	ops, err := control.OpenOperationStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if operations, err := ops.List(0); err != nil || len(operations) != 0 {
		t.Fatalf("preview enfileirou operação: %+v err=%v", operations, err)
	}

	server.allowWrite = true
	if err := control.WriteDaemonInfo(cfg, control.DaemonInfo{PID: os.Getpid(), Workers: 1, Interval: "1m", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"refine","arguments":{"run_id":"run-refine","target":"plan","feedback":"Ajuste o plano."}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"refine","arguments":{"run_id":"run-refine","target":"plan","feedback":"Outro feedback.","preview_id":"` + previewID + `"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"refine","arguments":{"run_id":"run-refine","target":"plan","feedback":"Ajuste o plano.","preview_id":"` + previewID + `"}}}`,
	}, "\n") + "\n"
	responses := serve(t, server, input)
	for _, response := range responses[:2] {
		var result struct {
			IsError bool `json:"isError"`
		}
		if response.Error != nil || json.Unmarshal(response.Result, &result) != nil || !result.IsError {
			t.Fatalf("refine sem preview válido deveria falhar: %+v", response)
		}
	}
	toolStructured(t, responses[2])
	operations, err := ops.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].Action != control.ActionRefine {
		t.Fatalf("operações = %+v", operations)
	}
	if operations[0].Args["preview_id"] != previewID {
		t.Fatalf("preview_id não persistido: %+v", operations[0].Args)
	}
}

func TestPrepareAndCreateIssue(t *testing.T) {
	server, cfg, _ := newTestServer(t)
	cfg.DefaultSource = "folder"
	cfg.Sources = map[string]config.SourceConfig{
		"folder": {Driver: "folder", Path: "./tasks"},
	}
	cfg.Workspace.DefaultRepo = "."
	server.allowWrite = true

	prepareArgs := map[string]any{
		"title":       "Nova issue",
		"description": "Contexto completo da issue.",
		"acceptance":  []string{"O fluxo funciona."},
	}
	preparePayload, _ := json.Marshal(prepareArgs)
	prepared := toolStructured(t, serve(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"prepare_issue","arguments":`+string(preparePayload)+`}}`+"\n")[0])
	previewID, _ := prepared["preview_id"].(string)
	missing, _ := prepared["missing"].([]any)
	if previewID == "" || len(missing) != 0 {
		t.Fatalf("preview = %+v", prepared)
	}

	createArgs := map[string]any{
		"preview_id":  previewID,
		"title":       "Nova issue",
		"description": "Contexto completo da issue.",
		"acceptance":  []string{"O fluxo funciona."},
	}
	createPayload, _ := json.Marshal(createArgs)
	created := toolStructured(t, serve(t, server, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_issue","arguments":`+string(createPayload)+`}}`+"\n")[0])
	if created["source"] != "folder" || created["external_id"] == "" {
		t.Fatalf("created = %+v", created)
	}
	entries, err := os.ReadDir(filepath.Join(cfg.Root, "tasks"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("tasks = %+v err=%v", entries, err)
	}
}

func TestCreateIssueRequiresIncompleteAcknowledgement(t *testing.T) {
	server, cfg, _ := newTestServer(t)
	cfg.DefaultSource = "folder"
	cfg.Sources = map[string]config.SourceConfig{
		"folder": {Driver: "folder", Path: "./tasks"},
	}
	cfg.Workspace.DefaultRepo = "."
	server.allowWrite = true

	prepareArgs := map[string]any{
		"title":       "Issue sem aceite",
		"description": "Contexto suficiente.",
	}
	preparePayload, _ := json.Marshal(prepareArgs)
	prepared := toolStructured(t, serve(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"prepare_issue","arguments":`+string(preparePayload)+`}}`+"\n")[0])
	previewID, _ := prepared["preview_id"].(string)
	missing, _ := prepared["missing"].([]any)
	if len(missing) != 1 || missing[0] != "acceptance" {
		t.Fatalf("missing = %+v", missing)
	}

	blockedPayload, _ := json.Marshal(map[string]any{
		"preview_id": previewID, "title": "Issue sem aceite", "description": "Contexto suficiente.",
	})
	blocked := serve(t, server, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_issue","arguments":`+string(blockedPayload)+`}}`+"\n")[0]
	var blockedResult struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(blocked.Result, &blockedResult); err != nil || !blockedResult.IsError {
		t.Fatalf("criação incompleta deveria falhar: %+v", blocked)
	}

	allowedPayload, _ := json.Marshal(map[string]any{
		"preview_id": previewID, "title": "Issue sem aceite", "description": "Contexto suficiente.",
		"allow_incomplete": true, "acknowledged_missing": []string{"acceptance"},
	})
	allowed := toolStructured(t, serve(t, server, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_issue","arguments":`+string(allowedPayload)+`}}`+"\n")[0])
	if allowed["external_id"] == "" {
		t.Fatalf("allowed = %+v", allowed)
	}
}
