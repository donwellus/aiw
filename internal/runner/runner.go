package runner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
)

type Runner struct {
	cfg     *config.Config
	store   *state.Store
	pipe    *pipeline.Pipeline
	execute func(context.Context, Request) Outcome
}

type Request struct {
	Task    *source.Task
	Ref     source.Ref
	Repo    string
	Force   bool
	Options pipeline.Options
}

type Outcome struct {
	TaskID       string
	Title        string
	RunID        string
	Status       string
	Stage        string
	Branch       string
	Worktree     string
	RunDir       string
	PRURL        string
	CostUSD      float64
	InputTokens  int
	OutputTokens int
	Duration     time.Duration
	Err          error
	Reason       string
}

type Job struct {
	Task    *source.Task
	Ref     source.Ref
	Repo    string
	Force   bool
	Options pipeline.Options
}

func New(cfg *config.Config, store *state.Store, pipe *pipeline.Pipeline) *Runner {
	return &Runner{cfg: cfg, store: store, pipe: pipe}
}

func (r *Runner) Check(task *source.Task, force bool) (Outcome, bool) {
	out := Outcome{TaskID: task.ExternalID, Title: task.Title}
	if force {
		return out, false
	}
	var record state.TaskRecord
	var ok bool
	if task.ClaimKey != "" {
		record, ok = r.store.FindByKey(task.ClaimKey)
	} else {
		record, ok = r.store.Get(task.Source, task.ExternalID)
	}
	if !ok {
		return out, false
	}
	switch record.Status {
	case "succeeded":
		if record.ContentHash == task.ContentHash {
			out.Status = "skipped"
			out.RunID = record.RunID
			out.Reason = fmt.Sprintf("já concluída com o mesmo conteúdo (run %s)", record.RunID)
			return out, true
		}
	case "paused":
		out.Status = "paused"
		out.RunID = record.RunID
		out.RunDir = filepath.Join(r.cfg.RunsDir(), record.RunID)
		out.Reason = fmt.Sprintf("pausada na run %s; use aiw resume %s", record.RunID, record.RunID)
		return out, true
	case "needs_input":
		if record.ContentHash == task.ContentHash {
			out.Status = "needs_input"
			out.RunID = record.RunID
			out.RunDir = filepath.Join(r.cfg.RunsDir(), record.RunID)
			out.Reason = "aguardando input; edite a tarefa na fonte e execute novamente"
			return out, true
		}
	}
	return out, false
}

func (r *Runner) Execute(ctx context.Context, req Request) (out Outcome) {
	if r.execute != nil {
		return r.execute(ctx, req)
	}
	out = Outcome{TaskID: req.Task.ExternalID, Title: req.Task.Title}
	start := time.Now()
	defer func() {
		out.Duration = time.Since(start)
	}()

	if skip, ok := r.Check(req.Task, req.Force); ok {
		return skip
	}

	runID := req.Options.RunID
	if runID == "" {
		runID = r.pipe.NewRunID(req.Task)
	}
	claim, acquired, err := r.store.ClaimByKey(source.IdentityKey(req.Ref, req.Task), req.Task.Source, req.Task.ExternalID, runID, r.cfg.Defaults.ClaimTTL.Std())
	if err != nil {
		out.Status = "failed"
		out.Err = fmt.Errorf("claim: %w", err)
		return out
	}
	if !acquired {
		out.Status = "busy"
		out.Reason = "claim ativo: outra execução está com esta tarefa"
		return out
	}
	defer func() {
		if err := claim.Release(); err != nil {
			r.pipe.Log.Warn("falha ao liberar claim", "task", req.Task.ExternalID, "error", err)
		}
	}()
	stopHeartbeat := claim.KeepAlive(time.Minute)
	defer stopHeartbeat()

	if skip, ok := r.Check(req.Task, req.Force); ok {
		return skip
	}

	out.RunID = runID
	if err := r.store.Put(state.TaskRecord{
		Key:         req.Task.ClaimKey,
		Source:      req.Task.Source,
		ID:          req.Task.ExternalID,
		Status:      "running",
		RunID:       runID,
		ContentHash: req.Task.ContentHash,
	}); err != nil {
		out.Status = "failed"
		out.Err = fmt.Errorf("registrando estado: %w", err)
		return out
	}

	var src source.Source
	if !req.Options.DisableWriteBack {
		if opened, err := r.pipe.Sources.Open(req.Task.Source); err == nil {
			src = opened
			if writer, ok := src.(source.StatusWriter); ok {
				if err := writer.MarkRunning(ctx, req.Ref, runID); err != nil {
					r.pipe.Log.Warn("falha ao marcar tarefa em execução na fonte", "task", req.Task.ExternalID, "error", err)
				}
			}
		} else {
			r.pipe.Log.Warn("fonte indisponível para write-back", "source", req.Task.Source, "error", err)
		}
	}

	pipe := r.pipe.WithLogger(r.pipe.Log.With("task", req.Task.ExternalID, "run_id", runID))
	req.Options.RunID = runID
	result, runErr := pipe.Run(ctx, req.Task, req.Ref, req.Repo, req.Options)
	if result != nil {
		out.RunID = result.RunID
		out.Stage = result.Stage
		out.Branch = result.Branch
		out.Worktree = result.Worktree
		out.RunDir = result.RunDir
		out.PRURL = result.PRURL
		out.CostUSD = result.Usage.CostUSD
		out.InputTokens = result.Usage.InputTokens
		out.OutputTokens = result.Usage.OutputTokens
	}
	out.Err = runErr

	switch {
	case runErr == nil:
		out.Status = "succeeded"
	case errors.Is(runErr, pipeline.ErrPaused):
		out.Status = "paused"
	case errors.Is(runErr, pipeline.ErrNeedsInput):
		out.Status = "needs_input"
	case errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		out.Status = "canceled"
	default:
		out.Status = "failed"
	}

	record := state.TaskRecord{
		Key:         req.Task.ClaimKey,
		Source:      req.Task.Source,
		ID:          req.Task.ExternalID,
		RunID:       out.RunID,
		ContentHash: req.Task.ContentHash,
	}
	switch out.Status {
	case "succeeded":
		record.Status = "succeeded"
	case "paused":
		record.Status = "paused"
	case "needs_input":
		record.Status = "needs_input"
	case "canceled":
		record.Status = "todo"
	default:
		record.Status = "failed"
	}
	if err := r.store.Put(record); err != nil {
		r.pipe.Log.Warn("falha ao salvar estado da tarefa", "task", req.Task.ExternalID, "error", err)
		if out.Err == nil {
			out.Err = err
		}
	}

	if src != nil {
		switch out.Status {
		case "needs_input", "canceled":
			if pending, ok := src.(source.PendingWriter); ok {
				if err := pending.MarkPending(ctx, req.Ref); err != nil {
					r.pipe.Log.Warn("falha ao devolver tarefa ao inbox", "task", req.Task.ExternalID, "error", err)
				}
			}
		case "failed":
			if result == nil {
				if writer, ok := src.(source.StatusWriter); ok {
					if err := writer.MarkFailed(ctx, req.Ref, runErr); err != nil {
						r.pipe.Log.Warn("falha ao mover tarefa para failed", "task", req.Task.ExternalID, "error", err)
					}
				}
			}
		}
	}
	return out
}

func (r *Runner) Work(ctx context.Context, jobs []Job, workers int, onOutcome func(Outcome)) []Outcome {
	if workers < 1 {
		workers = 1
	}
	queue := make(chan Job, len(jobs))
	for _, job := range jobs {
		queue <- job
	}
	close(queue)

	var mu sync.Mutex
	var outcomes []Outcome
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job, ok := <-queue:
					if !ok {
						return
					}
					outcome := r.Execute(ctx, Request(job))
					mu.Lock()
					outcomes = append(outcomes, outcome)
					if onOutcome != nil {
						onOutcome(outcome)
					}
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return outcomes
}
