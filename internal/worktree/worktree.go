package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var repoLocks sync.Map

type Worktree struct {
	Repo       string
	Path       string
	Branch     string
	BaseCommit string
}

func Create(ctx context.Context, repo, dataDir, runID string) (*Worktree, error) {
	top, err := git(ctx, repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("repositório %s não é um repo git válido: %w", repo, err)
	}
	repoTop := strings.TrimSpace(top)
	base, err := git(ctx, repoTop, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("lendo HEAD de %s: %w", repoTop, err)
	}
	branch := "aiw/" + runID
	path := filepath.Join(dataDir, "worktrees", runID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("worktree já existe em %s", path)
	}
	unlock := lockRepo(repoTop)
	fsUnlock, err := lockWorktreeFS(ctx, repoTop)
	if err != nil {
		unlock()
		return nil, err
	}
	err = addWithRetry(ctx, repoTop, branch, path)
	fsUnlock()
	unlock()
	if err != nil {
		return nil, err
	}
	wt := &Worktree{
		Repo:       repoTop,
		Path:       path,
		Branch:     branch,
		BaseCommit: strings.TrimSpace(base),
	}
	if err := ensureExcluded(ctx, wt.Path, ".aiw-run/"); err != nil {
		return nil, err
	}
	return wt, nil
}

func (w *Worktree) Diff(ctx context.Context) (string, error) {
	if _, err := git(ctx, w.Path, "add", "-A", "-N"); err != nil {
		return "", err
	}
	return git(ctx, w.Path, "diff", w.BaseCommit)
}

func (w *Worktree) Status(ctx context.Context) (string, error) {
	return git(ctx, w.Path, "status", "--porcelain")
}

func (w *Worktree) Changed(ctx context.Context) (bool, error) {
	status, err := w.Status(ctx)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(status) != "", nil
}

func (w *Worktree) CommitsAhead(ctx context.Context) (int, error) {
	out, err := git(ctx, w.Path, "rev-list", "--count", w.BaseCommit+"..HEAD")
	if err != nil {
		return 0, err
	}
	var count int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &count); err != nil {
		return 0, err
	}
	return count, nil
}

func (w *Worktree) Commit(ctx context.Context, message string) (bool, error) {
	if _, err := git(ctx, w.Path, "add", "-A"); err != nil {
		return false, err
	}
	changed, err := w.Changed(ctx)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if _, err := git(ctx, w.Path, "-c", "user.name=aiw", "-c", "user.email=aiw@localhost", "commit", "--no-gpg-sign", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

func (w *Worktree) Push(ctx context.Context) error {
	_, err := git(ctx, w.Path, "push", "-u", "origin", w.Branch)
	return err
}

func (w *Worktree) Remove(ctx context.Context) error {
	unlock := lockRepo(w.Repo)
	fsUnlock, lockErr := lockWorktreeFS(ctx, w.Repo)
	if lockErr != nil {
		unlock()
		return lockErr
	}
	_, err := git(ctx, w.Repo, "worktree", "remove", "--force", w.Path)
	fsUnlock()
	unlock()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Stat(w.Path); os.IsNotExist(statErr) {
			return nil
		}
	}
	return err
}

func DefaultBranch(ctx context.Context, repo string) string {
	out, err := git(ctx, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "--short")
	if err == nil {
		name := strings.TrimSpace(out)
		return strings.TrimPrefix(name, "origin/")
	}
	for _, candidate := range []string{"main", "master"} {
		if _, err := git(ctx, repo, "rev-parse", "--verify", candidate); err == nil {
			return candidate
		}
	}
	out, err = git(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err == nil {
		return strings.TrimSpace(out)
	}
	return "main"
}

func ensureExcluded(ctx context.Context, wtPath, pattern string) error {
	out, err := git(ctx, wtPath, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	excludePath := strings.TrimSpace(out)
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(wtPath, excludePath)
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}
	existing, err := os.ReadFile(excludePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	file, err := os.OpenFile(excludePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		if _, err := file.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = file.WriteString(pattern + "\n")
	return err
}

func lockWorktreeFS(ctx context.Context, repo string) (func(), error) {
	out, err := git(ctx, repo, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	commonDir := strings.TrimSpace(out)
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repo, commonDir)
	}
	lockPath := filepath.Join(commonDir, "aiw-worktree.lock")
	if err := os.MkdirAll(commonDir, 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			_ = file.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > 2*time.Minute {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout aguardando lock de worktree em %s", lockPath)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20*time.Millisecond + time.Duration(time.Now().UnixNano()%20)*time.Millisecond):
		}
	}
}

func lockRepo(repo string) func() {
	value, _ := repoLocks.LoadOrStore(repo, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func addWithRetry(ctx context.Context, repo, branch, path string) error {
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt)*150*time.Millisecond + time.Duration(time.Now().UnixNano()%100)*time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		if _, err := git(ctx, repo, "worktree", "add", "-b", branch, path, "HEAD"); err != nil {
			lastErr = err
			if _, statErr := os.Stat(path); statErr == nil {
				_ = os.RemoveAll(path)
			}
			continue
		}
		return nil
	}
	return lastErr
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
