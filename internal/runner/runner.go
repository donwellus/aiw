package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	case "running":
		if record.RunID != "" {
			if _, active := r.store.FindClaimByRunID(record.RunID); active {
				out.Status = "running"
				out.RunID = record.RunID
				out.Reason = "já está em execução"
				return out, true
			}
		}
	case "pr_open":
		out.Status = "pr_open"
		out.RunID = record.RunID
		out.Reason = "PR aberto; use aiw merge para adotar/mergear"
		if record.RunID != "" {
			if run, err := pipeline.LoadRun(r.cfg, record.RunID); err == nil {
				out.PRURL = run.PRURL
			}
		}
		return out, true
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
	runCtx, stopCancel := r.watchCancel(ctx, runID)
	defer stopCancel()
	defer os.Remove(r.cancelMarkerPath(runID))
	if runCtx.Err() != nil {
		out.RunID = runID
		out.Status = "canceled"
		out.Reason = "cancelada"
		return out
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
				if err := writer.MarkRunning(runCtx, req.Ref, runID); err != nil {
					r.pipe.Log.Warn("falha ao marcar tarefa em execução na fonte", "task", req.Task.ExternalID, "error", err)
				}
			}
		} else {
			r.pipe.Log.Warn("fonte indisponível para write-back", "source", req.Task.Source, "error", err)
		}
	}

	pipe := r.pipe.WithLogger(r.pipe.Log.With("task", req.Task.ExternalID, "run_id", runID))
	req.Options.RunID = runID
	if req.Options.OnDelivery == nil {
		task := req.Task
		req.Options.OnDelivery = func(run *pipeline.RunState) error {
			return r.store.Put(state.TaskRecord{
				Key:         task.ClaimKey,
				Source:      task.Source,
				ID:          task.ExternalID,
				Status:      "pr_open",
				RunID:       run.ID,
				ContentHash: task.ContentHash,
			})
		}
	}
	result, runErr := pipe.Run(runCtx, req.Task, req.Ref, req.Repo, req.Options)
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
	case errors.Is(runErr, pipeline.ErrPauseRequested):
		out.Status = "paused"
		out.Reason = "pausa solicitada; use aiw resume"
	case errors.Is(runErr, pipeline.ErrPaused):
		out.Status = "paused"
		out.Reason = runErr.Error()
	case errors.Is(runErr, pipeline.ErrNeedsInput):
		out.Status = "needs_input"
	case errors.Is(runErr, pipeline.ErrDeliveryPending):
		out.Status = "paused"
		out.Reason = runErr.Error()
	case errors.Is(runErr, context.Canceled) || errors.Is(runCtx.Err(), context.Canceled):
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

func (r *Runner) Resume(ctx context.Context, runID string, opts pipeline.Options) (Outcome, error) {
	run, err := pipeline.LoadRun(r.cfg, runID)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{
		TaskID:   run.Task.ExternalID,
		Title:    run.Task.Title,
		RunID:    run.ID,
		RunDir:   filepath.Join(r.cfg.RunsDir(), run.ID),
		Branch:   run.Branch,
		Worktree: run.Worktree,
	}
	start := time.Now()
	defer func() { out.Duration = time.Since(start) }()
	if run.Status == "needs_input" {
		return out, fmt.Errorf("run %s aguarda input; edite a tarefa na fonte e execute novamente", run.ID)
	}
	if run.Status == "succeeded" {
		out.Status = "skipped"
		out.Reason = "run já concluída"
		return out, nil
	}
	opts = resumeOptions(run, opts)
	_ = os.Remove(r.cancelMarkerPath(run.ID))
	runCtx, stopCancel := r.watchCancel(ctx, run.ID)
	defer stopCancel()
	defer os.Remove(r.cancelMarkerPath(run.ID))
	claimKey := source.IdentityKey(run.Ref, &run.Task)
	claim, acquired, err := r.store.ClaimByKey(claimKey, run.Task.Source, run.Task.ExternalID, run.ID, r.cfg.Defaults.ClaimTTL.Std())
	if err != nil {
		out.Status = "failed"
		out.Err = fmt.Errorf("claim: %w", err)
		return out, out.Err
	}
	if !acquired {
		out.Status = "busy"
		out.Reason = "claim ativo: outra execução está com esta tarefa"
		return out, nil
	}
	defer func() {
		if err := claim.Release(); err != nil {
			r.pipe.Log.Warn("falha ao liberar claim", "task", run.Task.ExternalID, "error", err)
		}
	}()
	stopHeartbeat := claim.KeepAlive(time.Minute)
	defer stopHeartbeat()

	if err := r.store.Put(state.TaskRecord{
		Key:         run.Task.ClaimKey,
		Source:      run.Task.Source,
		ID:          run.Task.ExternalID,
		Status:      "running",
		RunID:       run.ID,
		ContentHash: run.Task.ContentHash,
	}); err != nil {
		out.Status = "failed"
		out.Err = err
		return out, err
	}
	var src source.Source
	if !opts.DisableWriteBack {
		if opened, err := r.pipe.Sources.Open(run.Task.Source); err == nil {
			src = opened
			if writer, ok := src.(source.StatusWriter); ok {
				if err := writer.MarkRunning(runCtx, run.Ref, run.ID); err != nil {
					r.pipe.Log.Warn("falha ao marcar tarefa em execução na fonte", "task", run.Task.ExternalID, "error", err)
				}
			}
		}
	}
	pipe := r.pipe.WithLogger(r.pipe.Log.With("task", run.Task.ExternalID, "run_id", run.ID))
	if opts.OnDelivery == nil {
		task := run.Task
		opts.OnDelivery = func(run *pipeline.RunState) error {
			return r.store.Put(state.TaskRecord{
				Key:         task.ClaimKey,
				Source:      task.Source,
				ID:          task.ExternalID,
				Status:      "pr_open",
				RunID:       run.ID,
				ContentHash: task.ContentHash,
			})
		}
	}
	result, runErr := pipe.Resume(runCtx, runID, opts)
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
	case errors.Is(runErr, pipeline.ErrPauseRequested):
		out.Status = "paused"
		out.Reason = "pausa solicitada; use aiw resume"
	case errors.Is(runErr, pipeline.ErrPaused):
		out.Status = "paused"
		out.Reason = runErr.Error()
	case errors.Is(runErr, pipeline.ErrNeedsInput):
		out.Status = "needs_input"
	case errors.Is(runErr, pipeline.ErrDeliveryPending):
		out.Status = "paused"
		out.Reason = runErr.Error()
	case errors.Is(runErr, context.Canceled) || errors.Is(runCtx.Err(), context.Canceled):
		out.Status = "canceled"
	default:
		out.Status = "failed"
	}
	record := state.TaskRecord{
		Key:         run.Task.ClaimKey,
		Source:      run.Task.Source,
		ID:          run.Task.ExternalID,
		RunID:       out.RunID,
		ContentHash: run.Task.ContentHash,
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
		r.pipe.Log.Warn("falha ao salvar estado da tarefa", "task", run.Task.ExternalID, "error", err)
	}
	if src != nil {
		switch out.Status {
		case "needs_input", "canceled":
			if pending, ok := src.(source.PendingWriter); ok {
				if err := pending.MarkPending(ctx, run.Ref); err != nil {
					r.pipe.Log.Warn("falha ao devolver tarefa ao inbox", "task", run.Task.ExternalID, "error", err)
				}
			}
		case "failed":
			if result == nil {
				if writer, ok := src.(source.StatusWriter); ok {
					if err := writer.MarkFailed(ctx, run.Ref, runErr); err != nil {
						r.pipe.Log.Warn("falha ao mover tarefa para failed", "task", run.Task.ExternalID, "error", err)
					}
				}
			}
		}
	}
	return out, runErr
}

func (r *Runner) watchCancel(ctx context.Context, runID string) (context.Context, func()) {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	marker := r.cancelMarkerPath(runID)
	check := func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}
	if check() {
		cancel()
	}
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				if check() {
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	return runCtx, func() {
		once.Do(func() {
			close(done)
			cancel()
		})
	}
}

func (r *Runner) cancelMarkerPath(runID string) string {
	return filepath.Join(r.cfg.RunsDir(), runID, pipeline.CancelMarker)
}

func (r *Runner) Work(ctx context.Context, jobs []Job, workers int, drain <-chan struct{}, onOutcome func(Outcome)) []Outcome {
	if len(jobs) == 0 {
		return nil
	}
	pool := r.StartPool(ctx, workers, len(jobs), drain, onOutcome)
	for _, job := range jobs {
		job.Options.Drain = drain
		if !pool.Submit(job, nil) {
			break
		}
	}
	return pool.WaitAll()
}

func resumeOptions(run *pipeline.RunState, opts pipeline.Options) pipeline.Options {
	if run == nil {
		return opts
	}
	if run.CreatePR || strings.TrimSpace(run.PRURL) != "" {
		opts.CreatePR = true
	}
	return opts
}
