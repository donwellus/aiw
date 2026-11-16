package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/donwellus/aiw/internal/config"
)

func TestParseClaudeOutput(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"sess-1","total_cost_usd":0.0123,"usage":{"input_tokens":10,"output_tokens":5}}`)
	parsed, err := parseClaudeOutput(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Result != "ok" || parsed.SessionID != "sess-1" {
		t.Fatalf("parse inesperado: %+v", parsed)
	}
	if parsed.TotalCostUSD != 0.0123 || parsed.Usage.InputTokens != 10 || parsed.Usage.OutputTokens != 5 {
		t.Fatalf("usage inesperado: %+v", parsed)
	}
	if parsed.IsError {
		t.Fatal("IsError deveria ser false")
	}
}

func TestParseOpencodeEvents(t *testing.T) {
	raw := []byte(`{"type":"session","sessionID":"ses_123"}
{"type":"text","text":"Analisando o repositório"}
{"type":"step_finish","cost":0.002,"tokens":{"input":120,"output":30}}
{"type":"text","text":"Concluído"}
`)
	acc := parseOpencodeEvents(raw)
	if acc.sessionID != "ses_123" {
		t.Fatalf("sessionID = %q", acc.sessionID)
	}
	if acc.cost != 0.002 {
		t.Fatalf("cost = %v", acc.cost)
	}
	if acc.inputTokens != 120 || acc.outputTokens != 30 {
		t.Fatalf("tokens = %d/%d", acc.inputTokens, acc.outputTokens)
	}
	text := acc.text()
	if text == "" {
		t.Fatal("texto não extraído")
	}
}

func TestFakeEngine(t *testing.T) {
	fake := NewFake("fake")
	dir := t.TempDir()
	res, err := fake.Run(context.Background(), Request{Stage: "implement", Workdir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output == "" {
		t.Fatal("saída vazia")
	}
	if _, err := os.Stat(filepath.Join(dir, "AIW_FAKE_CHANGE.md")); err != nil {
		t.Fatalf("arquivo fake não criado: %v", err)
	}
	res, err = fake.Run(context.Background(), Request{Stage: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output == "" {
		t.Fatal("review fake sem saída")
	}
}

func TestRegistryFake(t *testing.T) {
	cfg := &config.Config{
		Engines:  map[string]config.EngineConfig{"fake": {Driver: "fake"}},
		Defaults: config.Defaults{Engine: "fake"},
	}
	registry := NewRegistry(cfg)
	if _, err := registry.Get("fake"); err != nil {
		t.Fatalf("Get fake: %v", err)
	}
	if _, err := registry.Get("inexistente"); err == nil {
		t.Fatal("esperava erro para engine inexistente")
	}
}
