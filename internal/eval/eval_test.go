package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/donwellus/aiw/internal/config"
)

func TestLoadSmokeSuite(t *testing.T) {
	suite, err := Load(filepath.Join("..", "..", "evals", "smoke", "suite.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if suite.Name != "smoke" || suite.Engine != "fake" {
		t.Fatalf("suite inesperada: %+v", suite)
	}
	if len(suite.Cases) != 1 || suite.Cases[0].ID != "fake-change" {
		t.Fatalf("casos inesperados: %+v", suite.Cases)
	}
	if _, err := os.Stat(suite.Cases[0].Task); err != nil {
		t.Fatalf("task não resolvida: %v", err)
	}
	if _, err := os.Stat(suite.Cases[0].Repo); err != nil {
		t.Fatalf("repo não resolvido: %v", err)
	}
}

func TestLoadInvalidSuite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suite.yaml")
	if err := os.WriteFile(path, []byte("name: x\ncases:\n  - id: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("esperava erro para caso sem task/repo")
	}
}

func TestRunHarnessWithFakeEngine(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasksDir := filepath.Join(dir, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "001.md"), []byte(`---
id: 001
title: Teste de eval
repo: .
labels: [eval]
acceptance:
  - O engine fake cria AIW_FAKE_CHANGE.md.
---

Descrição suficientemente longa para o intake e o pipeline fake executarem.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	suitePath := filepath.Join(dir, "suite.yaml")
	if err := os.WriteFile(suitePath, []byte(`name: test
engine: fake
timeout: 1m
cases:
  - id: fake
    task: tasks/001.md
    repo: fixture
    verify: test -f AIW_FAKE_CHANGE.md
`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite, err := Load(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	base, err := config.Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	results, summary, err := Run(context.Background(), suite, Options{
		Base:   base,
		Engine: "fake",
		OutDir: outDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 1 || summary.Passed != 1 || summary.Failed != 0 {
		t.Fatalf("summary inesperado: %+v", summary)
	}
	if results[0].Status != "passed" || results[0].VerifyExit != 0 {
		t.Fatalf("resultado inesperado: %+v", results[0])
	}
	if _, err := os.Stat(filepath.Join(outDir, "results.json")); err != nil {
		t.Fatalf("results.json ausente: %v", err)
	}
	if _, err := os.Stat(filepath.Join(results[0].ArtifactsDir, "diff.patch")); err != nil {
		t.Fatalf("artefatos não copiados: %v", err)
	}
}
