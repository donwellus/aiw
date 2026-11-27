package folder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestDoingLifecycle(t *testing.T) {
	root, base := newFixture(t)
	cfg := base.cfg
	cfg.DoingDir = "./tasks/doing"
	driver, err := New("folder", root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ref := source.Ref{Source: "folder", URL: filepath.Join(root, "tasks", "001.md")}
	if err := driver.MarkRunning(ctx, ref, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "doing", "001.md")); err != nil {
		t.Fatalf("tarefa não foi movida para doing: %v", err)
	}
	refs, err := driver.List(ctx, source.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != "002" {
		t.Fatalf("doing deveria ser ignorado pelo List: %+v", refs)
	}

	if err := driver.MarkPending(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "001.md")); err != nil {
		t.Fatalf("tarefa não voltou ao inbox: %v", err)
	}

	if err := driver.MarkRunning(ctx, ref, "run-2"); err != nil {
		t.Fatal(err)
	}
	if err := driver.MarkDone(ctx, ref, source.RunResult{RunID: "run-2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "done", "001.md")); err != nil {
		t.Fatalf("tarefa não foi para done: %v", err)
	}

	ref2 := source.Ref{Source: "folder", URL: filepath.Join(root, "tasks", "002.md")}
	if err := driver.MarkRunning(ctx, ref2, "run-3"); err != nil {
		t.Fatal(err)
	}
	if err := driver.MarkFailed(ctx, ref2, os.ErrDeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "failed", "002.md")); err != nil {
		t.Fatalf("tarefa não foi para failed: %v", err)
	}
	leftovers, err := os.ReadDir(filepath.Join(root, "tasks", "doing"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("doing deveria estar vazio: %v", leftovers)
	}
}

func TestListAllLocations(t *testing.T) {
	root, driver := newFixture(t)
	refs, err := driver.ListAll(context.Background(), source.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 4 {
		t.Fatalf("ListAll = %d, esperado 4 (inbox + done + failed)", len(refs))
	}
	locations := map[string]string{}
	for _, ref := range refs {
		locations[ref.ID] = ref.Meta["location"]
	}
	if locations["001"] != "inbox" || locations["002"] != "inbox" {
		t.Fatalf("inbox inesperado: %+v", locations)
	}
	if locations["antiga"] != "done" || locations["falha"] != "failed" {
		t.Fatalf("localizações inesperadas: %+v", locations)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "done", "antiga.md")); err != nil {
		t.Fatal(err)
	}
}

func TestClaimKeySharedAcrossAliases(t *testing.T) {
	root, base := newFixture(t)
	folderDriver, err := New("folder", root, base.cfg)
	if err != nil {
		t.Fatal(err)
	}
	waveDriver, err := New("wave-a", root, base.cfg)
	if err != nil {
		t.Fatal(err)
	}
	refsA, err := folderDriver.List(context.Background(), source.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	refsB, err := waveDriver.List(context.Background(), source.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refsA) != len(refsB) || len(refsA) == 0 {
		t.Fatal("fixtures inesperadas")
	}
	if refsA[0].ClaimKey == "" || refsA[0].ClaimKey != refsB[0].ClaimKey {
		t.Fatalf("ClaimKey divergente: %q vs %q", refsA[0].ClaimKey, refsB[0].ClaimKey)
	}
	task, err := waveDriver.Fetch(context.Background(), refsB[0])
	if err != nil {
		t.Fatal(err)
	}
	if task.ClaimKey != refsA[0].ClaimKey {
		t.Fatalf("Task.ClaimKey = %q, esperado %q", task.ClaimKey, refsA[0].ClaimKey)
	}
}

func TestCreateTask(t *testing.T) {
	root, driver := newFixture(t)
	task, err := driver.CreateTask(context.Background(), source.TaskDraft{
		Title:       "Nova tarefa",
		Description: "Corpo criado pelo coordinator.",
		Acceptance:  []string{"O critério é verificável."},
		Labels:      []string{"aiw", "reader"},
		Priority:    "high",
		Assignee:    "ana",
		Repo:        "./repo",
		Pending:     []string{"assignee"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ExternalID == "" || task.Source != "folder" {
		t.Fatalf("task = %+v", task)
	}
	path := filepath.Join(root, "tasks", task.ExternalID+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"title: Nova tarefa", "labels:", "reader", "## Critérios de aceite", "## Campos pendentes", "- assignee"} {
		if !strings.Contains(content, want) {
			t.Fatalf("arquivo sem %q:\n%s", want, content)
		}
	}
	refs, err := driver.List(context.Background(), source.Filter{Labels: []string{"reader"}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range refs {
		if ref.ID == task.ExternalID {
			found = true
		}
	}
	if !found {
		t.Fatalf("task criada não apareceu no filtro: %+v", refs)
	}
}

func TestCreateAndFetchAttachments(t *testing.T) {
	root, driver := newFixture(t)
	image := filepath.Join(root, "wireframe.png")
	if err := os.WriteFile(image, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	task, err := driver.CreateTask(context.Background(), source.TaskDraft{
		Title:                 "Com imagens",
		Description:           "Compare com o wireframe.",
		Acceptance:            []string{"Layout confere."},
		Attachments:           []string{image},
		AttachmentsAuthorized: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Attachments) != 1 {
		t.Fatalf("attachments = %+v", task.Attachments)
	}
	if _, err := os.Stat(task.Attachments[0]); err != nil {
		t.Fatal(err)
	}
	fetched, err := driver.Fetch(context.Background(), source.Ref{Source: "folder", URL: filepath.Join(root, "tasks", task.ExternalID+".md")})
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	attachments, err := driver.FetchAttachments(context.Background(), fetched, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments baixados = %+v", attachments)
	}
	if _, err := os.Stat(attachments[0].Path); err != nil {
		t.Fatal(err)
	}
}
