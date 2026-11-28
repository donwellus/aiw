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
	if err := os.WriteFile(filepath.Join(fixtures, "review.json"), []byte(reviewFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "gh.log")
	script := `#!/bin/sh
echo "$@" >> "$GH_LOG"
case "$1 $2" in
  "issue list") cat "$GH_FIXTURES/list.json" ;;
  "issue view") cat "$GH_FIXTURES/view.json" ;;
  "issue create") printf 'https://github.com/owner/repo/issues/99\n' ;;
  "pr list") cat "$GH_FIXTURES/pr.json" ;;
  "api graphql") cat "$GH_FIXTURES/review.json" ;;
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

const reviewFixture = `{
  "data": {
    "repository": {
      "pullRequest": {
        "reviewDecision": "CHANGES_REQUESTED",
        "reviews": {
          "nodes": [
            {"id":"R_approved","state":"APPROVED","body":"ok","submittedAt":"2026-01-01T00:00:00Z","url":"https://github.com/owner/repo/pull/10#pullrequestreview-1","author":{"login":"ana"},"commit":{"oid":"abc"}},
            {"id":"R_changes","state":"CHANGES_REQUESTED","body":"Ajuste o erro de parsing.","submittedAt":"2026-01-02T00:00:00Z","url":"https://github.com/owner/repo/pull/10#pullrequestreview-2","author":{"login":"bia"},"commit":{"oid":"abc"}},
            {"id":"R_own","state":"CHANGES_REQUESTED","body":"<!-- aiw:external-reply --> resposta do aiw","submittedAt":"2026-01-02T00:00:00Z","author":{"login":"bot"},"commit":{"oid":"abc"}}
          ]
        },
        "reviewThreads": {
          "nodes": [
            {
              "id":"THREAD_open",
              "isResolved":false,
              "isOutdated":false,
              "comments":{"nodes":[
                {"id":"C_own","body":"<!-- aiw:external-reply --> feito","path":"a.go","line":10,"createdAt":"2026-01-02T00:00:00Z","url":"u1","author":{"login":"bot"}},
                {"id":"C_external","body":"Cubra o caso de timeout.","path":"a.go","line":10,"createdAt":"2026-01-03T00:00:00Z","url":"u2","author":{"login":"ana"}}
              ]}
            },
            {
              "id":"THREAD_resolved",
              "isResolved":true,
              "isOutdated":false,
              "comments":{"nodes":[{"id":"C_resolved","body":"resolvido","path":"b.go","line":1,"createdAt":"2026-01-03T00:00:00Z","url":"u3","author":{"login":"ana"}}]}
            }
          ]
        },
        "comments": {
          "nodes": [
            {"id":"IC_external","body":"Comentário geral externo.","createdAt":"2026-01-04T00:00:00Z","url":"u4","author":{"login":"caio"}},
            {"id":"IC_own","body":"<!-- aiw:external-reply --> resposta","createdAt":"2026-01-04T00:00:00Z","url":"u5","author":{"login":"bot"}}
          ]
        }
      }
    }
  }
}`

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

func TestReadAndReplyPullRequestReview(t *testing.T) {
	logPath, driver := setupFakeGH(t, listFixture, viewFixture)
	ctx := context.Background()
	pr := source.PullRequest{ExternalID: "10", URL: "https://github.com/owner/repo/pull/10", Branch: "aiw/run-10"}
	review, err := driver.ReadPullRequestReview(ctx, pr)
	if err != nil {
		t.Fatal(err)
	}
	if review.Decision != "CHANGES_REQUESTED" {
		t.Fatalf("decision = %q", review.Decision)
	}
	if len(review.Feedback) != 3 {
		t.Fatalf("feedback = %+v", review.Feedback)
	}
	if review.Feedback[0].ID != "review:R_changes" || review.Feedback[1].ID != "thread-comment:C_external" || review.Feedback[1].ThreadID != "THREAD_open" || review.Feedback[2].ID != "pr-comment:IC_external" {
		t.Fatalf("feedback inesperado: %+v", review.Feedback)
	}
	if err := driver.ReplyPullRequestFeedback(ctx, pr, review.Feedback[1], "corrigido"); err != nil {
		t.Fatal(err)
	}
	if err := driver.ReplyPullRequestFeedback(ctx, pr, review.Feedback[2], "corrigido"); err != nil {
		t.Fatal(err)
	}
	log := readFile(t, logPath)
	if !strings.Contains(log, "api graphql") || !strings.Contains(log, "addPullRequestReviewThreadReply") {
		t.Fatalf("resposta de thread ausente: %s", log)
	}
	if !strings.Contains(log, "pr comment https://github.com/owner/repo/pull/10") {
		t.Fatalf("comentário geral ausente: %s", log)
	}
	if !strings.Contains(log, source.ExternalReplyMarker) {
		t.Fatalf("marcador de resposta ausente: %s", log)
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
		source.WritebackMarker,
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

func TestCreateTask(t *testing.T) {
	logPath, driver := setupFakeGH(t, listFixture, viewFixture)
	image := filepath.Join(t.TempDir(), "wireframe.png")
	if err := os.WriteFile(image, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	task, err := driver.CreateTask(context.Background(), source.TaskDraft{
		Title:                 "Nova issue",
		Description:           "Contexto da issue.",
		Acceptance:            []string{"O fluxo funciona."},
		Labels:                []string{"aiw", "backend"},
		Assignee:              "ana",
		Attachments:           []string{image},
		AttachmentsAuthorized: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ExternalID != "99" || task.URLs[0] != "https://github.com/owner/repo/issues/99" {
		t.Fatalf("task = %+v", task)
	}
	if len(task.Acceptance) != 1 || !strings.Contains(task.Description, "## Critérios de aceite") {
		t.Fatalf("conteúdo = %+v", task)
	}
	log := readFile(t, logPath)
	for _, want := range []string{"issue create --repo owner/repo", "--title Nova issue", "--attach " + image, "--label aiw", "--label backend", "--assignee ana"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log sem %q: %s", want, log)
		}
	}
	for _, forbidden := range []string{"Referências visuais", "![Imagem"} {
		if strings.Contains(log, forbidden) {
			t.Fatalf("body não deveria conter %q: %s", forbidden, log)
		}
	}
}

func TestDownloadableAttachmentRefs(t *testing.T) {
	urls, skipped := downloadableAttachmentRefs([]string{
		"/home/donwellus/image.png",
		"https://github.com/user-attachments/assets/abc",
		"https://raw.githubusercontent.com/owner/repo/main/a.jpg",
	})
	if len(urls) != 2 || len(skipped) != 1 || skipped[0] != "/home/donwellus/image.png" {
		t.Fatalf("urls=%+v skipped=%+v", urls, skipped)
	}
}

func TestFetchAttachmentsRejectsOnlyLocalPaths(t *testing.T) {
	_, driver := setupFakeGH(t, listFixture, viewFixture)
	task := &source.Task{Description: "![wireframe](/home/donwellus/wireframe.png)"}
	if _, err := driver.FetchAttachments(context.Background(), task, t.TempDir()); err == nil {
		t.Fatal("paths locais sem URL deveriam falhar")
	}
}

func TestCloseIssue(t *testing.T) {
	logPath, driver := setupFakeGH(t, listFixture, viewFixture)
	if err := driver.CloseIssue(context.Background(), source.Ref{Source: "github", ID: "1"}, "duplicada"); err != nil {
		t.Fatal(err)
	}
	log := readFile(t, logPath)
	if !strings.Contains(log, "issue close 1 --repo owner/repo --comment duplicada") {
		t.Fatalf("fechamento ausente: %s", log)
	}
}

func TestCloseIssueSkipsAlreadyClosedIssue(t *testing.T) {
	closedFixture := strings.Replace(viewFixture, `"state":"OPEN"`, `"state":"CLOSED"`, 1)
	logPath, driver := setupFakeGH(t, listFixture, closedFixture)
	if err := driver.CloseIssue(context.Background(), source.Ref{Source: "github", ID: "1"}, "duplicada"); err != nil {
		t.Fatal(err)
	}
	if log := readFile(t, logPath); strings.Contains(log, "issue close") {
		t.Fatalf("issue já fechada não deveria executar close: %s", log)
	}
}

func TestAllowedAttachmentHost(t *testing.T) {
	for _, host := range []string{
		"github.com",
		"raw.githubusercontent.com",
		"github-production-user-asset-6210df.s3.amazonaws.com",
		"github-production-release-asset-6210df.s3.amazonaws.com",
	} {
		if !allowedAttachmentHost(host) {
			t.Fatalf("host deveria ser permitido: %s", host)
		}
	}
	for _, host := range []string{
		"evil.example.com",
		"s3.amazonaws.com",
		"github-production-user-asset.evil.s3.amazonaws.com",
	} {
		if allowedAttachmentHost(host) {
			t.Fatalf("host não deveria ser permitido: %s", host)
		}
	}
	if !s3AttachmentHost("github-production-user-asset-6210df.s3.amazonaws.com") {
		t.Fatal("host de user asset deveria ser identificado como S3")
	}
}

func TestTaskHashIgnoresAIGeneratedComments(t *testing.T) {
	item := issue{
		Number: 1,
		Title:  "Tarefa",
		Body:   "- [ ] critério",
		State:  "OPEN",
		Comments: []comment{
			{Body: "comentário humano"},
			{Body: source.WritebackMarker + "\naiw: run `run-1` concluída."},
			{Body: "aiw: run `run-old` concluída."},
			{Body: source.ExternalReplyMarker + "\nresposta"},
		},
	}
	task := (&Driver{}).taskFromIssue(item, "github")
	want := source.HashContent(item.Title, item.Body+"\ncomentário humano\n", task.Acceptance)
	if task.ContentHash != want {
		t.Fatalf("hash = %q, esperado %q", task.ContentHash, want)
	}
	if !source.IsAIGeneratedComment("aiw: run `run-old` falhou.") {
		t.Fatal("comentário legado de falha deveria ser reconhecido")
	}
}
