package trello

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
)

type recorder struct {
	mu       sync.Mutex
	moves    []string
	comments int
	authOK   bool
}

func (r *recorder) handler(w http.ResponseWriter, req *http.Request) {
	if req.URL.Query().Get("key") == "key" && req.URL.Query().Get("token") == "token" {
		r.mu.Lock()
		r.authOK = true
		r.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	switch req.URL.Path {
	case "/boards/board-1/lists":
		_, _ = w.Write([]byte(`[{"id":"l-todo","name":"To Do"},{"id":"l-doing","name":"Doing"},{"id":"l-done","name":"Done"},{"id":"l-failed","name":"Blocked"}]`))
	case "/boards/board-1/cards":
		_, _ = w.Write([]byte(`[
			{"id":"card-1","idShort":1,"shortLink":"abc123","name":"Primeira","desc":"Descrição\n- [ ] critério desc","idList":"l-todo","url":"https://trello.com/c/abc123","dateLastActivity":"2026-01-02T00:00:00.000Z","labels":[{"name":"aiw"}],"members":[{"fullName":"Ana"}],"idChecklists":["cl1"]},
			{"id":"card-2","idShort":2,"shortLink":"def456","name":"Concluída","desc":"x","idList":"l-done","url":"https://trello.com/c/def456","dateLastActivity":"2026-01-02T00:00:00.000Z","labels":[{"name":"aiw"}],"members":[],"idChecklists":[]}
		]`))
	case "/cards/card-1":
		if req.Method == http.MethodPut {
			r.mu.Lock()
			r.moves = append(r.moves, req.URL.Query().Get("idList"))
			r.mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"card-1","idShort":1,"shortLink":"abc123","name":"Primeira","desc":"Descrição\n- [ ] critério desc","idList":"l-todo","url":"https://trello.com/c/abc123","dateLastActivity":"2026-01-02T00:00:00.000Z","labels":[{"name":"aiw"}],"members":[{"fullName":"Ana"}],"idChecklists":["cl1"]}`))
	case "/cards/card-1/actions":
		_, _ = w.Write([]byte(`[{"date":"2026-01-03T00:00:00.000Z","data":{"text":"comentário"},"memberCreator":{"fullName":"Bia"}}]`))
	case "/cards/card-1/actions/comments":
		r.mu.Lock()
		r.comments++
		r.mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	case "/cards/card-1/checklists":
		_, _ = w.Write([]byte(`[{"name":"Checklist","checkItems":[{"name":"critério A","state":"incomplete"},{"name":"critério B","state":"complete"}]}]`))
	case "/members/me":
		_, _ = w.Write([]byte(`{"username":"tester"}`))
	default:
		http.NotFound(w, req)
	}
}

func newDriver(t *testing.T, server *httptest.Server) *Driver {
	t.Helper()
	t.Setenv("TRELLO_TEST_KEY", "key")
	t.Setenv("TRELLO_TEST_TOKEN", "token")
	driver, err := New("trello", t.TempDir(), config.SourceConfig{
		Driver:      "trello",
		Board:       "board-1",
		List:        "To Do",
		RunningList: "Doing",
		DoneList:    "Done",
		FailedList:  "Blocked",
		PendingList: "To Do",
		Endpoint:    server.URL,
		KeyEnv:      "TRELLO_TEST_KEY",
		TokenEnv:    "TRELLO_TEST_TOKEN",
		RepoPath:    "./app",
	})
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

func TestListFetchWriteBack(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer server.Close()
	driver := newDriver(t, server)
	ctx := context.Background()

	refs, err := driver.List(ctx, source.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != "abc123" || refs[0].ProviderID != "card-1" {
		t.Fatalf("refs inesperados: %+v", refs)
	}
	task, err := driver.Fetch(ctx, refs[0])
	if err != nil {
		t.Fatal(err)
	}
	if task.Title != "Primeira" || task.Assignee != "Ana" || task.Status != "To Do" {
		t.Fatalf("task inesperada: %+v", task)
	}
	if len(task.Acceptance) != 3 || len(task.Comments) != 1 {
		t.Fatalf("acceptance/comments inesperados: %+v", task.Acceptance)
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

	rec.mu.Lock()
	defer rec.mu.Unlock()
	want := []string{"l-doing", "l-done", "l-failed", "l-todo"}
	if len(rec.moves) != len(want) {
		t.Fatalf("movimentos = %v", rec.moves)
	}
	for i := range want {
		if rec.moves[i] != want[i] {
			t.Fatalf("movimentos = %v, esperado %v", rec.moves, want)
		}
	}
	if rec.comments != 3 {
		t.Fatalf("comentários = %d, esperado 3", rec.comments)
	}
	if !rec.authOK {
		t.Fatal("key/token não foram enviados")
	}
}
