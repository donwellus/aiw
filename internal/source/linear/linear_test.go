package linear

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

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type linearServer struct {
	mu       sync.Mutex
	requests []gqlRequest
}

func (s *linearServer) handler(w http.ResponseWriter, r *http.Request) {
	var request gqlRequest
	_ = json.NewDecoder(r.Body).Decode(&request)
	s.mu.Lock()
	s.requests = append(s.requests, request)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.Contains(request.Query, "query Issues"):
		_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[
			{"id":"uuid-1","identifier":"ENG-1","title":"Primeira","description":"Descrição\n- [ ] critério A\n- [x] critério B","url":"https://linear.app/eng/issue/ENG-1","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z","state":{"name":"Todo"},"labels":{"nodes":[{"name":"aiw"}]},"assignee":{"name":"ana"},"comments":{"nodes":[{"body":"comentário","createdAt":"2026-01-03T00:00:00Z","user":{"name":"bia"}}]}},
			{"id":"uuid-2","identifier":"ENG-2","title":"Concluída","description":"x","url":"https://linear.app/eng/issue/ENG-2","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z","state":{"name":"Done"},"labels":{"nodes":[{"name":"aiw"}]},"assignee":null,"comments":{"nodes":[]}}
		]}}}`))
	case strings.Contains(request.Query, "query Issue("):
		_, _ = w.Write([]byte(`{"data":{"issue":{"id":"uuid-1","identifier":"ENG-1","title":"Primeira","description":"Descrição\n- [ ] critério A\n- [x] critério B","url":"https://linear.app/eng/issue/ENG-1","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z","state":{"name":"Todo"},"labels":{"nodes":[{"name":"aiw"}]},"assignee":{"name":"ana"},"comments":{"nodes":[{"body":"comentário","createdAt":"2026-01-03T00:00:00Z","user":{"name":"bia"}}]}}}}`))
	case strings.Contains(request.Query, "query IssueByNumber"):
		_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[]}}}`))
	case strings.Contains(request.Query, "query States"):
		_, _ = w.Write([]byte(`{"data":{"workflowStates":{"nodes":[{"id":"state-progress","name":"In Progress"},{"id":"state-done","name":"Done"},{"id":"state-todo","name":"Todo"}]}}}`))
	case strings.Contains(request.Query, "mutation UpdateIssue"):
		_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
	case strings.Contains(request.Query, "mutation AddComment"):
		_, _ = w.Write([]byte(`{"data":{"commentCreate":{"success":true}}}`))
	case strings.Contains(request.Query, "query Check"):
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"user-1","name":"Tester"},"teams":{"nodes":[{"id":"team-1","key":"ENG"}]}}}`))
	default:
		_, _ = w.Write([]byte(`{"data":{}}`))
	}
}

func newTestDriver(t *testing.T, server *httptest.Server) *Driver {
	t.Helper()
	t.Setenv("LINEAR_TEST_KEY", "test-key")
	driver, err := New("linear", t.TempDir(), config.SourceConfig{
		Driver:       "linear",
		Team:         "ENG",
		RepoPath:     "./app",
		APIKeyEnv:    "LINEAR_TEST_KEY",
		Endpoint:     server.URL,
		Labels:       []string{"aiw"},
		States:       []string{"Todo", "In Progress"},
		RunningState: "In Progress",
		DoneState:    "Done",
		FailedState:  "Todo",
		PendingState: "Todo",
	})
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

func TestListFetchAndCheck(t *testing.T) {
	collector := &linearServer{}
	server := httptest.NewServer(http.HandlerFunc(collector.handler))
	defer server.Close()
	driver := newTestDriver(t, server)

	refs, err := driver.List(context.Background(), source.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != "ENG-1" || refs[0].ProviderID != "uuid-1" {
		t.Fatalf("refs inesperados: %+v", refs)
	}
	task, err := driver.Fetch(context.Background(), refs[0])
	if err != nil {
		t.Fatal(err)
	}
	if task.Title != "Primeira" || task.Assignee != "ana" || task.Status != "Todo" {
		t.Fatalf("task inesperada: %+v", task)
	}
	if len(task.Acceptance) != 2 || len(task.Comments) != 1 {
		t.Fatalf("acceptance/comments inesperados: %+v", task)
	}
	if !strings.HasSuffix(task.RepoHint, "app") || task.ContentHash == "" {
		t.Fatalf("repo/hash inesperados: %+v", task)
	}
	if err := driver.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWriteBack(t *testing.T) {
	collector := &linearServer{}
	server := httptest.NewServer(http.HandlerFunc(collector.handler))
	defer server.Close()
	driver := newTestDriver(t, server)
	ctx := context.Background()
	ref := source.Ref{Source: "linear", ID: "ENG-1", ProviderID: "uuid-1"}

	if err := driver.MarkRunning(ctx, ref, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := driver.MarkDone(ctx, ref, source.RunResult{RunID: "run-1", Branch: "aiw/run-1", PRURL: "https://github.com/owner/repo/pull/1", CostUSD: 0.01}); err != nil {
		t.Fatal(err)
	}
	if err := driver.MarkFailed(ctx, ref, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	if err := driver.MarkPending(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := driver.Comment(ctx, ref, "comentário extra"); err != nil {
		t.Fatal(err)
	}

	collector.mu.Lock()
	requests := append([]gqlRequest{}, collector.requests...)
	collector.mu.Unlock()
	var states []string
	var comments int
	for _, request := range requests {
		if strings.Contains(request.Query, "mutation UpdateIssue") {
			states = append(states, request.Variables["stateId"].(string))
		}
		if strings.Contains(request.Query, "mutation AddComment") {
			comments++
		}
	}
	wantStates := []string{"state-progress", "state-done", "state-todo", "state-todo"}
	if len(states) != len(wantStates) {
		t.Fatalf("estados = %v", states)
	}
	for i := range wantStates {
		if states[i] != wantStates[i] {
			t.Fatalf("estados = %v, esperado %v", states, wantStates)
		}
	}
	if comments != 3 {
		t.Fatalf("comentários = %d, esperado 3", comments)
	}
}
