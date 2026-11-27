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

func TestCheckRunningActiveClaim(t *testing.T) {
	run, store := newTestRunner(t)
	task := testTask("001")
	claim, ok, err := store.Claim("folder", "001", "run-1", time.Hour)
	if err != nil || !ok {
		t.Fatalf("Claim: %v %v", ok, err)
	}
	defer func() { _ = claim.Release() }()
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "001", Status: "running", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	outcome, skip := run.Check(task, false)
	if !skip || outcome.Status != "running" || outcome.RunID != "run-1" {
		t.Fatalf("outcome inesperado: %+v skip=%v", outcome, skip)
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
	outcomes := run.Work(context.Background(), jobs, 2, nil, func(Outcome) { atomic.AddInt32(&callbacks, 1) })
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
	go func() { done <- run.Work(ctx, jobs, 1, nil, nil) }()
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

func TestCheckNeedsInput(t *testing.T) {
	run, store := newTestRunner(t)
	task := testTask("001")
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "001", Status: "needs_input", RunID: "run-1", ContentHash: task.ContentHash}); err != nil {
		t.Fatal(err)
	}
	outcome, skip := run.Check(task, false)
	if !skip || outcome.Status != "needs_input" || outcome.RunID != "run-1" {
		t.Fatalf("outcome inesperado: %+v skip=%v", outcome, skip)
	}
	task.ContentHash = "novo"
	if _, skip := run.Check(task, false); skip {
		t.Fatal("hash alterado deveria ser elegível")
	}
}

func TestWorkDrainFinishesInFlight(t *testing.T) {
	run, _ := newTestRunner(t)
	drain := make(chan struct{})
	var started int32
	run.execute = func(ctx context.Context, req Request) Outcome {
		atomic.AddInt32(&started, 1)
		time.Sleep(20 * time.Millisecond)
		return Outcome{TaskID: req.Task.ExternalID, Status: "succeeded"}
	}
	jobs := []Job{
		{Task: testTask("a"), Repo: "/tmp"},
		{Task: testTask("b"), Repo: "/tmp"},
		{Task: testTask("c"), Repo: "/tmp"},
	}
	outcomes := run.Work(context.Background(), jobs, 1, drain, func(Outcome) {
		select {
		case <-drain:
		default:
			close(drain)
		}
	})
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %d, esperado 1 durante drain", len(outcomes))
	}
	if got := atomic.LoadInt32(&started); got != 1 {
		t.Fatalf("jobs iniciados = %d, esperado 1", got)
	}
}

func TestPoolProcessesQuickJobWhileLongRuns(t *testing.T) {
	run, _ := newTestRunner(t)
	started := make(chan string, 2)
	releaseLong := make(chan struct{})
	run.execute = func(ctx context.Context, req Request) Outcome {
		started <- req.Task.ExternalID
		if req.Task.ExternalID == "long" {
			<-releaseLong
		}
		return Outcome{TaskID: req.Task.ExternalID, Status: "succeeded"}
	}
	pool := run.StartPool(context.Background(), 2, 0, nil, nil)
	defer func() {
		pool.Stop()
		pool.Wait()
	}()
	if !pool.Submit(Job{Task: testTask("long"), Repo: "/tmp"}, nil) {
		t.Fatal("não submeteu job long")
	}
	if !pool.Submit(Job{Task: testTask("quick"), Repo: "/tmp"}, nil) {
		t.Fatal("não submeteu job quick")
	}
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case id := <-started:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatalf("jobs iniciados = %v", seen)
		}
	}
	close(releaseLong)
}

func TestMarkQueued(t *testing.T) {
	run, store := newTestRunner(t)
	task := testTask("queued")
	if err := run.MarkQueued([]Candidate{{Task: task}}); err != nil {
		t.Fatal(err)
	}
	record, found := store.Get("folder", "queued")
	if !found || record.Status != "queued" || record.RunID != "" {
		t.Fatalf("record = %+v found=%v", record, found)
	}
	if _, skip := run.Check(task, false); skip {
		t.Fatal("tarefa queued deveria ser elegível")
	}
}

func TestMarkQueuedDoesNotOverwriteActiveRun(t *testing.T) {
	run, store := newTestRunner(t)
	task := testTask("running")
	task.ClaimKey = "folder:running"
	if err := store.Put(state.TaskRecord{Key: task.ClaimKey, Source: "folder", ID: task.ExternalID, Status: "running", RunID: "run-active", ContentHash: task.ContentHash}); err != nil {
		t.Fatal(err)
	}
	claim, acquired, err := store.ClaimByKey(task.ClaimKey, "folder", task.ExternalID, "run-active", time.Hour)
	if err != nil || !acquired {
		t.Fatalf("claim: %v %v", acquired, err)
	}
	defer func() { _ = claim.Release() }()
	if err := run.MarkQueued([]Candidate{{Task: task}}); err != nil {
		t.Fatal(err)
	}
	record, _ := store.FindByKey(task.ClaimKey)
	if record.Status != "running" || record.RunID != "run-active" {
		t.Fatalf("running foi sobrescrito: %+v", record)
	}
}

type fakePRSource struct {
	task *source.Task
	prs  []source.PullRequest
}

func (f fakePRSource) Name() string      { return "github" }
func (f fakePRSource) Caps() source.Caps { return source.Caps{} }
func (f fakePRSource) List(ctx context.Context, filter source.Filter) ([]source.Ref, error) {
	return []source.Ref{{Source: "github", ID: f.task.ExternalID}}, nil
}
func (f fakePRSource) Fetch(ctx context.Context, ref source.Ref) (*source.Task, error) {
	return f.task, nil
}
func (f fakePRSource) ListPullRequests(ctx context.Context) ([]source.PullRequest, error) {
	return f.prs, nil
}

func TestCheckPROpen(t *testing.T) {
	run, store := newTestRunner(t)
	task := testTask("1")
	task.Source = "github"
	if err := store.Put(state.TaskRecord{Source: "github", ID: "1", Status: "pr_open", RunID: "run-pr"}); err != nil {
		t.Fatal(err)
	}
	outcome, skip := run.Check(task, false)
	if !skip || outcome.Status != "pr_open" || outcome.RunID != "run-pr" {
		t.Fatalf("outcome inesperado: %+v skip=%v", outcome, skip)
	}
}

func TestCollectSkipsOpenPullRequest(t *testing.T) {
	run, store := newTestRunner(t)
	task := testTask("1")
	task.Source = "github"
	task.ClaimKey = "github:1"
	src := fakePRSource{
		task: task,
		prs:  []source.PullRequest{{ExternalID: "10", URL: "https://github.com/o/r/pull/10", Branch: "aiw/run-10", TaskID: "1"}},
	}
	candidates, skips, err := run.Collect(context.Background(), src, source.Filter{}, "/tmp", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 || len(skips) != 1 || skips[0].Status != "pr_open" {
		t.Fatalf("candidates=%+v skips=%+v", candidates, skips)
	}
	record, found := store.Get("github", "1")
	if !found || record.Status != "pr_open" || record.RunID != "run-10" {
		t.Fatalf("record = %+v found=%v", record, found)
	}
}
