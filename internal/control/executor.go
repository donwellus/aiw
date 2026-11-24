package control

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pause"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/runner"
	"github.com/donwellus/aiw/internal/sources"
	"github.com/donwellus/aiw/internal/state"
)

type Executor struct {
	cfg    *config.Config
	store  *state.Store
	ops    *OperationStore
	runner *runner.Runner
	log    *slog.Logger
	poll   time.Duration
}

func NewExecutor(cfg *config.Config, store *state.Store, ops *OperationStore, run *runner.Runner, log *slog.Logger) *Executor {
	if log == nil {
		log = slog.Default()
	}
	return &Executor{cfg: cfg, store: store, ops: ops, runner: run, log: log, poll: time.Second}
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
	default:
		return nil, errors.New("ação de operação desconhecida: " + operation.Action)
	}
}

func (e *Executor) runResume(ctx context.Context, operation *Operation) (map[string]any, error) {
	if err := ValidateRunID(operation.RunID); err != nil {
		return map[string]any{"run_id": operation.RunID}, err
	}
	outcome, runErr := e.runner.Resume(ctx, operation.RunID, operationOptions(operation.Args))
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
		outcome, runErr := e.runner.Resume(ctx, runID, operationOptions(operation.Args))
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
	_, prepared, err := PrepareRefine(e.cfg, operation.RunID, target, feedback)
	if err != nil {
		return nil, err
	}
	outcome, runErr := e.runner.Resume(ctx, operation.RunID, operationOptions(operation.Args))
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
		Options: operationOptions(operation.Args),
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
