package clean

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
	"github.com/donwellus/aiw/internal/worktree"
)

var defaultStatuses = []string{"succeeded", "failed", "canceled"}

var terminalStatuses = map[string]bool{
	"succeeded": true,
	"failed":    true,
	"canceled":  true,
}

var activeStatuses = map[string]bool{
	"running":     true,
	"paused":      true,
	"needs_input": true,
}

type Options struct {
	Statuses     []string
	DryRun       bool
	Force        bool
	KeepBranches bool
}

type Action struct {
	RunID          string `json:"run_id"`
	Status         string `json:"status"`
	Repo           string `json:"repo,omitempty"`
	Worktree       string `json:"worktree,omitempty"`
	Branch         string `json:"branch,omitempty"`
	WorktreeAction string `json:"worktree_action"`
	BranchAction   string `json:"branch_action"`
	Reason         string `json:"reason,omitempty"`
	Error          string `json:"error,omitempty"`
}

type Summary struct {
	Runs             int `json:"runs"`
	WorktreesRemoved int `json:"worktrees_removed"`
	BranchesDeleted  int `json:"branches_deleted"`
	WorktreesSkipped int `json:"worktrees_skipped"`
	BranchesSkipped  int `json:"branches_skipped"`
	Errors           int `json:"errors"`
}

type Result struct {
	DryRun  bool     `json:"dry_run"`
	Summary Summary  `json:"summary"`
	Actions []Action `json:"actions"`
	Errors  []string `json:"errors,omitempty"`
}

func ParseStatuses(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return append([]string(nil), defaultStatuses...), nil
	}
	seen := map[string]bool{}
	statuses := make([]string, 0)
	for _, status := range strings.Split(value, ",") {
		status = strings.TrimSpace(status)
		if status == "" {
			continue
		}
		if !terminalStatuses[status] {
			return nil, fmt.Errorf("status %q não pode ser limpo; use succeeded, failed ou canceled", status)
		}
		if !seen[status] {
			seen[status] = true
			statuses = append(statuses, status)
		}
	}
	if len(statuses) == 0 {
		return append([]string(nil), defaultStatuses...), nil
	}
	return statuses, nil
}

func Run(ctx context.Context, cfg *config.Config, store *state.Store, opts Options) (*Result, error) {
	statuses := opts.Statuses
	if len(statuses) == 0 {
		statuses = defaultStatuses
	}
	selected := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		if !terminalStatuses[status] {
			return nil, fmt.Errorf("status %q não pode ser limpo", status)
		}
		selected[status] = true
	}
	runs, err := loadRuns(cfg)
	if err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].CreatedAt.Before(runs[j].CreatedAt)
		}
		return runs[i].ID < runs[j].ID
	})

	result := &Result{DryRun: opts.DryRun, Actions: []Action{}}
	bases := map[string]string{}
	prune := map[string]bool{}
	for _, run := range runs {
		if !selected[run.Status] {
			continue
		}
		action := processRun(ctx, cfg, store, run, opts, bases, prune)
		result.Summary.Runs++
		switch action.WorktreeAction {
		case "remove":
			result.Summary.WorktreesRemoved++
		case "skip":
			result.Summary.WorktreesSkipped++
		}
		switch action.BranchAction {
		case "delete":
			result.Summary.BranchesDeleted++
		case "skip":
			result.Summary.BranchesSkipped++
		}
		if action.Error != "" {
			result.Summary.Errors++
		}
		result.Actions = append(result.Actions, action)
	}
	if !opts.DryRun {
		repos := make([]string, 0, len(prune))
		for repo := range prune {
			repos = append(repos, repo)
		}
		sort.Strings(repos)
		for _, repo := range repos {
			if err := worktree.Prune(ctx, repo); err != nil {
				result.Errors = append(result.Errors, err.Error())
				result.Summary.Errors++
			}
		}
	}
	return result, nil
}

func processRun(ctx context.Context, cfg *config.Config, store *state.Store, run *pipeline.RunState, opts Options, bases map[string]string, prune map[string]bool) (action Action) {
	action = Action{
		RunID:    run.ID,
		Status:   run.Status,
		Repo:     run.Repo,
		Worktree: run.Worktree,
		Branch:   run.Branch,
	}
	skip := func(reason string) Action {
		action.WorktreeAction = "skip"
		action.BranchAction = "skip"
		action.Reason = appendReason(action.Reason, reason)
		return action
	}

	if run.Worktree != "" && !within(cfg.WorktreesDir(), run.Worktree) {
		return skip("worktree fora de .aiw/worktrees")
	}
	if claim, found := store.FindClaimByRunID(run.ID); found {
		return skip(fmt.Sprintf("claim ativo (pid %d)", claim.PID))
	}
	if record, found := store.FindByRunID(run.ID); found && activeStatuses[record.Status] {
		return skip("tarefa com estado ativo: " + record.Status)
	}
	if strings.TrimSpace(run.Task.Source) == "" || strings.TrimSpace(run.Task.ExternalID) == "" {
		return skip("run sem identidade de tarefa")
	}

	var claim *state.Claim
	if !opts.DryRun {
		key := source.IdentityKey(run.Ref, &run.Task)
		acquiredClaim, acquired, err := store.ClaimByKey(key, run.Task.Source, run.Task.ExternalID, run.ID, 5*time.Minute)
		if err != nil {
			action.Error = err.Error()
			return skip("falha ao adquirir claim")
		}
		if !acquired {
			return skip("claim ativo: outra execução está com a tarefa")
		}
		claim = acquiredClaim
		defer func() {
			if err := claim.Release(); err != nil {
				action.Error = appendError(action.Error, err.Error())
			}
		}()
		fresh, err := pipeline.LoadRun(cfg, run.ID)
		if err != nil {
			action.Error = err.Error()
			return skip("falha ao reler run.json")
		}
		run = fresh
		action.Status = run.Status
		action.Repo = run.Repo
		action.Worktree = run.Worktree
		action.Branch = run.Branch
		if !terminalStatuses[run.Status] {
			return skip("status mudou para " + run.Status)
		}
		if record, found := store.FindByRunID(run.ID); found && activeStatuses[record.Status] {
			return skip("tarefa com estado ativo: " + record.Status)
		}
	}

	if run.Worktree == "" {
		action.WorktreeAction = "none"
	} else {
		if _, err := os.Stat(run.Worktree); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				action.Error = err.Error()
				return skip("falha ao inspecionar worktree")
			}
			action.WorktreeAction = "missing"
			prune[run.Repo] = true
		} else {
			wt := &worktree.Worktree{Repo: run.Repo, Path: run.Worktree, Branch: run.Branch}
			status, err := wt.Status(ctx)
			if err != nil {
				action.Error = err.Error()
				return skip("falha ao verificar worktree")
			}
			if strings.TrimSpace(status) != "" && !opts.Force {
				action.WorktreeAction = "skip"
				action.BranchAction = "skip"
				action.Reason = appendReason(action.Reason, "worktree sujo; use --force para remover")
				return action
			}
			if opts.DryRun {
				action.WorktreeAction = "remove"
			} else {
				if err := wt.Remove(ctx, opts.Force); err != nil {
					action.Error = err.Error()
					action.WorktreeAction = "skip"
					action.BranchAction = "skip"
					action.Reason = appendReason(action.Reason, "falha ao remover worktree")
					return action
				}
				action.WorktreeAction = "remove"
				prune[run.Repo] = true
			}
		}
	}

	if opts.KeepBranches {
		action.BranchAction = "keep"
		return action
	}
	if run.Branch == "" || !strings.HasPrefix(run.Branch, "aiw/") {
		action.BranchAction = "skip"
		action.Reason = appendReason(action.Reason, "branch fora do prefixo aiw/")
		return action
	}
	exists, err := worktree.BranchExists(ctx, run.Repo, run.Branch)
	if err != nil {
		action.Error = err.Error()
		action.BranchAction = "skip"
		return action
	}
	if !exists {
		action.BranchAction = "none"
		return action
	}
	base, ok := bases[run.Repo]
	if !ok {
		base = worktree.DefaultBranch(ctx, run.Repo)
		bases[run.Repo] = base
	}
	merged, err := worktree.BranchMerged(ctx, run.Repo, run.Branch, base)
	if err != nil {
		action.Error = err.Error()
		action.BranchAction = "skip"
		return action
	}
	if !merged && !opts.Force {
		action.BranchAction = "skip"
		action.Reason = appendReason(action.Reason, fmt.Sprintf("branch não merged em %s; use --force", base))
		return action
	}
	if opts.DryRun {
		action.BranchAction = "delete"
		return action
	}
	if err := worktree.DeleteBranch(ctx, run.Repo, run.Branch, !merged); err != nil {
		action.Error = err.Error()
		action.BranchAction = "skip"
		return action
	}
	action.BranchAction = "delete"
	return action
}

func loadRuns(cfg *config.Config) ([]*pipeline.RunState, error) {
	entries, err := os.ReadDir(cfg.RunsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	runs := make([]*pipeline.RunState, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		run, err := pipeline.LoadRun(cfg, entry.Name())
		if err != nil {
			continue
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func within(base, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func appendReason(current, reason string) string {
	if strings.TrimSpace(current) == "" {
		return reason
	}
	return current + "; " + reason
}

func appendError(current, err string) string {
	if strings.TrimSpace(current) == "" {
		return err
	}
	return current + "; " + err
}
