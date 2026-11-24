package control

import (
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

func PrepareRefine(cfg *config.Config, runID, target, feedback string) (*pipeline.RunState, RefineResult, error) {
	target = strings.ToLower(strings.TrimSpace(target))
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return nil, RefineResult{}, errors.New("feedback vazio")
	}
	stage, gateTargets, artifactNames, ok := refinePlan(target)
	if !ok {
		return nil, RefineResult{}, fmt.Errorf("target inválido %q (use intake, spec ou plan)", target)
	}
	run, err := pipeline.LoadRun(cfg, runID)
	if err != nil {
		return nil, RefineResult{}, err
	}
	if run.Status != "paused" {
		switch run.Status {
		case "needs_input":
			return nil, RefineResult{}, errors.New("run está needs_input; use aiw_answer")
		default:
			return nil, RefineResult{}, fmt.Errorf("run precisa estar pausada para refinar (status atual: %s)", run.Status)
		}
	}
	if err := ensureNoCompletedDownstream(run); err != nil {
		return nil, RefineResult{}, err
	}
	if _, err := os.Stat(run.Worktree); err != nil {
		return nil, RefineResult{}, fmt.Errorf("worktree %s inacessível: %w", run.Worktree, err)
	}
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
