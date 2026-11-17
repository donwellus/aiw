package runner

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
)

func newTestRunner(t *testing.T) (*Runner, *state.Store) {
	t.Helper()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Defaults: config.Defaults{ClaimTTL: config.Duration(time.Hour)}}
	return New(cfg, store, nil), store
}

func testTask(id string) *source.Task {
	return &source.Task{
		Source:      "folder",
		ExternalID:  id,
		Title:       "Tarefa " + id,
		ContentHash: "hash-" + id,
	}
}

func TestCheckSucceededSameHash(t *testing.T) {
	run, store := newTestRunner(t)
	task := testTask("001")
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "001", Status: "succeeded", RunID: "run-1", ContentHash: task.ContentHash}); err != nil {
		t.Fatal(err)
	}
	outcome, skip := run.Check(task, false)
	if !skip || outcome.Status != "skipped" {
		t.Fatalf("esperava skip, obtive %+v skip=%v", outcome, skip)
	}
	if _, skip := run.Check(task, true); skip {
		t.Fatal("force deveria ignorar o skip")
	}
	task.ContentHash = "novo-hash"
	if _, skip := run.Check(task, false); skip {
		t.Fatal("hash diferente deveria ser elegível")
	}
}

func TestCheckPaused(t *testing.T) {
	run, store := newTestRunner(t)
	task := testTask("001")
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "001", Status: "paused", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	outcome, skip := run.Check(task, false)
	if !skip || outcome.Status != "paused" || outcome.RunID != "run-1" {
		t.Fatalf("outcome inesperado: %+v skip=%v", outcome, skip)
	}
}

func TestExecuteBusyWhenClaimActive(t *testing.T) {
	run, store := newTestRunner(t)
	task := testTask("001")
	claim, ok, err := store.Claim("folder", "001", "run-outro", time.Hour)
	if err != nil || !ok {
		t.Fatalf("Claim: %v %v", ok, err)
	}
	defer func() { _ = claim.Release() }()
	outcome := run.Execute(context.Background(), Request{Task: task, Repo: "/tmp", Options: pipeline.Options{RunID: "run-x"}})
	if outcome.Status != "busy" {
		t.Fatalf("status = %q (%+v)", outcome.Status, outcome)
	}
}

func TestWorkConcurrency(t *testing.T) {
	run, _ := newTestRunner(t)
	var active int32
	var maxActive int32
	run.execute = func(ctx context.Context, req Request) Outcome {
		current := atomic.AddInt32(&active, 1)
		for {
			observed := atomic.LoadInt32(&maxActive)
			if current <= observed || atomic.CompareAndSwapInt32(&maxActive, observed, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		return Outcome{TaskID: req.Task.ExternalID, Status: "succeeded"}
	}
	jobs := make([]Job, 0, 6)
	for i := 0; i < 6; i++ {
		task := testTask(string(rune('a' + i)))
		jobs = append(jobs, Job{Task: task, Repo: "/tmp"})
	}
	var callbacks int32
	outcomes := run.Work(context.Background(), jobs, 2, func(Outcome) { atomic.AddInt32(&callbacks, 1) })
	if len(outcomes) != 6 || callbacks != 6 {
		t.Fatalf("outcomes=%d callbacks=%d", len(outcomes), callbacks)
	}
	if maxActive > 2 {
		t.Fatalf("concorrência máxima = %d, esperado <= 2", maxActive)
	}
}

func TestWorkCancel(t *testing.T) {
	run, _ := newTestRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	run.execute = func(ctx context.Context, req Request) Outcome {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return Outcome{TaskID: req.Task.ExternalID, Status: "canceled"}
	}
	jobs := []Job{{Task: testTask("a"), Repo: "/tmp"}, {Task: testTask("b"), Repo: "/tmp"}}
	done := make(chan []Outcome, 1)
	go func() { done <- run.Work(ctx, jobs, 1, nil) }()
	<-started
	cancel()
	select {
	case outcomes := <-done:
		if len(outcomes) == 0 {
			t.Fatal("esperava ao menos um outcome")
		}
	case <-time.After(time.Second):
		t.Fatal("Work não retornou após cancelamento")
	}
}
