package worktree

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if wt.Branch != "github.com/donwellus/aiw/run-1" {
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

	if err := wt.Remove(ctx); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree ainda existe: %v", err)
	}
}
