package folder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
)

type frontmatter struct {
	ID         string   `yaml:"id"`
	Title      string   `yaml:"title"`
	Repo       string   `yaml:"repo"`
	Labels     []string `yaml:"labels"`
	Priority   string   `yaml:"priority"`
	Assignee   string   `yaml:"assignee"`
	Acceptance []string `yaml:"acceptance"`
	Status     string   `yaml:"status"`
}

type Driver struct {
	name   string
	cfg    config.SourceConfig
	base   string
	doing  string
	done   string
	failed string
}

func New(name, root string, cfg config.SourceConfig) (*Driver, error) {
	base := resolve(root, cfg.Path)
	if base == "" {
		return nil, errors.New("fonte folder sem path configurado")
	}
	return &Driver{
		name:   name,
		cfg:    cfg,
		base:   base,
		doing:  resolve(root, cfg.DoingDir),
		done:   resolve(root, cfg.DoneDir),
		failed: resolve(root, cfg.FailedDir),
	}, nil
}

func resolve(root, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, p)
}

func (d *Driver) Name() string {
	return d.name
}

func (d *Driver) Caps() source.Caps {
	return source.Caps{WriteBack: d.cfg.WriteBackEnabled()}
}

func (d *Driver) List(ctx context.Context, f source.Filter) ([]source.Ref, error) {
	return d.scan(ctx, f, false)
}

func (d *Driver) ListAll(ctx context.Context, f source.Filter) ([]source.Ref, error) {
	return d.scan(ctx, f, true)
}

func (d *Driver) scan(ctx context.Context, f source.Filter, all bool) ([]source.Ref, error) {
	var refs []source.Ref
	err := filepath.WalkDir(d.base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path == d.base {
				return nil
			}
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			if !all && (samePath(path, d.doing) || samePath(path, d.done) || samePath(path, d.failed)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		task, err := ParseFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !source.MatchFilter(task, f) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		location := "inbox"
		switch {
		case within(path, d.doing):
			location = "doing"
		case within(path, d.done):
			location = "done"
		case within(path, d.failed):
			location = "failed"
		}
		refs = append(refs, source.Ref{
			Source:    d.name,
			ID:        task.ExternalID,
			URL:       path,
			UpdatedAt: info.ModTime(),
			ClaimKey:  d.claimKey(task.ExternalID),
			Meta:      map[string]string{"location": location},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	if f.Limit > 0 && len(refs) > f.Limit {
		refs = refs[:f.Limit]
	}
	return refs, nil
}

func (d *Driver) Fetch(ctx context.Context, ref source.Ref) (*source.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := ref.URL
	if path == "" {
		path = filepath.Join(d.base, ref.ID)
		if filepath.Ext(path) == "" {
			path += ".md"
		}
	}
	task, err := ParseFile(path)
	if err != nil {
		return nil, err
	}
	task.Source = d.name
	task.ClaimKey = d.claimKey(task.ExternalID)
	return task, nil
}

func (d *Driver) MarkRunning(ctx context.Context, ref source.Ref, runID string) error {
	if !d.cfg.WriteBackEnabled() || d.doing == "" {
		return nil
	}
	src, err := d.sourceIn(ref, d.base)
	if err != nil || src == "" {
		return err
	}
	return moveFile(src, d.doing)
}

func (d *Driver) MarkDone(ctx context.Context, ref source.Ref, result source.RunResult) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	src, err := d.locate(ref)
	if err != nil || src == "" {
		return err
	}
	return moveFile(src, d.done)
}

func (d *Driver) MarkFailed(ctx context.Context, ref source.Ref, runErr error) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	src, err := d.locate(ref)
	if err != nil || src == "" {
		return err
	}
	return moveFile(src, d.failed)
}

func (d *Driver) MarkPending(ctx context.Context, ref source.Ref) error {
	if !d.cfg.WriteBackEnabled() || d.doing == "" {
		return nil
	}
	src, err := d.locate(ref)
	if err != nil || src == "" {
		return err
	}
	return moveFile(src, d.base)
}

func (d *Driver) Release(ctx context.Context, ref source.Ref) error {
	return d.MarkPending(ctx, ref)
}

func (d *Driver) Comment(ctx context.Context, ref source.Ref, body string) error {
	if !d.cfg.WriteBackEnabled() {
		return nil
	}
	path, err := d.locate(ref)
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("tarefa não encontrada para comentário")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n" + strings.TrimSpace(body) + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func (d *Driver) locate(ref source.Ref) (string, error) {
	if ref.URL != "" {
		if _, err := os.Stat(ref.URL); err == nil {
			return ref.URL, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if d.doing != "" {
			candidate := filepath.Join(d.doing, filepath.Base(ref.URL))
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}
	for _, dir := range []string{d.doing, d.base} {
		if dir == "" {
			continue
		}
		if path := findByID(dir, ref.ID); path != "" {
			return path, nil
		}
	}
	return "", nil
}

func (d *Driver) sourceIn(ref source.Ref, dir string) (string, error) {
	if ref.URL != "" && filepath.Clean(filepath.Dir(ref.URL)) == filepath.Clean(dir) {
		if _, err := os.Stat(ref.URL); err == nil {
			return ref.URL, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if ref.URL != "" {
		candidate := filepath.Join(dir, filepath.Base(ref.URL))
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if path := findByID(dir, ref.ID); path != "" {
		return path, nil
	}
	return "", nil
}

func findByID(dir, id string) string {
	if id == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		task, err := ParseFile(path)
		if err != nil {
			continue
		}
		if task.ExternalID == id || task.ExternalID == strings.TrimSuffix(id, filepath.Ext(id)) {
			return path
		}
	}
	return ""
}

func moveFile(src, destDir string) error {
	if src == "" || destDir == "" {
		return nil
	}
	if filepath.Clean(filepath.Dir(src)) == filepath.Clean(destDir) {
		return nil
	}
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	name := filepath.Base(src)
	dest := filepath.Join(destDir, name)
	if _, err := os.Stat(dest); err == nil {
		base := strings.TrimSuffix(name, filepath.Ext(name))
		dest = filepath.Join(destDir, fmt.Sprintf("%s-%d%s", base, time.Now().UnixNano(), filepath.Ext(name)))
	}
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return err
	}
	return os.Remove(src)
}

func ParseFile(path string) (*source.Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	header, body, hasHeader := splitFrontmatter(data)
	var fm frontmatter
	if hasHeader {
		if err := yaml.Unmarshal(header, &fm); err != nil {
			return nil, fmt.Errorf("frontmatter inválido: %w", err)
		}
	}
	externalID := strings.TrimSpace(fm.ID)
	if externalID == "" {
		externalID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	description := strings.TrimSpace(string(body))
	title := strings.TrimSpace(fm.Title)
	if title == "" {
		title = firstHeading(description)
	}
	if title == "" {
		title = externalID
	}
	status := strings.TrimSpace(fm.Status)
	if status == "" {
		status = "todo"
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	task := &source.Task{
		SchemaVersion: 1,
		Source:        "folder",
		ExternalID:    externalID,
		Title:         title,
		Description:   description,
		Acceptance:    fm.Acceptance,
		Labels:        fm.Labels,
		Priority:      strings.TrimSpace(fm.Priority),
		Assignee:      strings.TrimSpace(fm.Assignee),
		Status:        status,
		RepoHint:      strings.TrimSpace(fm.Repo),
		CreatedAt:     info.ModTime().UTC(),
		UpdatedAt:     info.ModTime().UTC(),
		URLs:          []string{"file://" + abs},
		ProviderMeta:  map[string]string{"path": abs},
	}
	task.ContentHash = source.HashContent(task.Title, task.Description, task.Acceptance)
	return task, nil
}

func splitFrontmatter(data []byte) (header, body []byte, ok bool) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) == 0 || strings.TrimSpace(string(lines[0])) != "---" {
		return nil, data, false
	}
	var buf bytes.Buffer
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(string(lines[i])) == "---" {
			return buf.Bytes(), bytes.Join(lines[i+1:], nil), true
		}
		buf.Write(lines[i])
	}
	return nil, data, false
}

func firstHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return ""
}

func (d *Driver) claimKey(id string) string {
	return "folder:" + filepath.Clean(d.base) + ":" + id
}

func within(path, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
