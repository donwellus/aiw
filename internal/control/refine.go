package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
)

const (
	TargetIntake = "intake"
	TargetSpec   = "spec"
	TargetPlan   = "plan"
)

var refineStageOrder = []string{"intake", "requirements", "architect", "implement", "test", "review", "deliver"}

type RefineResult struct {
	RunID        string `json:"run_id"`
	Target       string `json:"target"`
	Stage        string `json:"stage"`
	FeedbackFile string `json:"feedback_file"`
}

type RefinePreview struct {
	PreviewID       string            `json:"preview_id"`
	RunID           string            `json:"run_id"`
	Target          string            `json:"target"`
	Status          string            `json:"status"`
	Stage           string            `json:"stage"`
	Gates           map[string]string `json:"gates,omitempty"`
	GatesReset      []string          `json:"gates_reset,omitempty"`
	StagesReset     []string          `json:"stages_reset,omitempty"`
	Artifacts       []string          `json:"artifacts_removed,omitempty"`
	Feedback        string            `json:"feedback"`
	FeedbackFile    string            `json:"feedback_file"`
	CurrentFeedback string            `json:"current_feedback,omitempty"`
	Warnings        []string          `json:"warnings,omitempty"`
}

func PrepareRefine(cfg *config.Config, runID, target, feedback string) (*pipeline.RunState, RefineResult, error) {
	run, stage, gateTargets, artifactNames, err := loadRefineContext(cfg, runID, target, feedback)
	if err != nil {
		return nil, RefineResult{}, err
	}
	target = strings.ToLower(strings.TrimSpace(target))
	feedback = strings.TrimSpace(feedback)
	for _, name := range artifactNames {
		if err := os.Remove(filepath.Join(run.Worktree, ".aiw-run", name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, RefineResult{}, err
		}
	}
	feedbackDir := filepath.Join(cfg.RunsDir(), run.ID, "feedback")
	if err := os.MkdirAll(feedbackDir, 0o755); err != nil {
		return nil, RefineResult{}, err
	}
	if target == TargetSpec || target == TargetIntake {
		_ = os.Remove(filepath.Join(feedbackDir, "plan.md"))
	}
	if target == TargetIntake {
		_ = os.Remove(filepath.Join(feedbackDir, "spec.md"))
	}
	feedbackPath := filepath.Join(feedbackDir, target+".md")
	if err := appendFeedback(feedbackPath, feedback); err != nil {
		return nil, RefineResult{}, err
	}
	targetIndex := indexOfStage(stage)
	for i := range run.Stages {
		if i < targetIndex {
			continue
		}
		run.Stages[i].Status = "pending"
		run.Stages[i].Error = ""
		run.Stages[i].Rounds = 0
		run.Stages[i].CostUSD = 0
		run.Stages[i].StartedAt = time.Time{}
		run.Stages[i].EndedAt = time.Time{}
	}
	if run.Gates == nil {
		run.Gates = map[string]string{}
	}
	for _, gate := range gateTargets {
		run.Gates[gate] = "pending"
	}
	run.Status = "paused"
	run.Stage = stage
	run.Error = ""
	if err := pipeline.SaveRun(cfg, run); err != nil {
		return nil, RefineResult{}, err
	}
	return run, RefineResult{
		RunID:        run.ID,
		Target:       target,
		Stage:        stage,
		FeedbackFile: feedbackPath,
	}, nil
}

func PreviewRefine(cfg *config.Config, runID, target, feedback string) (*RefinePreview, error) {
	run, stage, gateTargets, artifactNames, err := loadRefineContext(cfg, runID, target, feedback)
	if err != nil {
		return nil, err
	}
	target = strings.ToLower(strings.TrimSpace(target))
	feedback = strings.TrimSpace(feedback)
	feedbackFile := filepath.Join(cfg.RunsDir(), run.ID, "feedback", target+".md")
	currentFeedback := ""
	if data, err := os.ReadFile(feedbackFile); err == nil {
		currentFeedback = strings.TrimSpace(string(data))
	}
	preview := &RefinePreview{
		RunID:           run.ID,
		Target:          target,
		Status:          run.Status,
		Stage:           stage,
		Gates:           cloneStringMap(run.Gates),
		GatesReset:      append([]string(nil), gateTargets...),
		StagesReset:     stagesFrom(stage),
		Artifacts:       append([]string(nil), artifactNames...),
		Feedback:        feedback,
		FeedbackFile:    feedbackFile,
		CurrentFeedback: truncateText(currentFeedback, 4000),
		Warnings: []string{
			"Este preview não aprova o refine; a confirmação final é humana.",
			"Confira o texto; o aiw não verifica a autoria do feedback.",
		},
	}
	preview.PreviewID = refinePreviewID(run, target, feedback, currentFeedback)
	return preview, nil
}

func ValidateRefinePreview(cfg *config.Config, runID, target, feedback, previewID string) (*RefinePreview, error) {
	previewID = strings.TrimSpace(previewID)
	if previewID == "" {
		return nil, errors.New("preview_id obrigatório; chame aiw_refine_preview antes de refinar")
	}
	preview, err := PreviewRefine(cfg, runID, target, feedback)
	if err != nil {
		return nil, err
	}
	if preview.PreviewID != previewID {
		return nil, errors.New("preview expirado ou inválido; gere um novo preview")
	}
	return preview, nil
}

func loadRefineContext(cfg *config.Config, runID, target, feedback string) (*pipeline.RunState, string, []string, []string, error) {
	target = strings.ToLower(strings.TrimSpace(target))
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return nil, "", nil, nil, errors.New("feedback vazio")
	}
	stage, gateTargets, artifactNames, ok := refinePlan(target)
	if !ok {
		return nil, "", nil, nil, fmt.Errorf("target inválido %q (use intake, spec ou plan)", target)
	}
	run, err := pipeline.LoadRun(cfg, runID)
	if err != nil {
		return nil, "", nil, nil, err
	}
	if run.Status != "paused" {
		switch run.Status {
		case "needs_input":
			return nil, "", nil, nil, errors.New("run está needs_input; use aiw_answer")
		default:
			return nil, "", nil, nil, fmt.Errorf("run precisa estar pausada para refinar (status atual: %s)", run.Status)
		}
	}
	if err := ensureNoCompletedDownstream(run); err != nil {
		return nil, "", nil, nil, err
	}
	if _, err := os.Stat(run.Worktree); err != nil {
		return nil, "", nil, nil, fmt.Errorf("worktree %s inacessível: %w", run.Worktree, err)
	}
	return run, stage, gateTargets, artifactNames, nil
}

func stagesFrom(stage string) []string {
	index := indexOfStage(stage)
	if index < 0 {
		return nil
	}
	return append([]string(nil), refineStageOrder[index:]...)
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	clone := make(map[string]string, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

func truncateText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "...(truncado)"
}

func refinePreviewID(run *pipeline.RunState, target, feedback, currentFeedback string) string {
	type stageState struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	stages := make([]stageState, 0, len(run.Stages))
	for _, stage := range run.Stages {
		stages = append(stages, stageState{Name: stage.Name, Status: stage.Status})
	}
	payload := struct {
		RunID           string            `json:"run_id"`
		Target          string            `json:"target"`
		Feedback        string            `json:"feedback"`
		Status          string            `json:"status"`
		Stage           string            `json:"stage"`
		Gates           map[string]string `json:"gates,omitempty"`
		Stages          []stageState      `json:"stages"`
		CurrentFeedback string            `json:"current_feedback,omitempty"`
	}{
		RunID:           run.ID,
		Target:          target,
		Feedback:        feedback,
		Status:          run.Status,
		Stage:           run.Stage,
		Gates:           run.Gates,
		Stages:          stages,
		CurrentFeedback: currentFeedback,
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return "rp-" + hex.EncodeToString(sum[:])[:16]
}

func refinePlan(target string) (string, []string, []string, bool) {
	switch target {
	case TargetIntake:
		return "intake", []string{"intake", "spec", "plan"}, []string{
			"intake.md", "questions.md", "assumptions.md",
			"spec.md", "spec-review.md", "plan.md", "plan-review.md",
		}, true
	case TargetSpec:
		return "requirements", []string{"spec", "plan"}, []string{
			"spec.md", "spec-review.md", "plan.md", "plan-review.md",
		}, true
	case TargetPlan:
		return "architect", []string{"plan"}, []string{"plan.md", "plan-review.md"}, true
	default:
		return "", nil, nil, false
	}
}

func ensureNoCompletedDownstream(run *pipeline.RunState) error {
	for _, stage := range []string{"implement", "test", "review", "deliver"} {
		index := indexOfStage(stage)
		if index < 0 || index >= len(run.Stages) {
			continue
		}
		if run.Stages[index].Status != "pending" {
			return fmt.Errorf("refinamento bloqueado: estágio %s já não está pendente (status %s); use uma nova run", stage, run.Stages[index].Status)
		}
	}
	return nil
}

func indexOfStage(stage string) int {
	for index, name := range refineStageOrder {
		if name == stage {
			return index
		}
	}
	return -1
}

func appendFeedback(path, feedback string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	header := fmt.Sprintf("\n## Rodada %s\n\n", time.Now().UTC().Format(time.RFC3339))
	if _, err := file.WriteString(header + feedback + "\n"); err != nil {
		return err
	}
	return nil
}
