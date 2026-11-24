package control

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/source/folder"
	"github.com/donwellus/aiw/internal/state"
)

func TestResumeCandidates(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(id, status, runID string) {
		t.Helper()
		if err := store.Put(state.TaskRecord{Source: "folder", ID: id, Status: status, RunID: runID}); err != nil {
			t.Fatal(err)
		}
	}
	put("a", "paused", "run-a")
	put("b", "todo", "run-b")
	put("c", "running", "run-c")
	put("d", "todo", "")
	put("e", "needs_input", "run-e")
	put("f", "todo", "run-a")

	got := ResumeCandidates(store)
	want := []string{"run-a", "run-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidatos = %v, esperado %v", got, want)
	}
}

func TestResumeCandidatesUsesLatestStatus(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "a", Status: "paused", RunID: "run-a"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "a", Status: "running", RunID: "run-a"}); err != nil {
		t.Fatal(err)
	}
	if got := ResumeCandidates(store); len(got) != 0 {
		t.Fatalf("candidatos = %v, esperado vazio", got)
	}
}

func TestCancelPausedRun(t *testing.T) {
	cfg := testConfig(t)
	cfg.Sources = map[string]config.SourceConfig{
		"folder": {Driver: "folder", Path: "./tasks"},
	}
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	run := &pipeline.RunState{
		ID:     "run-paused",
		Task:   source.Task{Source: "folder", ExternalID: "task-1", Title: "Tarefa"},
		Source: "folder",
		Ref:    source.Ref{Source: "folder", ID: "task-1"},
		Status: "paused",
	}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "task-1", Status: "paused", RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if err := writeMarker(filepath.Join(cfg.RunsDir(), run.ID, pipeline.PauseMarker)); err != nil {
		t.Fatal(err)
	}
	result, err := Cancel(context.Background(), cfg, store, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Canceled || result.Status != "canceled" {
		t.Fatalf("cancel inesperado: %+v", result)
	}
	loaded, err := pipeline.LoadRun(cfg, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "canceled" {
		t.Fatalf("run status = %s", loaded.Status)
	}
	record, found := store.FindByRunID(run.ID)
	if !found || record.Status != "todo" {
		t.Fatalf("estado inesperado: %+v %v", record, found)
	}
	if _, err := os.Stat(filepath.Join(cfg.RunsDir(), run.ID, pipeline.PauseMarker)); !os.IsNotExist(err) {
		t.Fatal("marker de pausa permaneceu")
	}
}

func TestCancelRunningWithClaim(t *testing.T) {
	cfg := testConfig(t)
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "folder:" + cfg.Root + ":task-1"
	run := &pipeline.RunState{
		ID:     "run-running",
		Task:   source.Task{Source: "folder", ExternalID: "task-1", Title: "Tarefa", ClaimKey: key},
		Source: "folder",
		Ref:    source.Ref{Source: "folder", ID: "task-1", ClaimKey: key},
		Status: "running",
	}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	claim, acquired, err := store.ClaimByKey(key, "folder", "task-1", run.ID, time.Hour)
	if err != nil || !acquired {
		t.Fatalf("claim: %v %v", acquired, err)
	}
	defer func() { _ = claim.Release() }()
	result, err := Cancel(context.Background(), cfg, store, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "canceling" {
		t.Fatalf("status = %s", result.Status)
	}
	if _, err := os.Stat(filepath.Join(cfg.RunsDir(), run.ID, pipeline.CancelMarker)); err != nil {
		t.Fatalf("marker CANCEL ausente: %v", err)
	}
}

func TestAnswerPublishesAndQueuesRerun(t *testing.T) {
	cfg := testConfig(t)
	tasks := filepath.Join(cfg.Root, "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(tasks, "task-1.md")
	if err := os.WriteFile(taskPath, []byte("---\nid: task-1\ntitle: Tarefa\n---\ncurta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Sources = map[string]config.SourceConfig{
		"folder": {Driver: "folder", Path: "./tasks"},
	}
	run := &pipeline.RunState{
		ID:     "run-answer",
		Task:   source.Task{Source: "folder", ExternalID: "task-1", Title: "Tarefa"},
		Source: "folder",
		Ref:    source.Ref{Source: "folder", ID: "task-1", URL: taskPath},
		Status: "needs_input",
	}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	result, err := Answer(context.Background(), cfg, run.ID, "Use SQLite e mantenha o escopo.")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Answered || result.OperationID == "" {
		t.Fatalf("answer inesperado: %+v", result)
	}
	data, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "## Resposta do usuário") || !strings.Contains(string(data), "SQLite") {
		t.Fatalf("resposta não foi anexada:\n%s", data)
	}
	task, err := folder.ParseFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if task.ContentHash == run.Task.ContentHash {
		t.Fatal("content hash não mudou")
	}
	operations, err := OpenOperationStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := operations.Get(result.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Action != ActionRerun || operation.Status != StatusQueued {
		t.Fatalf("operação inesperada: %+v", operation)
	}
	if _, err := os.Stat(filepath.Join(cfg.RunsDir(), run.ID, "answer.md")); err != nil {
		t.Fatalf("answer.md ausente: %v", err)
	}
}
