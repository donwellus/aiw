package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
)

func closeConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{
		Root:          t.TempDir(),
		DefaultSource: "folder",
		Sources: map[string]config.SourceConfig{
			"folder": {Driver: "folder", Path: "./tasks", DoneDir: "./tasks/done"},
		},
	}
	if err := os.MkdirAll(filepath.Join(cfg.Root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestCloseIssueWithoutRun(t *testing.T) {
	cfg := closeConfig(t)
	writeCloseTask(t, cfg, "task-1")
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "task-1", Status: "todo"}); err != nil {
		t.Fatal(err)
	}
	result, err := CloseIssue(context.Background(), cfg, store, "task-1", "duplicada", false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Closed || result.Source != "folder" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "tasks", "done", "task-1.md")); err != nil {
		t.Fatal(err)
	}
	record, found := store.FindByID("task-1")
	if !found || record.Status != "canceled" {
		t.Fatalf("registro local = %+v, encontrado=%v", record, found)
	}
}

func TestCloseIssueCancelsPausedRunBeforeClosing(t *testing.T) {
	cfg := closeConfig(t)
	writeCloseTask(t, cfg, "task-2")
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	run := &pipeline.RunState{
		ID:     "run-2",
		Task:   source.Task{Source: "folder", ExternalID: "task-2", Title: "Tarefa"},
		Source: "folder",
		Ref:    source.Ref{Source: "folder", ID: "task-2"},
		Status: "paused",
	}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "task-2", Status: "paused", RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	result, err := CloseIssue(context.Background(), cfg, store, "task-2", "substituída", true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Closed || !result.Canceled {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "tasks", "done", "task-2.md")); err != nil {
		t.Fatal(err)
	}
}

func TestCloseIssueRequiresCancelRun(t *testing.T) {
	cfg := closeConfig(t)
	writeCloseTask(t, cfg, "task-3")
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	run := &pipeline.RunState{ID: "run-3", Task: source.Task{Source: "folder", ExternalID: "task-3"}, Source: "folder", Ref: source.Ref{Source: "folder", ID: "task-3"}, Status: "paused"}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "task-3", Status: "paused", RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := CloseIssue(context.Background(), cfg, store, "task-3", "sem cancelar", false); err == nil {
		t.Fatal("fechar run ativa sem cancel_run deveria falhar")
	}
}

func writeCloseTask(t *testing.T, cfg *config.Config, id string) {
	t.Helper()
	path := filepath.Join(cfg.Root, "tasks", id+".md")
	if err := os.WriteFile(path, []byte("---\nid: "+id+"\ntitle: Tarefa\n---\ncorpo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCloseIssueWaitsForPendingCancel(t *testing.T) {
	cfg := closeConfig(t)
	writeCloseTask(t, cfg, "task-4")
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "folder:" + cfg.Root + ":task-4"
	run := &pipeline.RunState{
		ID:     "run-4",
		Task:   source.Task{Source: "folder", ExternalID: "task-4", ClaimKey: key},
		Source: "folder",
		Ref:    source.Ref{Source: "folder", ID: "task-4", ClaimKey: key},
		Status: "running",
	}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	claim, acquired, err := store.ClaimByKey(key, "folder", "task-4", run.ID, time.Hour)
	if err != nil || !acquired {
		t.Fatalf("claim: %v %v", acquired, err)
	}
	defer func() { _ = claim.Release() }()
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "task-4", Status: "running", RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	result, err := CloseIssue(context.Background(), cfg, store, "task-4", "substituída", true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Closed || result.Status != "canceling" {
		t.Fatalf("fechamento deveria aguardar cancelamento: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "tasks", "task-4.md")); err != nil {
		t.Fatalf("task não deveria ter sido movida: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "tasks", "done", "task-4.md")); !os.IsNotExist(err) {
		t.Fatalf("task não deveria estar em done: %v", err)
	}
}

func TestCloseIssueRefusesOpenPR(t *testing.T) {
	cfg := closeConfig(t)
	writeCloseTask(t, cfg, "task-5")
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	run := &pipeline.RunState{
		ID:     "run-5",
		Task:   source.Task{Source: "folder", ExternalID: "task-5"},
		Source: "folder",
		Ref:    source.Ref{Source: "folder", ID: "task-5"},
		Status: "succeeded",
		PRURL:  "https://github.com/owner/repo/pull/99",
	}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "task-5", Status: "succeeded", RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := CloseIssue(context.Background(), cfg, store, "task-5", "duplicada", true); err == nil {
		t.Fatal("fechamento com PR aberto deveria falhar")
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "tasks", "task-5.md")); err != nil {
		t.Fatalf("task não deveria ter sido movida: %v", err)
	}
}

func TestSetDeliveryFeedbackResetsCIFixRounds(t *testing.T) {
	cfg := closeConfig(t)
	run := &pipeline.RunState{
		ID:     "run-ci",
		Task:   source.Task{Source: "folder", ExternalID: "task-ci"},
		Source: "folder",
		Ref:    source.Ref{Source: "folder", ID: "task-ci"},
		Status: "paused",
		CIFix:  &pipeline.CIFixState{Rounds: 3},
	}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	path, err := SetDeliveryFeedback(cfg, run.ID, "FIX_TEST", "O teste está desatualizado.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := pipeline.LoadRun(cfg, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CIFix == nil || loaded.CIFix.Rounds != 0 {
		t.Fatalf("ci_fix = %+v", loaded.CIFix)
	}
}
