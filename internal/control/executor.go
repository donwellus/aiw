package control

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pause"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/runner"
	"github.com/donwellus/aiw/internal/sources"
	"github.com/donwellus/aiw/internal/state"
)

type Executor struct {
	cfg      *config.Config
	store    *state.Store
	ops      *OperationStore
	runner   *runner.Runner
	log      *slog.Logger
	poll     time.Duration
	createPR bool
}

func NewExecutor(cfg *config.Config, store *state.Store, ops *OperationStore, run *runner.Runner, log *slog.Logger, createPR bool) *Executor {
	if log == nil {
		log = slog.Default()
	}
	return &Executor{cfg: cfg, store: store, ops: ops, runner: run, log: log, poll: time.Second, createPR: createPR}
}

func (e *Executor) options(args map[string]any) pipeline.Options {
	options := operationOptions(args)
	if e.createPR {
		options.CreatePR = true
	}
	return options
}

func (e *Executor) Run(ctx context.Context) {
	ticker := time.NewTicker(e.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		operation, err := e.ops.ClaimNext()
		if err != nil {
			e.log.Warn("falha ao processar fila de controle", "error", err)
			continue
		}
		if operation == nil {
			continue
		}
		e.execute(ctx, operation)
	}
}

func (e *Executor) execute(ctx context.Context, operation *Operation) {
	opCtx, cancel := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	go e.watchCancel(opCtx, operation.ID, cancel, watchDone)
	result, err := e.runOperation(opCtx, operation)
	canceled := ctx.Err() != nil || errors.Is(err, context.Canceled) || opCtx.Err() != nil || e.ops.CancelRequested(operation.ID)
	close(watchDone)
	cancel()

	status := StatusSucceeded
	errMessage := ""
	if canceled {
		status = StatusCanceled
	} else if err != nil {
		status = StatusFailed
		errMessage = err.Error()
	}
	if result == nil {
		result = map[string]any{}
	}
	if errMessage != "" {
		result["error"] = errMessage
	}
	if finishErr := e.ops.Finish(operation.ID, status, result, errMessage); finishErr != nil {
		e.log.Warn("falha ao finalizar operação", "operation_id", operation.ID, "error", finishErr)
		return
	}
	e.log.Info("operação de controle concluída", "operation_id", operation.ID, "action", operation.Action, "status", status)
}

func (e *Executor) watchCancel(ctx context.Context, operationID string, cancel context.CancelFunc, done <-chan struct{}) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if e.ops.CancelRequested(operationID) {
				cancel()
				return
			}
		}
	}
}

func (e *Executor) runOperation(ctx context.Context, operation *Operation) (map[string]any, error) {
	switch operation.Action {
	case ActionResume:
		return e.runResume(ctx, operation)
	case ActionResumeAll:
		return e.runResumeAll(ctx, operation)
	case ActionRerun:
		return e.runRerun(ctx, operation)
	case ActionRefine:
		return e.runRefine(ctx, operation)
	case ActionMerge:
		return e.runMerge(ctx, operation)
	default:
		return nil, errors.New("ação de operação desconhecida: " + operation.Action)
	}
}

func (e *Executor) runResume(ctx context.Context, operation *Operation) (map[string]any, error) {
	if err := ValidateRunID(operation.RunID); err != nil {
		return map[string]any{"run_id": operation.RunID}, err
	}
	outcome, runErr := e.runner.Resume(ctx, operation.RunID, e.options(operation.Args))
	result := outcomeResult(outcome, runErr)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if outcome.RunID == "" && runErr != nil {
		return result, runErr
	}
	return result, nil
}

func (e *Executor) runResumeAll(ctx context.Context, operation *Operation) (map[string]any, error) {
	if err := pause.Clear(e.cfg.DataDir()); err != nil {
		return nil, err
	}
	candidates := ResumeCandidates(e.store)
	results := make([]map[string]any, 0, len(candidates))
	for _, runID := range candidates {
		if err := ctx.Err(); err != nil {
			return map[string]any{"cleared_pause": true, "runs": results}, err
		}
		outcome, runErr := e.runner.Resume(ctx, runID, e.options(operation.Args))
		results = append(results, outcomeResult(outcome, runErr))
	}
	return map[string]any{
		"cleared_pause": true,
		"count":         len(results),
		"runs":          results,
	}, nil
}

func (e *Executor) runRefine(ctx context.Context, operation *Operation) (map[string]any, error) {
	target, _ := operation.Args["target"].(string)
	feedback, _ := operation.Args["feedback"].(string)
	previewID, _ := operation.Args["preview_id"].(string)
	if strings.TrimSpace(previewID) == "" {
		e.log.Warn("refine legado sem preview_id; permitindo por compatibilidade", "operation_id", operation.ID, "run_id", operation.RunID)
	} else if _, err := ValidateRefinePreview(e.cfg, operation.RunID, target, feedback, previewID); err != nil {
		return map[string]any{"run_id": operation.RunID, "preview_id": previewID}, err
	}
	_, prepared, err := PrepareRefine(e.cfg, operation.RunID, target, feedback)
	if err != nil {
		return nil, err
	}
	outcome, runErr := e.runner.Resume(ctx, operation.RunID, e.options(operation.Args))
	result := outcomeResult(outcome, runErr)
	result["target"] = prepared.Target
	result["feedback_file"] = prepared.FeedbackFile
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if outcome.RunID == "" && runErr != nil {
		return result, runErr
	}
	return result, nil
}

func (e *Executor) runRerun(ctx context.Context, operation *Operation) (map[string]any, error) {
	if err := ValidateRunID(operation.RunID); err != nil {
		return map[string]any{"run_id": operation.RunID}, err
	}
	run, err := pipeline.LoadRun(e.cfg, operation.RunID)
	if err != nil {
		return nil, err
	}
	src, err := sources.NewRegistry(e.cfg).Open(run.Source)
	if err != nil {
		return nil, err
	}
	task, err := src.Fetch(ctx, run.Ref)
	if err != nil {
		return nil, err
	}
	outcome := e.runner.Execute(ctx, runner.Request{
		Task:    task,
		Ref:     run.Ref,
		Repo:    run.Repo,
		Options: e.options(operation.Args),
	})
	runErr := outcome.Err
	result := outcomeResult(outcome, runErr)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if outcome.RunID == "" && runErr != nil {
		return result, runErr
	}
	return result, nil
}

func operationOptions(args map[string]any) pipeline.Options {
	options := pipeline.Options{}
	if value, ok := args["auto"].(bool); ok {
		options.AutoApprove = value
	}
	if value, ok := args["create_pr"].(bool); ok {
		options.CreatePR = value
	}
	switch value := args["budget_usd"].(type) {
	case float64:
		options.BudgetUSD = value
	case int:
		options.BudgetUSD = float64(value)
	}
	return options
}

func outcomeResult(outcome runner.Outcome, runErr error) map[string]any {
	result := map[string]any{
		"run_id":        outcome.RunID,
		"task_id":       outcome.TaskID,
		"status":        outcome.Status,
		"reason":        outcome.Reason,
		"stage":         outcome.Stage,
		"cost_usd":      outcome.CostUSD,
		"input_tokens":  outcome.InputTokens,
		"output_tokens": outcome.OutputTokens,
		"pr_url":        outcome.PRURL,
	}
	if runErr != nil {
		result["error"] = runErr.Error()
	}
	return result
}

func (e *Executor) runMerge(ctx context.Context, operation *Operation) (map[string]any, error) {
	if err := ValidateRunID(operation.RunID); err != nil {
		return map[string]any{"run_id": operation.RunID}, err
	}
	p := pipeline.New(e.cfg)
	prURL, _ := operation.Args["pr_url"].(string)
	if strings.TrimSpace(prURL) != "" {
		if _, err := p.AdoptPR(ctx, operation.RunID, prURL); err != nil {
			return map[string]any{"run_id": operation.RunID, "pr_url": prURL}, err
		}
	}
	if err := p.MergePR(ctx, operation.RunID); err != nil {
		return map[string]any{"run_id": operation.RunID, "pr_url": prURL}, err
	}
	run, err := pipeline.LoadRun(e.cfg, operation.RunID)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"run_id":  run.ID,
		"task_id": run.Task.ExternalID,
		"status":  run.Status,
		"stage":   run.Stage,
		"pr_url":  run.PRURL,
		"merged":  run.PRMerged,
	}
	if record, found := e.store.FindByRunID(run.ID); found {
		record.Status = "succeeded"
		record.RunID = run.ID
		record.ContentHash = run.Task.ContentHash
		if err := e.store.Put(record); err != nil {
			return result, err
		}
	} else if record, found := e.store.FindByID(run.Task.ExternalID); found {
		record.Status = "succeeded"
		record.RunID = run.ID
		record.ContentHash = run.Task.ContentHash
		if err := e.store.Put(record); err != nil {
			return result, err
		}
	}
	return result, nil
}
