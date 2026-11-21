package worktree

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
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

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func TestCreateDiffCommitRemove(t *testing.T) {
	repo := newRepo(t)
	dataDir := t.TempDir()
	ctx := context.Background()

	wt, err := Create(ctx, repo, dataDir, "run-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wt.Branch != "aiw/run-1" {
		t.Fatalf("branch = %q", wt.Branch)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("worktree não existe: %v", err)
	}

	if err := os.WriteFile(filepath.Join(wt.Path, "feature.txt"), []byte("conteúdo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt.Path, ".aiw-run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, ".aiw-run", "spec.md"), []byte("spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff, err := wt.Diff(ctx)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "feature.txt") {
		t.Fatalf("diff sem feature.txt:\n%s", diff)
	}
	if strings.Contains(diff, "spec.md") {
		t.Fatalf("diff não deveria conter .aiw-run:\n%s", diff)
	}

	committed, err := wt.Commit(ctx, "aiw: teste")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !committed {
		t.Fatal("esperava commit criado")
	}
	ahead, err := wt.CommitsAhead(ctx)
	if err != nil {
		t.Fatalf("CommitsAhead: %v", err)
	}
	if ahead != 1 {
		t.Fatalf("CommitsAhead = %d", ahead)
	}

	files := runGit(t, wt.Path, "show", "--name-only", "--pretty=format:", "HEAD")
	if !strings.Contains(files, "feature.txt") {
		t.Fatalf("commit sem feature.txt: %q", files)
	}
	if strings.Contains(files, ".aiw-run") {
		t.Fatalf("commit incluiu .aiw-run: %q", files)
	}

	if got := DefaultBranch(ctx, repo); got != "main" {
		t.Fatalf("DefaultBranch = %q", got)
	}

	if err := wt.Remove(ctx, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree ainda existe: %v", err)
	}
}

func TestCreateConcurrent(t *testing.T) {
	repo := newRepo(t)
	dataDir := t.TempDir()
	ctx := context.Background()
	var wg sync.WaitGroup
	var failures int32
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if _, err := Create(ctx, repo, dataDir, "concurrent-"+string(rune('a'+index))); err != nil {
				atomic.AddInt32(&failures, 1)
				t.Errorf("Create %d: %v", index, err)
			}
		}(i)
	}
	wg.Wait()
	if failures != 0 {
		t.Fatalf("falhas = %d", failures)
	}
	output := runGit(t, repo, "worktree", "list", "--porcelain")
	count := strings.Count(output, "worktree ")
	if count < 4 {
		t.Fatalf("worktrees = %d, esperado 4 (principal + 3)", count)
	}
}

func TestRemoveSafetyAndBranches(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	dataDir := t.TempDir()

	dirty, err := Create(ctx, repo, dataDir, "run-dirty")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirty.Path, "dirty.txt"), []byte("pendente\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dirty.Remove(ctx, false); err == nil {
		t.Fatal("remoção segura deveria falhar com worktree sujo")
	}
	if err := dirty.Remove(ctx, true); err != nil {
		t.Fatalf("remoção forçada: %v", err)
	}

	unmerged, err := Create(ctx, repo, dataDir, "run-unmerged")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unmerged.Path, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := unmerged.Commit(ctx, "aiw: unmerged"); err != nil {
		t.Fatal(err)
	}
	if ok, err := BranchExists(ctx, repo, unmerged.Branch); err != nil || !ok {
		t.Fatalf("BranchExists = %v, %v", ok, err)
	}
	if merged, err := BranchMerged(ctx, repo, unmerged.Branch, "main"); err != nil || merged {
		t.Fatalf("BranchMerged = %v, %v", merged, err)
	}
	if err := unmerged.Remove(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := DeleteBranch(ctx, repo, unmerged.Branch, false); err == nil {
		t.Fatal("branch não merged deveria falhar com -d")
	}
	if err := DeleteBranch(ctx, repo, unmerged.Branch, true); err != nil {
		t.Fatalf("delete forçado: %v", err)
	}

	merged, err := Create(ctx, repo, dataDir, "run-merged")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(merged.Path, "merged.txt"), []byte("merged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := merged.Commit(ctx, "aiw: merged"); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "merge", "--no-ff", merged.Branch, "-m", "merge run")
	if ok, err := BranchMerged(ctx, repo, merged.Branch, "main"); err != nil || !ok {
		t.Fatalf("BranchMerged após merge = %v, %v", ok, err)
	}
	if err := merged.Remove(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := DeleteBranch(ctx, repo, merged.Branch, false); err != nil {
		t.Fatalf("delete merged: %v", err)
	}
	if ok, err := BranchExists(ctx, repo, merged.Branch); err != nil || ok {
		t.Fatalf("branch ainda existe: %v, %v", ok, err)
	}
}
