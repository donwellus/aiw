package runner

import (
	"context"
	"errors"
	"fmt"
	"os"

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
		if maxTasks > 0 && len(candidates) >= maxTasks {
			continue
		}
		candidates = append(candidates, Candidate{Task: task, Ref: ref, Repo: repo})
	}
	return candidates, skips, nil
}
