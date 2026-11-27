package taskcreate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/control"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/sources"
)

type Preview struct {
	Source       string           `json:"source"`
	Driver       string           `json:"driver"`
	DaemonActive bool             `json:"daemon_active"`
	Draft        source.TaskDraft `json:"draft"`
	Missing      []string         `json:"missing,omitempty"`
	Warnings     []string         `json:"warnings,omitempty"`
	Attachments  []string         `json:"attachments,omitempty"`
	Body         string           `json:"body"`
	PreviewID    string           `json:"preview_id"`
}

type Request struct {
	Draft               source.TaskDraft
	PreviewID           string
	AllowIncomplete     bool
	AcknowledgedMissing []string
}

type Result struct {
	Source     string   `json:"source"`
	ExternalID string   `json:"external_id"`
	URL        string   `json:"url"`
	Title      string   `json:"title"`
	Missing    []string `json:"missing,omitempty"`
}

type Service struct {
	cfg      *config.Config
	registry *sources.Registry
}

func New(cfg *config.Config) *Service {
	return &Service{cfg: cfg, registry: sources.NewRegistry(cfg)}
}

func (s *Service) Prepare(ctx context.Context, draft source.TaskDraft) (*Preview, error) {
	name, driver, active, sc, err := s.resolveSource()
	if err != nil {
		return nil, err
	}
	src, err := s.registry.Open(name)
	if err != nil {
		return nil, err
	}
	creator, ok := src.(source.TaskCreator)
	if !ok {
		return nil, fmt.Errorf("fonte %s (%s) não suporta criação de tarefas", name, driver)
	}
	_ = creator
	draft = normalizeDraft(draft, sc, s.cfg)
	if draft.Title == "" {
		return nil, errors.New("title é obrigatório")
	}
	if err := validateAttachments(draft); err != nil {
		return nil, err
	}
	missing, warnings := validateDraft(s.cfg, draft)
	draft.Pending = append([]string(nil), missing...)
	body := source.RenderTaskBody(draft)
	preview := &Preview{
		Source:       name,
		Driver:       driver,
		DaemonActive: active,
		Draft:        draft,
		Missing:      missing,
		Warnings:     warnings,
		Attachments:  append([]string(nil), draft.Attachments...),
		Body:         body,
	}
	preview.PreviewID = previewID(preview)
	return preview, nil
}

func (s *Service) Create(ctx context.Context, req Request) (*Result, error) {
	preview, err := s.Prepare(ctx, req.Draft)
	if err != nil {
		return nil, err
	}
	if req.PreviewID == "" || req.PreviewID != preview.PreviewID {
		return nil, errors.New("preview_id expirado ou inválido; gere um novo preview")
	}
	if len(preview.Missing) > 0 {
		if !req.AllowIncomplete {
			return nil, fmt.Errorf("campos faltando: %s; crie novamente com allow_incomplete=true e acknowledged_missing=%v se for intencional", strings.Join(preview.Missing, ", "), preview.Missing)
		}
		if !sameStrings(normalizeStringList(req.AcknowledgedMissing), preview.Missing) {
			return nil, fmt.Errorf("acknowledged_missing deve ser exatamente %v", preview.Missing)
		}
	}
	src, err := s.registry.Open(preview.Source)
	if err != nil {
		return nil, err
	}
	creator, ok := src.(source.TaskCreator)
	if !ok {
		return nil, fmt.Errorf("fonte %s não suporta criação de tarefas", preview.Source)
	}
	task, err := creator.CreateTask(ctx, preview.Draft)
	if err != nil {
		return nil, err
	}
	return &Result{
		Source:     preview.Source,
		ExternalID: task.ExternalID,
		URL:        firstURL(task),
		Title:      task.Title,
		Missing:    preview.Missing,
	}, nil
}

func (s *Service) resolveSource() (string, string, bool, config.SourceConfig, error) {
	info, active := control.DaemonActive(s.cfg, 15*time.Second)
	name := strings.TrimSpace(s.cfg.DefaultSource)
	if active && strings.TrimSpace(info.Source) != "" {
		name = strings.TrimSpace(info.Source)
	}
	if name == "" {
		return "", "", active, config.SourceConfig{}, errors.New("source não configurada")
	}
	sc, err := s.cfg.SourceConfig(name)
	if err != nil {
		return "", "", active, config.SourceConfig{}, err
	}
	return name, sc.Driver, active, sc, nil
}

func normalizeDraft(draft source.TaskDraft, sc config.SourceConfig, cfg *config.Config) source.TaskDraft {
	draft.Title = strings.TrimSpace(draft.Title)
	draft.Description = strings.TrimSpace(draft.Description)
	draft.Priority = strings.TrimSpace(draft.Priority)
	draft.Assignee = strings.TrimSpace(draft.Assignee)
	draft.Repo = strings.TrimSpace(draft.Repo)
	if draft.Repo == "" {
		draft.Repo = strings.TrimSpace(sc.RepoPath)
	}
	if draft.Repo == "" {
		draft.Repo = strings.TrimSpace(cfg.Workspace.DefaultRepo)
	}
	draft.Acceptance = normalizeStringList(draft.Acceptance)
	labels := append([]string(nil), sc.Labels...)
	labels = append(labels, draft.Labels...)
	draft.Labels = normalizeStringList(labels)
	draft.Pending = nil
	attachments := make([]string, 0, len(draft.Attachments))
	for _, path := range draft.Attachments {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
			}
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		attachments = append(attachments, filepath.Clean(path))
	}
	draft.Attachments = attachments
	return draft
}

func validateAttachments(draft source.TaskDraft) error {
	if len(draft.Attachments) > 20 {
		return errors.New("máximo de 20 anexos por issue")
	}
	if len(draft.Attachments) > 0 && !draft.AttachmentsAuthorized {
		return errors.New("anexos exigem autorização explícita do humano (attachments_authorized=true)")
	}
	seen := map[string]bool{}
	for _, path := range draft.Attachments {
		if seen[path] {
			continue
		}
		seen[path] = true
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("anexo %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("anexo %s não é um arquivo regular", path)
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		default:
			return fmt.Errorf("anexo %s não é uma imagem suportada", path)
		}
		if info.Size() > 10*1024*1024 {
			return fmt.Errorf("anexo %s excede 10 MB", path)
		}
	}
	return nil
}

func validateDraft(cfg *config.Config, draft source.TaskDraft) ([]string, []string) {
	missing := make([]string, 0, 3)
	warnings := make([]string, 0)
	if draft.Description == "" {
		missing = append(missing, "description")
	} else if cfg.Intake.MinDescriptionChars > 0 && len([]rune(draft.Description)) < cfg.Intake.MinDescriptionChars {
		warnings = append(warnings, fmt.Sprintf("description tem menos de %d caracteres", cfg.Intake.MinDescriptionChars))
	}
	if len(draft.Acceptance) == 0 {
		missing = append(missing, "acceptance")
	} else {
		for _, criterion := range draft.Acceptance {
			if placeholderCriterion(criterion) {
				warnings = append(warnings, "acceptance contém critério genérico: "+criterion)
			}
		}
	}
	if draft.Repo == "" {
		missing = append(missing, "repo")
	}
	return missing, warnings
}

func placeholderCriterion(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "todo", "tbd", "a definir", "definir", "critério", "criterio":
		return true
	default:
		return strings.Contains(value, "tbd") || strings.Contains(value, "a definir")
	}
}

func normalizeStringList(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func previewID(preview *Preview) string {
	payload := struct {
		Source      string           `json:"source"`
		Draft       source.TaskDraft `json:"draft"`
		Missing     []string         `json:"missing"`
		Attachments []string         `json:"attachments"`
	}{
		Source:      preview.Source,
		Draft:       preview.Draft,
		Missing:     preview.Missing,
		Attachments: preview.Attachments,
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return "ip-" + hex.EncodeToString(sum[:])[:16]
}

func firstURL(task *source.Task) string {
	if len(task.URLs) == 0 {
		return ""
	}
	return task.URLs[0]
}
