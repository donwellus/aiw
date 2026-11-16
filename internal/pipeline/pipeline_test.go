package pipeline

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/source/folder"
	"github.com/donwellus/aiw/internal/telemetry"
)

func setupProject(t *testing.T, gates string) (string, *config.Config, source.Ref, *source.Task) {
	t.Helper()
	root := t.TempDir()

	cfgYAML := `engines:
  fake:
    driver: fake
defaults:
  engine: fake
  timeout: 1m
  budget_usd: 5
  max_test_retries: 1
  max_review_rounds: 1
telemetry:
  provider: noop
sources:
  folder:
    driver: folder
    path: ./tasks
    done_dir: ./tasks/done
    failed_dir: ./tasks/failed
default_source: folder
workspace:
  default_repo: ./demo
  test_commands: ["true"]
  gates:
` + gates
	writeTestFile(t, filepath.Join(root, ".aiw", "config.yaml"), cfgYAML)
	writeTestFile(t, filepath.Join(root, "tasks", "001.md"), `---
id: 001
title: Tarefa de demonstração
repo: ./demo
labels: [aiw]
acceptance:
  - O pipeline executa de ponta a ponta.
---
Implemente algo simples para validar o pipeline.
`)

	repo := filepath.Join(root, "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, "init", "-b", "main")
	runTestGit(t, repo, "config", "user.email", "test@example.com")
	runTestGit(t, repo, "config", "user.name", "Test")
	writeTestFile(t, filepath.Join(repo, "README.md"), "# demo\n")
	runTestGit(t, repo, "add", "-A")
	runTestGit(t, repo, "commit", "-m", "initial")

	cfg, err := config.Load(root, "")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	driver, err := folder.New("folder", root, cfg.Sources["folder"])
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	refs, err := driver.List(ctx, source.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %d", len(refs))
	}
	task, err := driver.Fetch(ctx, refs[0])
	if err != nil {
		t.Fatal(err)
	}
	return root, cfg, refs[0], task
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, stderr.String())
	}
	return stdout.String()
}

func TestRunEndToEndWithFakeEngine(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: false\n    plan: false\n")
	recorder := telemetry.NewRecorder()
	p := New(cfg)
	p.Telemetry = recorder
	p.AutoApprove = true

	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "succeeded" {
		t.Fatalf("status = %q", res.Status)
	}

	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if run.Status != "succeeded" {
		t.Fatalf("run.status = %q (erro: %s)", run.Status, run.Error)
	}
	for _, name := range []string{"spec.md", "plan.md", "implement.md", "test.log", "diff.patch", "review.md", "deliver.md"} {
		if _, err := os.Stat(filepath.Join(run.Artifacts[name])); err != nil {
			t.Fatalf("artefato %s ausente: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, "AIW_FAKE_CHANGE.md")); err != nil {
		t.Fatalf("mudança do fake ausente: %v", err)
	}
	log := runTestGit(t, res.Worktree, "log", "--oneline", "-1")
	if !strings.Contains(log, "aiw:") {
		t.Fatalf("commit não criado: %q", log)
	}

	if _, err := os.Stat(filepath.Join(root, "tasks", "001.md")); !os.IsNotExist(err) {
		t.Fatal("tarefa deveria ter sido movida para done")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "done", "001.md")); err != nil {
		t.Fatalf("tarefa não está em done: %v", err)
	}

	if _, err := os.Stat(filepath.Join(cfg.RunsDir(), res.RunID, "events.jsonl")); err != nil {
		t.Fatalf("events.jsonl ausente: %v", err)
	}

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("traces = %d", len(traces))
	}
	spans := map[string]bool{}
	for _, span := range traces[0].Spans {
		spans[span.Name] = true
	}
	for _, stage := range []string{"stage.requirements", "stage.architect", "stage.implement", "stage.test", "stage.review", "stage.deliver"} {
		if !spans[stage] {
			t.Fatalf("span %s ausente (spans: %v)", stage, spans)
		}
	}
	if len(traces[0].Scores) == 0 || traces[0].Scores[0].Value != 1 {
		t.Fatalf("score de sucesso ausente: %+v", traces[0].Scores)
	}
}

func TestPausedGateAndResume(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: true\n    plan: false\n")
	p := New(cfg)
	p.Telemetry = telemetry.NewRecorder()

	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{})
	if err == nil || !strings.Contains(err.Error(), "pausada") {
		t.Fatalf("esperava ErrPaused, obtive %v", err)
	}
	if res == nil {
		t.Fatal("resultado vazio")
	}
	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "paused" {
		t.Fatalf("status = %q", run.Status)
	}
	if run.Gates["spec"] != "pending" {
		t.Fatalf("gate spec = %q", run.Gates["spec"])
	}
	run.Gates["spec"] = "approved"
	if err := SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}

	p2 := New(cfg)
	p2.Telemetry = telemetry.NewRecorder()
	p2.AutoApprove = true
	resumed, err := p2.Resume(context.Background(), res.RunID, Options{})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Status != "succeeded" {
		t.Fatalf("status após resume = %q", resumed.Status)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "done", "001.md")); err != nil {
		t.Fatalf("tarefa não foi movida após resume: %v", err)
	}
}
