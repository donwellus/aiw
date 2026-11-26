package control

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
)

func refineRun(t *testing.T, status, stage string, stages map[string]string, gates map[string]string) (*config.Config, *pipeline.RunState) {
	t.Helper()
	cfg := testConfig(t)
	worktree := filepath.Join(cfg.Root, "worktree")
	if err := os.MkdirAll(filepath.Join(worktree, ".aiw-run"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &pipeline.RunState{
		ID:        "run-refine",
		Task:      source.Task{Source: "folder", ExternalID: "task"},
		Source:    "folder",
		Status:    status,
		Stage:     stage,
		Worktree:  worktree,
		Gates:     gates,
		CreatedAt: time.Now().UTC(),
	}
	for _, name := range []string{"intake", "requirements", "architect", "implement", "test", "review", "deliver"} {
		status := stages[name]
		if status == "" {
			status = "pending"
		}
		run.Stages = append(run.Stages, pipeline.StageState{Name: name, Status: status})
	}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"intake.md", "spec.md", "plan.md", "spec-review.md", "plan-review.md"} {
		if err := os.WriteFile(filepath.Join(worktree, ".aiw-run", name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cfg, run
}

func TestPrepareRefineSpecResetsSpecAndPlan(t *testing.T) {
	cfg, run := refineRun(t, "paused", "requirements", map[string]string{
		"intake": "done", "requirements": "gated", "architect": "gated",
	}, map[string]string{"intake": "approved", "spec": "escalated", "plan": "pending"})
	prepared, result, err := PrepareRefine(cfg, run.ID, "spec", "Mude o critério de aceite para X.")
	if err != nil {
		t.Fatal(err)
	}
	if result.Target != "spec" || result.Stage != "requirements" {
		t.Fatalf("resultado = %+v", result)
	}
	if prepared.Stages[indexOfStage("requirements")].Status != "pending" || prepared.Stages[indexOfStage("architect")].Status != "pending" {
		t.Fatalf("estágios não resetados: %+v", prepared.Stages)
	}
	if prepared.Gates["spec"] != "pending" || prepared.Gates["plan"] != "pending" {
		t.Fatalf("gates = %+v", prepared.Gates)
	}
	if _, err := os.Stat(filepath.Join(prepared.Worktree, ".aiw-run", "spec.md")); !os.IsNotExist(err) {
		t.Fatalf("spec.md deveria ter sido removido: %v", err)
	}
	if _, err := os.Stat(filepath.Join(prepared.Worktree, ".aiw-run", "plan.md")); !os.IsNotExist(err) {
		t.Fatalf("plan.md deveria ter sido removido: %v", err)
	}
	if _, err := os.Stat(filepath.Join(prepared.Worktree, ".aiw-run", "intake.md")); err != nil {
		t.Fatalf("intake.md deveria permanecer: %v", err)
	}
	feedback, err := os.ReadFile(result.FeedbackFile)
	if err != nil || !strings.Contains(string(feedback), "critério de aceite") {
		t.Fatalf("feedback = %q err=%v", feedback, err)
	}
}

func TestPrepareRefinePlanKeepsSpec(t *testing.T) {
	cfg, run := refineRun(t, "paused", "architect", map[string]string{
		"intake": "done", "requirements": "done", "architect": "gated",
	}, map[string]string{"intake": "approved", "spec": "approved", "plan": "escalated"})
	prepared, _, err := PrepareRefine(cfg, run.ID, "plan", "Ajuste apenas os testes do plano.")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Stages[indexOfStage("requirements")].Status != "done" {
		t.Fatalf("requirements deveria permanecer done: %+v", prepared.Stages)
	}
	if prepared.Stages[indexOfStage("architect")].Status != "pending" {
		t.Fatalf("architect deveria ser pending: %+v", prepared.Stages)
	}
	if prepared.Gates["spec"] != "approved" || prepared.Gates["plan"] != "pending" {
		t.Fatalf("gates = %+v", prepared.Gates)
	}
	if _, err := os.Stat(filepath.Join(prepared.Worktree, ".aiw-run", "spec.md")); err != nil {
		t.Fatalf("spec.md deveria permanecer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(prepared.Worktree, ".aiw-run", "plan.md")); !os.IsNotExist(err) {
		t.Fatalf("plan.md deveria ter sido removido: %v", err)
	}
}

func TestPrepareRefineRejectsStartedImplementation(t *testing.T) {
	cfg, run := refineRun(t, "paused", "architect", map[string]string{
		"intake": "done", "requirements": "done", "architect": "gated", "implement": "done",
	}, map[string]string{"spec": "approved", "plan": "escalated"})
	if _, _, err := PrepareRefine(cfg, run.ID, "spec", "Mudança tardia"); err == nil {
		t.Fatal("deveria rejeitar refinamento depois do implement")
	}
}

func TestPreviewRefineDoesNotMutate(t *testing.T) {
	cfg, run := refineRun(t, "paused", "architect", map[string]string{
		"intake": "done", "requirements": "done", "architect": "gated",
	}, map[string]string{"intake": "approved", "spec": "approved", "plan": "escalated"})
	runPath := filepath.Join(cfg.RunsDir(), run.ID, "run.json")
	before, err := os.ReadFile(runPath)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewRefine(cfg, run.ID, "plan", "Ajuste o plano sem mudar a spec.")
	if err != nil {
		t.Fatal(err)
	}
	if preview.PreviewID == "" || preview.Target != "plan" || preview.Stage != "architect" || preview.Status != "paused" {
		t.Fatalf("preview = %+v", preview)
	}
	if len(preview.StagesReset) != 5 || preview.StagesReset[0] != "architect" {
		t.Fatalf("stages = %+v", preview.StagesReset)
	}
	if len(preview.Artifacts) != 2 || preview.Artifacts[0] != "plan.md" {
		t.Fatalf("artifacts = %+v", preview.Artifacts)
	}
	after, err := os.ReadFile(runPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("preview alterou run.json")
	}
	if _, err := os.Stat(filepath.Join(run.Worktree, ".aiw-run", "plan.md")); err != nil {
		t.Fatalf("preview removeu plan.md: %v", err)
	}
	if _, err := os.Stat(preview.FeedbackFile); !os.IsNotExist(err) {
		t.Fatalf("preview criou feedback: %v", err)
	}
	if _, err := ValidateRefinePreview(cfg, run.ID, "plan", "Ajuste o plano sem mudar a spec.", preview.PreviewID); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRefinePreview(cfg, run.ID, "plan", "outro feedback", preview.PreviewID); err == nil {
		t.Fatal("feedback alterado deveria invalidar preview")
	}
}

func TestValidateRefinePreviewInvalidatesOnRunChange(t *testing.T) {
	cfg, run := refineRun(t, "paused", "architect", map[string]string{
		"intake": "done", "requirements": "done", "architect": "gated",
	}, map[string]string{"intake": "approved", "spec": "approved", "plan": "escalated"})
	preview, err := PreviewRefine(cfg, run.ID, "plan", "Ajuste o plano.")
	if err != nil {
		t.Fatal(err)
	}
	run.Stages[indexOfStage("architect")].Status = "done"
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRefinePreview(cfg, run.ID, "plan", "Ajuste o plano.", preview.PreviewID); err == nil {
		t.Fatal("mudança de estágio deveria invalidar preview")
	}
}

func TestExecutorRejectsInvalidPreview(t *testing.T) {
	cfg, run := refineRun(t, "paused", "architect", map[string]string{
		"intake": "done", "requirements": "done", "architect": "gated",
	}, map[string]string{"intake": "approved", "spec": "approved", "plan": "escalated"})
	executor := &Executor{cfg: cfg, log: slog.Default()}
	_, err := executor.runRefine(context.Background(), &Operation{
		RunID: run.ID,
		Args: map[string]any{
			"target":     "plan",
			"feedback":   "Ajuste o plano.",
			"preview_id": "rp-invalido",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "preview") {
		t.Fatalf("esperava erro de preview, obtive %v", err)
	}
}
