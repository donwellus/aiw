package clean

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
	"github.com/donwellus/aiw/internal/worktree"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, stderr.String())
	}
	return stdout.String()
}

func newCleanProject(t *testing.T) (*config.Config, *state.Store) {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".aiw/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", "initial")
	cfg := &config.Config{
		Root:     root,
		Defaults: config.Defaults{ClaimTTL: config.Duration(time.Hour)},
	}
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	return cfg, store
}

type runOptions struct {
	commit bool
	dirty  bool
}

func addRun(t *testing.T, cfg *config.Config, store *state.Store, id, taskID, status string, opts runOptions) *pipeline.RunState {
	t.Helper()
	ctx := context.Background()
	wt, err := worktree.Create(ctx, cfg.Root, cfg.DataDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	if opts.commit {
		if err := os.WriteFile(filepath.Join(wt.Path, taskID+".txt"), []byte(taskID+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit(ctx, "aiw: "+taskID); err != nil {
			t.Fatal(err)
		}
	}
	if opts.dirty {
		if err := os.WriteFile(filepath.Join(wt.Path, "dirty.txt"), []byte("pendente\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	key := "folder:" + cfg.Root + ":" + taskID
	run := &pipeline.RunState{
		ID:        id,
		Task:      source.Task{Source: "folder", ExternalID: taskID, Title: taskID, ClaimKey: key},
		Source:    "folder",
		Ref:       source.Ref{Source: "folder", ID: taskID, ClaimKey: key},
		Repo:      wt.Repo,
		Worktree:  wt.Path,
		Branch:    wt.Branch,
		Status:    status,
		CreatedAt: time.Now().UTC(),
	}
	if err := pipeline.SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: taskID, Status: status, RunID: id, Key: key}); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestParseStatuses(t *testing.T) {
	statuses, err := ParseStatuses("succeeded, failed,succeeded")
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 || statuses[0] != "succeeded" || statuses[1] != "failed" {
		t.Fatalf("statuses = %v", statuses)
	}
	if _, err := ParseStatuses("running"); err == nil {
		t.Fatal("status ativo deveria falhar")
	}
}

func TestRunCleansWorktreesAndMergedBranches(t *testing.T) {
	cfg, store := newCleanProject(t)
	ctx := context.Background()
	merged := addRun(t, cfg, store, "run-merged", "task-merged", "succeeded", runOptions{commit: true})
	unmerged := addRun(t, cfg, store, "run-unmerged", "task-unmerged", "failed", runOptions{commit: true})
	runGit(t, cfg.Root, "merge", "--no-ff", merged.Branch, "-m", "merge task-merged")

	dry, err := Run(ctx, cfg, store, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || dry.Summary.WorktreesRemoved != 2 || dry.Summary.BranchesDeleted != 1 {
		t.Fatalf("dry-run inesperado: %+v", dry.Summary)
	}
	for _, path := range []string{merged.Worktree, unmerged.Worktree} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry-run removeu %s: %v", path, err)
		}
	}
	if ok, err := worktree.BranchExists(ctx, cfg.Root, merged.Branch); err != nil || !ok {
		t.Fatalf("branch merged sumiu no dry-run: %v %v", ok, err)
	}

	result, err := Run(ctx, cfg, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.WorktreesRemoved != 2 || result.Summary.BranchesDeleted != 1 || result.Summary.Errors != 0 {
		t.Fatalf("limpeza inesperada: %+v %v", result.Summary, result.Errors)
	}
	for _, path := range []string{merged.Worktree, unmerged.Worktree} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("worktree permaneceu em %s: %v", path, err)
		}
	}
	if ok, err := worktree.BranchExists(ctx, cfg.Root, merged.Branch); err != nil || ok {
		t.Fatalf("branch merged ainda existe: %v %v", ok, err)
	}
	if ok, err := worktree.BranchExists(ctx, cfg.Root, unmerged.Branch); err != nil || !ok {
		t.Fatalf("branch não merged deveria permanecer: %v %v", ok, err)
	}
	if entries, err := os.ReadDir(cfg.RunsDir()); err != nil || len(entries) != 2 {
		t.Fatalf("runs deveriam permanecer: %d %v", len(entries), err)
	}

	forced, err := Run(ctx, cfg, store, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if forced.Summary.BranchesDeleted != 1 {
		t.Fatalf("force deveria apagar 1 branch: %+v", forced.Summary)
	}
	if ok, err := worktree.BranchExists(ctx, cfg.Root, unmerged.Branch); err != nil || ok {
		t.Fatalf("branch não merged ainda existe após force: %v %v", ok, err)
	}
}

func TestRunSkipsActiveClaimAndDirtyWorktree(t *testing.T) {
	cfg, store := newCleanProject(t)
	ctx := context.Background()
	active := addRun(t, cfg, store, "run-active", "task-active", "succeeded", runOptions{commit: true})
	claim, acquired, err := store.ClaimByKey(active.Task.ClaimKey, "folder", active.Task.ExternalID, active.ID, time.Hour)
	if err != nil || !acquired {
		t.Fatalf("claim ativo: %v %v", acquired, err)
	}
	result, err := Run(ctx, cfg, store, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.WorktreesRemoved != 0 || result.Summary.WorktreesSkipped != 1 {
		t.Fatalf("claim ativo deveria pular: %+v", result.Summary)
	}
	if _, err := os.Stat(active.Worktree); err != nil {
		t.Fatalf("worktree ativo removido: %v", err)
	}
	if err := claim.Release(); err != nil {
		t.Fatal(err)
	}

	dirty := addRun(t, cfg, store, "run-dirty", "task-dirty", "succeeded", runOptions{commit: true, dirty: true})
	result, err = Run(ctx, cfg, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	foundDirty := false
	for _, action := range result.Actions {
		if action.RunID == dirty.ID {
			foundDirty = true
			if action.WorktreeAction != "skip" || action.Error != "" {
				t.Fatalf("worktree sujo deveria ser pulado: %+v", action)
			}
		}
	}
	if !foundDirty {
		t.Fatal("run suja não apareceu no resultado")
	}
	if _, err := os.Stat(dirty.Worktree); err != nil {
		t.Fatalf("worktree sujo removido sem force: %v", err)
	}

	result, err = Run(ctx, cfg, store, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	foundDirty = false
	for _, action := range result.Actions {
		if action.RunID == dirty.ID {
			foundDirty = true
			if action.WorktreeAction != "remove" {
				t.Fatalf("force deveria remover worktree sujo: %+v", action)
			}
		}
	}
	if !foundDirty {
		t.Fatal("run suja não apareceu no segundo resultado")
	}
	if _, err := os.Stat(dirty.Worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree sujo permaneceu após force: %v", err)
	}
}
