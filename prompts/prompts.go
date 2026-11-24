package prompts

import (
	"embed"

	"github.com/donwellus/aiw/internal/source"
)

//go:embed *.md
var FS embed.FS

type Data struct {
	Task           source.Task
	Repo           string
	Worktree       string
	RunDir         string
	Stage          string
	IntakePath     string
	QuestionsPath  string
	SpecPath       string
	SpecReviewPath string
	PlanPath       string
	PlanReviewPath string
	ImplementPath  string
	ReviewPath     string
	CorrectionPath string
	DiffPath       string
	TestLogPath    string
	TestLog        string
	Diff           string
	Feedback       string
	HumanFeedback  string
	ReviewRound    int
	TestCommands   []string
}
