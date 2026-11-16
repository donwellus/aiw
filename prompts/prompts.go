package prompts

import (
	"embed"

	"github.com/donwellus/aiw/internal/source"
)

//go:embed *.md
var FS embed.FS

type Data struct {
	Task          source.Task
	Repo          string
	Worktree      string
	RunDir        string
	Stage         string
	SpecPath      string
	PlanPath      string
	ImplementPath string
	ReviewPath    string
	DiffPath      string
	TestLogPath   string
	TestLog       string
	Diff          string
	Feedback      string
	ReviewRound   int
	TestCommands  []string
}
