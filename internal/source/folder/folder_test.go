package folder

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/source/sourcetest"
)

func newFixture(t *testing.T) (string, *Driver) {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "tasks", "001.md"), `---
id: 001
title: Primeira tarefa
repo: ./repo
labels: [aiw, backend]
priority: high
acceptance:
  - Teste passa
---
Corpo da tarefa 001.
`)
	mustWrite(t, filepath.Join(root, "tasks", "002.md"), `# Segunda tarefa

Sem frontmatter.
`)
	mustWrite(t, filepath.Join(root, "tasks", "done", "antiga.md"), `# Antiga

Já concluída.
`)
	mustWrite(t, filepath.Join(root, "tasks", "failed", "falha.md"), `# Falha

Já falhou.
`)
	if err := os.MkdirAll(filepath.Join(root, "tasks", "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tasks", "failed"), 0o755); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := config.SourceConfig{
		Driver:    "folder",
		Path:      "./tasks",
		DoneDir:   "./tasks/done",
		FailedDir: "./tasks/failed",
		WriteBack: &enabled,
	}
	driver, err := New("folder", root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return root, driver
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseAndList(t *testing.T) {
	root, driver := newFixture(t)
	sourcetest.Run(t, driver, sourcetest.Options{ExpectedID: "001"})

	refs, err := driver.List(context.Background(), source.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("List = %d tarefas, esperado 2 (done/failed ignorados)", len(refs))
	}

	task, err := driver.Fetch(context.Background(), source.Ref{Source: "folder", URL: filepath.Join(root, "tasks", "001.md")})
	if err != nil {
		t.Fatal(err)
	}
	if task.Title != "Primeira tarefa" {
		t.Fatalf("Title = %q", task.Title)
	}
	if task.RepoHint != "./repo" {
		t.Fatalf("RepoHint = %q", task.RepoHint)
	}
	if len(task.Acceptance) != 1 {
		t.Fatalf("Acceptance = %v", task.Acceptance)
	}
	if task.ContentHash == "" {
		t.Fatal("ContentHash vazio")
	}
}

func TestListFilter(t *testing.T) {
	_, driver := newFixture(t)
	refs, err := driver.List(context.Background(), source.Filter{Labels: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != "001" {
		t.Fatalf("filtro por label retornou %+v", refs)
	}
	refs, err = driver.List(context.Background(), source.Filter{ExcludeLabels: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != "002" {
		t.Fatalf("filtro por exclude retornou %+v", refs)
	}
}

func TestMarkDoneAndFailed(t *testing.T) {
	root, driver := newFixture(t)
	ctx := context.Background()
	ref := source.Ref{Source: "folder", URL: filepath.Join(root, "tasks", "001.md")}
	if err := driver.MarkDone(ctx, ref, source.RunResult{RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "done", "001.md")); err != nil {
		t.Fatalf("tarefa não foi movida para done: %v", err)
	}
	ref = source.Ref{Source: "folder", URL: filepath.Join(root, "tasks", "002.md")}
	if err := driver.MarkFailed(ctx, ref, os.ErrDeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "failed", "002.md")); err != nil {
		t.Fatalf("tarefa não foi movida para failed: %v", err)
	}
}

func TestWriteBackDisabled(t *testing.T) {
	root, driver := newFixture(t)
	driver.cfg.WriteBack = boolPtr(false)
	if driver.Caps().WriteBack {
		t.Fatal("Caps.WriteBack deveria ser false")
	}
	ref := source.Ref{Source: "folder", URL: filepath.Join(root, "tasks", "001.md")}
	if err := driver.MarkDone(context.Background(), ref, source.RunResult{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "001.md")); err != nil {
		t.Fatalf("arquivo não deveria ter sido movido: %v", err)
	}
}

func boolPtr(value bool) *bool {
	return &value
}
