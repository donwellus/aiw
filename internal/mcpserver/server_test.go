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
	for _, want := range []string{"status", "report", "runs", "run_show", "events"} {
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
	for _, want := range []string{"pause", "resume", "approve", "release", "clean_apply", "operation_cancel", "cancel", "answer", "questions"} {
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
