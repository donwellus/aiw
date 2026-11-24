package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
)

type Candidate struct {
	Task *source.Task
	Ref  source.Ref
	Repo string
}

func (r *Runner) MarkQueued(candidates []Candidate) error {
	for _, candidate := range candidates {
		if candidate.Task == nil {
			continue
		}
		record, found := r.lookupRecord(candidate.Task)
		if found && record.Status == "running" && record.RunID != "" {
			if _, active := r.store.FindClaimByRunID(record.RunID); active {
				continue
			}
		}
		sourceName := candidate.Task.Source
		if sourceName == "" {
			sourceName = candidate.Ref.Source
		}
		if err := r.store.Put(state.TaskRecord{
			Key:         candidate.Task.ClaimKey,
			Source:      sourceName,
			ID:          candidate.Task.ExternalID,
			Status:      "queued",
			ContentHash: candidate.Task.ContentHash,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) markPROpen(task *source.Task, pr source.PullRequest) error {
	runID := strings.TrimPrefix(pr.Branch, "aiw/")
	if runID == pr.Branch {
		runID = ""
	}
	return r.store.Put(state.TaskRecord{
		Key:         task.ClaimKey,
		Source:      task.Source,
		ID:          task.ExternalID,
		Status:      "pr_open",
		RunID:       runID,
		ContentHash: task.ContentHash,
	})
}

func (r *Runner) lookupRecord(task *source.Task) (state.TaskRecord, bool) {
	if task.ClaimKey != "" {
		return r.store.FindByKey(task.ClaimKey)
	}
	return r.store.Get(task.Source, task.ExternalID)
}

func (r *Runner) Collect(ctx context.Context, src source.Source, filter source.Filter, repoOverride string, force bool, maxTasks int) ([]Candidate, []Outcome, error) {
	refs, err := src.List(ctx, filter)
	if err != nil {
		return nil, nil, err
	}
	prByTask := map[string]source.PullRequest{}
	if lister, ok := src.(source.PullRequestLister); ok {
		prs, err := lister.ListPullRequests(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("listando PRs abertos: %w", err)
		}
		for _, pr := range prs {
			if pr.TaskID != "" {
				prByTask[pr.TaskID] = pr
			}
			if runID := strings.TrimPrefix(pr.Branch, "aiw/"); runID != pr.Branch {
				if run, loadErr := pipeline.LoadRun(r.cfg, runID); loadErr == nil {
					prByTask[run.Task.ExternalID] = pr
				}
			}
		}
	}
	candidates := make([]Candidate, 0, len(refs))
	var skips []Outcome
	for _, ref := range refs {
		task, err := src.Fetch(ctx, ref)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				skips = append(skips, Outcome{
					TaskID: ref.ID,
					Status: "skipped",
					Reason: "removida da fonte durante a listagem",
				})
				continue
			}
			return nil, nil, fmt.Errorf("buscando %s: %w", ref.ID, err)
		}
		repo := r.cfg.ResolveRepo(task, repoOverride)
		if repo == "" {
			skips = append(skips, Outcome{
				TaskID: task.ExternalID,
				Title:  task.Title,
				Status: "failed",
				Err:    errors.New("repositório não resolvido"),
			})
			continue
		}
		if !force {
			if outcome, skip := r.Check(task, false); skip {
				skips = append(skips, outcome)
				continue
			}
		}
		if pr, ok := prByTask[task.ExternalID]; ok {
			if err := r.markPROpen(task, pr); err != nil {
				r.pipe.Log.Warn("falha ao registrar PR aberto", "task", task.ExternalID, "error", err)
			}
			skips = append(skips, Outcome{
				TaskID: task.ExternalID,
				Title:  task.Title,
				Status: "pr_open",
				PRURL:  pr.URL,
				Reason: "PR aberto; use aiw merge para adotar/mergear",
			})
			continue
		}
		if maxTasks > 0 && len(candidates) >= maxTasks {
			continue
		}
		candidates = append(candidates, Candidate{Task: task, Ref: ref, Repo: repo})
	}
	return candidates, skips, nil
}
