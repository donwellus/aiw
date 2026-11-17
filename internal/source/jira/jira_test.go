package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
)

type jiraRecorder struct {
	mu          sync.Mutex
	transitions []string
	comments    int
	basicAuth   bool
}

func (r *jiraRecorder) handler(w http.ResponseWriter, req *http.Request) {
	if user, _, ok := req.BasicAuth(); ok && user == "dev@example.com" {
		r.mu.Lock()
		r.basicAuth = true
		r.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case req.URL.Path == "/rest/api/3/search/jql":
		_, _ = w.Write([]byte(`{"issues":[{"id":"10001","key":"APP-1","fields":{"summary":"Primeira","description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Descrição"}]},{"type":"taskList","content":[{"type":"taskItem","attrs":{"state":"TODO"},"content":[{"type":"text","text":"critério A"}]},{"type":"taskItem","attrs":{"state":"DONE"},"content":[{"type":"text","text":"critério B"}]}]}]},"status":{"name":"To Do"},"labels":["aiw"],"assignee":{"displayName":"Ana"},"created":"2026-01-01T00:00:00.000+0000","updated":"2026-01-02T00:00:00.000+0000","project":{"key":"APP"},"issuetype":{"name":"Task"}}}]}`))
	case req.URL.Path == "/rest/api/3/issue/APP-1":
		_, _ = w.Write([]byte(`{"id":"10001","key":"APP-1","fields":{"summary":"Primeira","description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Descrição"}]},{"type":"taskList","content":[{"type":"taskItem","attrs":{"state":"TODO"},"content":[{"type":"text","text":"critério A"}]},{"type":"taskItem","attrs":{"state":"DONE"},"content":[{"type":"text","text":"critério B"}]}]}]},"status":{"name":"To Do"},"labels":["aiw"],"assignee":{"displayName":"Ana"},"created":"2026-01-01T00:00:00.000+0000","updated":"2026-01-02T00:00:00.000+0000","project":{"key":"APP"},"issuetype":{"name":"Task"},"comment":{"comments":[{"author":{"displayName":"Bia"},"body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"comentário"}]}]},"created":"2026-01-03T00:00:00.000+0000"}]}}}`))
	case req.URL.Path == "/rest/api/3/issue/APP-1/transitions" && req.Method == http.MethodGet:
		_, _ = w.Write([]byte(`{"transitions":[{"id":"11","name":"In Progress","to":{"name":"In Progress"}},{"id":"21","name":"Done","to":{"name":"Done"}},{"id":"31","name":"To Do","to":{"name":"To Do"}}]}`))
	case req.URL.Path == "/rest/api/3/issue/APP-1/transitions" && req.Method == http.MethodPost:
		var body struct {
			Transition struct {
				ID string `json:"id"`
			} `json:"transition"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		r.mu.Lock()
		r.transitions = append(r.transitions, body.Transition.ID)
		r.mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	case req.URL.Path == "/rest/api/3/issue/APP-1/comment":
		r.mu.Lock()
		r.comments++
		r.mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"c1"}`))
	case req.URL.Path == "/rest/api/3/myself":
		_, _ = w.Write([]byte(`{"displayName":"Teste"}`))
	default:
		http.NotFound(w, req)
	}
}

func newTestDriver(t *testing.T, server *httptest.Server) *Driver {
	t.Helper()
	t.Setenv("JIRA_TEST_EMAIL", "dev@example.com")
	t.Setenv("JIRA_TEST_TOKEN", "token")
	driver, err := New("jira", t.TempDir(), config.SourceConfig{
		Driver:            "jira",
		BaseURL:           server.URL,
		JQL:               "project = APP",
		RepoPath:          "./app",
		EmailEnv:          "JIRA_TEST_EMAIL",
		TokenEnv:          "JIRA_TEST_TOKEN",
		RunningTransition: "In Progress",
		DoneTransition:    "Done",
		FailedTransition:  "To Do",
		PendingTransition: "To Do",
	})
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

func TestListFetchAndWriteBack(t *testing.T) {
	recorder := &jiraRecorder{}
	server := httptest.NewServer(http.HandlerFunc(recorder.handler))
	defer server.Close()
	driver := newTestDriver(t, server)
	ctx := context.Background()

	refs, err := driver.List(ctx, source.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != "APP-1" || refs[0].ProviderID != "10001" {
		t.Fatalf("refs inesperados: %+v", refs)
	}
	task, err := driver.Fetch(ctx, refs[0])
	if err != nil {
		t.Fatal(err)
	}
	if task.Title != "Primeira" || task.Assignee != "Ana" || task.Status != "To Do" {
		t.Fatalf("task inesperada: %+v", task)
	}
	if len(task.Acceptance) != 2 || len(task.Comments) != 1 {
		t.Fatalf("acceptance/comments inesperados: %+v", task)
	}
	if !strings.Contains(task.Description, "Descrição") || task.ContentHash == "" {
		t.Fatalf("descrição/hash inesperados: %+v", task)
	}

	if err := driver.MarkRunning(ctx, refs[0], "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := driver.MarkDone(ctx, refs[0], source.RunResult{RunID: "run-1", Branch: "aiw/run-1", CostUSD: 0.01}); err != nil {
		t.Fatal(err)
	}
	if err := driver.MarkFailed(ctx, refs[0], context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	if err := driver.MarkPending(ctx, refs[0]); err != nil {
		t.Fatal(err)
	}
	if err := driver.Comment(ctx, refs[0], "comentário extra"); err != nil {
		t.Fatal(err)
	}
	if err := driver.Check(ctx); err != nil {
		t.Fatal(err)
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	want := []string{"11", "21", "31", "31"}
	if len(recorder.transitions) != len(want) {
		t.Fatalf("transições = %v", recorder.transitions)
	}
	for i := range want {
		if recorder.transitions[i] != want[i] {
			t.Fatalf("transições = %v, esperado %v", recorder.transitions, want)
		}
	}
	if recorder.comments != 3 {
		t.Fatalf("comentários = %d, esperado 3", recorder.comments)
	}
	if !recorder.basicAuth {
		t.Fatal("Basic auth não foi usado")
	}
}
