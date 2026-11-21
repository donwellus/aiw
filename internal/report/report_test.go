package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/engine"
	"github.com/donwellus/aiw/internal/pause"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Root:     t.TempDir(),
		Defaults: config.Defaults{ClaimTTL: config.Duration(time.Hour)},
	}
}

func saveRun(t *testing.T, cfg *config.Config, id, taskID, title, status, claimKey string, cost float64, created time.Time) {
	t.Helper()
	run := &pipeline.RunState{
		ID: id,
		Task: source.Task{
			Source:     "folder",
			ExternalID: taskID,
			Title:      title,
			ClaimKey:   claimKey,
		},
		Source:    "folder",
		Status:    status,
		Stage:     "done",
		Usage:     engine.Usage{InputTokens: 10, OutputTokens: 5, CostUSD: cost},
		CreatedAt: created,
	}
	if status == "failed" {
		run.Error = "testes falharam"
	}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
}

func putRecord(t *testing.T, store *state.Store, id, status, runID, key string) {
	t.Helper()
	if err := store.Put(state.TaskRecord{Source: "folder", ID: id, Status: status, RunID: runID, Key: key}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildReport(t *testing.T) {
	cfg := testConfig(t)
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	alphaKey := "folder:" + cfg.Root + ":alpha"
	saveRun(t, cfg, "run-1", "alpha", "Alpha antiga", "succeeded", alphaKey, 1.0, now.Add(-2*time.Hour))
	saveRun(t, cfg, "run-2", "alpha", "Alpha atual", "succeeded", alphaKey, 0.5, now.Add(-time.Hour))
	saveRun(t, cfg, "run-3", "beta", "Beta", "failed", "folder:"+cfg.Root+":beta", 0.25, now.Add(-30*time.Minute))
	saveRun(t, cfg, "run-4", "gamma", "Gamma", "needs_input", "folder:"+cfg.Root+":gamma", 0, now.Add(-20*time.Minute))
	putRecord(t, store, "alpha", "succeeded", "run-2", alphaKey)
	putRecord(t, store, "beta", "failed", "run-3", "folder:"+cfg.Root+":beta")
	putRecord(t, store, "gamma", "needs_input", "run-4", "folder:"+cfg.Root+":gamma")
	putRecord(t, store, "delta", "todo", "", "")
	if err := pause.Set(cfg.DataDir(), "teste"); err != nil {
		t.Fatal(err)
	}
	if _, acquired, err := store.ClaimByKey(alphaKey, "folder", "alpha", "run-2", time.Hour); err != nil || !acquired {
		t.Fatalf("claim ativo: acquired=%v err=%v", acquired, err)
	}
	stale := state.ClaimInfo{
		Key:       "folder:" + cfg.Root + ":delta",
		Source:    "folder",
		ID:        "delta",
		RunID:     "run-antiga",
		PID:       123,
		CreatedAt: now.Add(-3 * time.Hour),
	}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.StateDir(), "claims", "stale.lock"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	built, err := Build(cfg, store, Options{
		Source: "folder",
		SourceTasks: []SourceTask{
			{Task: &source.Task{Source: "folder", ExternalID: "epsilon", Title: "Epsilon"}, Location: "inbox"},
			{Task: &source.Task{Source: "folder", ExternalID: "zeta", Title: "Zeta"}, Location: "done"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !built.Paused {
		t.Fatal("pausa global não detectada")
	}
	if built.Source != "folder" {
		t.Fatalf("source = %q", built.Source)
	}
	if built.Summary.Tasks != 6 {
		t.Fatalf("tarefas = %d, esperado 6", built.Summary.Tasks)
	}
	if built.Summary.Runs != 4 {
		t.Fatalf("runs = %d, esperado 4", built.Summary.Runs)
	}
	if built.Summary.CostUSD != 1.75 {
		t.Fatalf("custo acumulado = %v, esperado 1.75", built.Summary.CostUSD)
	}
	if built.Summary.TaskCostUSD != 0.75 {
		t.Fatalf("custo da última run por tarefa = %v, esperado 0.75", built.Summary.TaskCostUSD)
	}
	wantStatus := map[string]int{"succeeded": 2, "failed": 1, "needs_input": 1, "todo": 2}
	for status, count := range wantStatus {
		if built.Summary.ByStatus[status] != count {
			t.Fatalf("status %s = %d, esperado %d (mapa %v)", status, built.Summary.ByStatus[status], count, built.Summary.ByStatus)
		}
	}
	if built.Summary.ActiveClaims != 2 || built.Summary.StaleClaims != 1 {
		t.Fatalf("claims = %d ativos / %d vencidos", built.Summary.ActiveClaims, built.Summary.StaleClaims)
	}
	if len(built.Alerts) < 4 {
		t.Fatalf("alertas = %d, esperado >= 4: %+v", len(built.Alerts), built.Alerts)
	}
	alpha := findTask(t, built.Tasks, "alpha")
	if alpha.RunID != "run-2" || alpha.CostUSD != 0.5 || alpha.Title != "Alpha atual" {
		t.Fatalf("alpha inesperada: %+v", alpha)
	}
	zeta := findTask(t, built.Tasks, "zeta")
	if zeta.Status != "succeeded" || zeta.Location != "done" {
		t.Fatalf("zeta inesperada: %+v", zeta)
	}
	epsilon := findTask(t, built.Tasks, "epsilon")
	if epsilon.Status != "todo" || epsilon.Location != "inbox" {
		t.Fatalf("epsilon inesperada: %+v", epsilon)
	}
}

func TestBuildMissingRunAlert(t *testing.T) {
	cfg := testConfig(t)
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	putRecord(t, store, "orphan", "succeeded", "run-sumiu", "")
	built, err := Build(cfg, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, alert := range built.Alerts {
		if alert.Kind == "missing_run" && alert.TaskID == "orphan" {
			found = true
		}
	}
	if !found {
		t.Fatalf("alerta missing_run ausente: %+v", built.Alerts)
	}
}

func findTask(t *testing.T, tasks []Task, id string) Task {
	t.Helper()
	for _, task := range tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("tarefa %s não encontrada", id)
	return Task{}
}

func TestBuildDeduplicatesFolderAliases(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultSource = "folder"
	cfg.Sources = map[string]config.SourceConfig{
		"folder": {Driver: "folder", Path: "./tasks"},
		"alias":  {Driver: "folder", Path: "./tasks"},
	}
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "alias", ID: "dup", Status: "needs_input"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "dup", Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	built, err := Build(cfg, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Tasks) != 1 {
		t.Fatalf("tarefas = %d, esperado 1: %+v", len(built.Tasks), built.Tasks)
	}
	if built.Tasks[0].Status != "succeeded" {
		t.Fatalf("status = %s, esperado succeeded", built.Tasks[0].Status)
	}
}
