package github

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
)

func setupFakeGH(t *testing.T, listJSON, viewJSON string) (string, *Driver) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	fixtures := filepath.Join(dir, "fixtures")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fixtures, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtures, "list.json"), []byte(listJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtures, "view.json"), []byte(viewJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtures, "pr.json"), []byte(prFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "gh.log")
	script := `#!/bin/sh
echo "$@" >> "$GH_LOG"
case "$1 $2" in
  "issue list") cat "$GH_FIXTURES/list.json" ;;
  "issue view") cat "$GH_FIXTURES/view.json" ;;
  "pr list") cat "$GH_FIXTURES/pr.json" ;;
  "auth status") exit 0 ;;
  "repo view") printf '{"nameWithOwner":"owner/repo"}' ;;
  *) exit 0 ;;
esac
`
	ghPath := filepath.Join(bin, "gh")
	if err := os.WriteFile(ghPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("GH_LOG", logPath)
	t.Setenv("GH_FIXTURES", fixtures)
	driver, err := New("github", dir, config.SourceConfig{
		Driver:        "github",
		Repo:          "owner/repo",
		RepoPath:      "./repo",
		Labels:        []string{"aiw"},
		ExcludeLabels: []string{"aiw:done"},
		CloseOnDone:   boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	return logPath, driver
}

func boolPtr(value bool) *bool { return &value }

const listFixture = `[
  {"number":1,"title":"Primeira","body":"Corpo com tarefas:\n- [ ] critério A\n- [x] critério B","state":"OPEN","url":"https://github.com/owner/repo/issues/1","labels":[{"name":"aiw"}],"assignees":[{"login":"ana"}],"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z"},
  {"number":2,"title":"Concluída","body":"x","state":"OPEN","url":"https://github.com/owner/repo/issues/2","labels":[{"name":"aiw"},{"name":"aiw:done"}],"assignees":[],"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z"}
]`

const prFixture = `[
  {"number":10,"url":"https://github.com/owner/repo/pull/10","headRefName":"aiw/run-10","body":"Tarefa\n\nCloses #2"},
  {"number":11,"url":"https://github.com/owner/repo/pull/11","headRefName":"aiw/run-11","body":"sem referência"}
]`

const viewFixture = `{"number":1,"title":"Primeira","body":"Corpo com tarefas:\n- [ ] critério A\n- [x] critério B","state":"OPEN","url":"https://github.com/owner/repo/issues/1","labels":[{"name":"aiw"}],"assignees":[{"login":"ana"}],"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z","comments":[{"author":{"login":"bia"},"body":"detalhe extra","createdAt":"2026-01-03T00:00:00Z"}]}`

func TestListAndFetch(t *testing.T) {
	logPath, driver := setupFakeGH(t, listFixture, viewFixture)
	refs, err := driver.List(context.Background(), source.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != "1" {
		t.Fatalf("refs inesperados: %+v", refs)
	}
	task, err := driver.Fetch(context.Background(), refs[0])
	if err != nil {
		t.Fatal(err)
	}
	if task.Title != "Primeira" || task.Assignee != "ana" {
		t.Fatalf("task inesperada: %+v", task)
	}
	if len(task.Acceptance) != 2 || len(task.Comments) != 1 {
		t.Fatalf("acceptance/comments inesperados: %+v", task)
	}
	if task.RepoHint != filepath.Join(driver.cfg.RepoPath) && !strings.HasSuffix(task.RepoHint, "repo") {
		t.Fatalf("RepoHint = %q", task.RepoHint)
	}
	if task.ContentHash == "" {
		t.Fatal("ContentHash vazio")
	}
	log := readFile(t, logPath)
	if !strings.Contains(log, "issue list --repo owner/repo") {
		t.Fatalf("comando de list ausente: %s", log)
	}
}

func TestListPullRequests(t *testing.T) {
	_, driver := setupFakeGH(t, listFixture, viewFixture)
	prs, err := driver.ListPullRequests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 {
		t.Fatalf("prs = %+v", prs)
	}
	if prs[0].ExternalID != "10" || prs[0].TaskID != "2" || prs[0].Branch != "aiw/run-10" {
		t.Fatalf("pr 10 = %+v", prs[0])
	}
	if prs[1].TaskID != "" {
		t.Fatalf("pr sem closing não deveria ter task: %+v", prs[1])
	}
}

func TestWriteBack(t *testing.T) {
	logPath, driver := setupFakeGH(t, listFixture, viewFixture)
	ctx := context.Background()
	ref := source.Ref{Source: "github", ID: "1"}
	if err := driver.MarkRunning(ctx, ref, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := driver.MarkDone(ctx, ref, source.RunResult{RunID: "run-1", PRURL: "https://github.com/owner/repo/pull/9", Branch: "aiw/run-1", CostUSD: 0.01}); err != nil {
		t.Fatal(err)
	}
	if err := driver.MarkFailed(ctx, ref, os.ErrDeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	if err := driver.MarkPending(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := driver.Comment(ctx, ref, "comentário de teste"); err != nil {
		t.Fatal(err)
	}
	if err := driver.Check(ctx); err != nil {
		t.Fatal(err)
	}
	log := readFile(t, logPath)
	for _, want := range []string{
		"--add-label aiw:running",
		"--add-label aiw:done",
		"--add-label aiw:failed",
		"--remove-label aiw:running",
		"issue comment 1 --repo owner/repo",
		"issue close 1 --repo owner/repo",
		"auth status",
		"repo view owner/repo",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log sem %q:\n%s", want, log)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
