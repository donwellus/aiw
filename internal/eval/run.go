package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/engine"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/runner"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/sources"
	"github.com/donwellus/aiw/internal/state"
	"github.com/donwellus/aiw/prompts"
)

type Options struct {
	Base      *config.Config
	Engine    string
	BudgetUSD float64
	Keep      bool
	OutDir    string
}

type Result struct {
	CaseID         string  `json:"case_id"`
	Status         string  `json:"status"`
	PipelineStatus string  `json:"pipeline_status,omitempty"`
	Stage          string  `json:"stage,omitempty"`
	RunID          string  `json:"run_id,omitempty"`
	Branch         string  `json:"branch,omitempty"`
	RunDir         string  `json:"run_dir,omitempty"`
	ArtifactsDir   string  `json:"artifacts_dir,omitempty"`
	WorkDir        string  `json:"work_dir,omitempty"`
	CostUSD        float64 `json:"cost_usd"`
	DurationMS     int64   `json:"duration_ms"`
	VerifyExit     int     `json:"verify_exit"`
	VerifyOutput   string  `json:"verify_output,omitempty"`
	Error          string  `json:"error,omitempty"`

	ReferenceSimilarity *float64 `json:"reference_similarity,omitempty"`
	ReferenceError      string   `json:"reference_error,omitempty"`
	JudgeScore          *float64 `json:"judge_score,omitempty"`
	JudgeThreshold      float64  `json:"judge_threshold,omitempty"`
	JudgeOutput         string   `json:"judge_output,omitempty"`
	JudgeError          string   `json:"judge_error,omitempty"`
}

type Summary struct {
	Suite      string  `json:"suite"`
	Total      int     `json:"total"`
	Passed     int     `json:"passed"`
	Failed     int     `json:"failed"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMS int64   `json:"duration_ms"`
	OutDir     string  `json:"out_dir"`
	WorkRoot   string  `json:"work_root,omitempty"`
}

func Run(ctx context.Context, suite *Suite, options Options) ([]Result, Summary, error) {
	if options.Base == nil {
		return nil, Summary{}, fmt.Errorf("config base ausente")
	}
	outDir := options.OutDir
	if outDir == "" {
		outDir = filepath.Join(options.Base.DataDir(), "evals", time.Now().UTC().Format("20060102-150405"))
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return nil, Summary{}, err
	}
	if err := os.MkdirAll(absOut, 0o755); err != nil {
		return nil, Summary{}, err
	}
	workRoot, err := os.MkdirTemp("", "aiw-eval-"+safeID(suite.Name)+"-")
	if err != nil {
		return nil, Summary{}, err
	}
	if !options.Keep {
		defer os.RemoveAll(workRoot)
	}

	summary := Summary{Suite: suite.Name, OutDir: absOut}
	if options.Keep {
		summary.WorkRoot = workRoot
	}
	startAll := time.Now()
	results := make([]Result, 0, len(suite.Cases))
	for _, testCase := range suite.Cases {
		result := runCase(ctx, suite, testCase, options, workRoot, absOut)
		if result.Status == "passed" {
			summary.Passed++
		} else {
			summary.Failed++
		}
		summary.CostUSD += result.CostUSD
		results = append(results, result)
	}
	summary.Total = len(results)
	summary.DurationMS = time.Since(startAll).Milliseconds()

	report := struct {
		Suite       string   `json:"suite"`
		GeneratedAt string   `json:"generated_at"`
		Results     []Result `json:"results"`
		Summary     Summary  `json:"summary"`
	}{
		Suite:       suite.Name,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Results:     results,
		Summary:     summary,
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return results, summary, err
	}
	if err := os.WriteFile(filepath.Join(absOut, "results.json"), append(data, '\n'), 0o644); err != nil {
		return results, summary, err
	}
	return results, summary, nil
}

func runCase(ctx context.Context, suite *Suite, testCase Case, options Options, workRoot, outDir string) Result {
	result := Result{CaseID: testCase.ID}
	start := time.Now()
	caseRoot := filepath.Join(workRoot, safeID(testCase.ID))
	repoPath := filepath.Join(caseRoot, "repo")
	tasksDir := filepath.Join(caseRoot, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		result.Status = "error"
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	if err := prepareRepo(ctx, testCase.Repo, repoPath, testCase.BaseRef); err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("preparando repo: %v", err)
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	taskName := filepath.Base(testCase.Task)
	if !strings.EqualFold(filepath.Ext(taskName), ".md") {
		taskName += ".md"
	}
	data, err := os.ReadFile(testCase.Task)
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	if err := os.WriteFile(filepath.Join(tasksDir, taskName), data, 0o644); err != nil {
		result.Status = "error"
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}

	cfg := caseConfig(options.Base, caseRoot, repoPath, options.BudgetUSD)
	src, filter, err := openEvalSource(cfg)
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	refs, err := src.List(ctx, filter)
	if err != nil || len(refs) != 1 {
		if err == nil {
			err = fmt.Errorf("esperava 1 task, encontrei %d", len(refs))
		}
		result.Status = "error"
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	task, err := src.Fetch(ctx, refs[0])
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	task.RepoHint = repoPath
	task.Source = "eval"
	baseCommit := ""
	if output, err := runCommand(ctx, repoPath, "git", "rev-parse", "HEAD"); err == nil {
		baseCommit = strings.TrimSpace(output)
	}

	store, err := state.Open(cfg.StateDir())
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	pipe := pipeline.New(cfg)
	run := runner.New(cfg, store, pipe)
	engine := options.Engine
	if testCase.Engine != "" {
		engine = testCase.Engine
	}
	if engine == "" {
		engine = suite.Engine
	}
	timeout := testCase.Timeout.Std()
	caseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	outcome := run.Execute(caseCtx, runner.Request{
		Task:  task,
		Ref:   refs[0],
		Repo:  repoPath,
		Force: true,
		Options: pipeline.Options{
			AutoApprove:      true,
			EngineOverride:   engine,
			BudgetUSD:        options.BudgetUSD,
			DisableWriteBack: true,
		},
	})

	result.PipelineStatus = outcome.Status
	result.Stage = outcome.Stage
	result.RunID = outcome.RunID
	result.Branch = outcome.Branch
	result.RunDir = outcome.RunDir
	result.CostUSD = outcome.CostUSD
	if outcome.Err != nil {
		result.Error = outcome.Err.Error()
	}
	if outcome.Status == "succeeded" {
		result.VerifyExit = 0
		if strings.TrimSpace(testCase.Verify) != "" && outcome.Worktree != "" {
			output, exitCode, verifyErr := runVerify(caseCtx, outcome.Worktree, testCase.Verify)
			result.VerifyExit = exitCode
			result.VerifyOutput = truncate(output, 8000)
			if verifyErr != nil {
				result.VerifyExit = -1
				result.Error = verifyErr.Error()
			}
			if verifyErr == nil && exitCode != 0 && result.Error == "" {
				result.Error = fmt.Sprintf("verificação falhou com exit %d", exitCode)
			}
		}
	}
	if strings.TrimSpace(testCase.ReferenceRef) != "" && outcome.RunDir != "" && baseCommit != "" {
		generated, _ := os.ReadFile(filepath.Join(outcome.RunDir, "diff.patch"))
		reference, refErr := runCommand(ctx, repoPath, "git", "diff", baseCommit, testCase.ReferenceRef)
		if refErr != nil {
			result.ReferenceError = refErr.Error()
		} else {
			score := diffSimilarity(string(generated), reference)
			result.ReferenceSimilarity = &score
		}
	}

	judgeOK := true
	if suite.Judge != nil {
		result.JudgeThreshold = suite.Judge.Threshold
		score, output, cost, judgeErr := runJudge(caseCtx, cfg, pipe, suite.Judge, task, outcome)
		if judgeErr != nil {
			result.JudgeError = judgeErr.Error()
		} else {
			result.JudgeScore = &score
			result.JudgeOutput = truncate(output, 8000)
			result.CostUSD += cost
		}
		if suite.Judge.Threshold > 0 {
			judgeOK = result.JudgeScore != nil && *result.JudgeScore >= suite.Judge.Threshold
			if !judgeOK && result.JudgeError == "" {
				result.JudgeError = fmt.Sprintf("score abaixo do threshold %.1f", suite.Judge.Threshold)
			}
		}
	}

	if outcome.Status == "succeeded" && result.VerifyExit == 0 && result.Error == "" && judgeOK {
		result.Status = "passed"
	} else {
		result.Status = "failed"
		if result.Error == "" {
			if !judgeOK && result.JudgeError != "" {
				result.Error = result.JudgeError
			} else {
				result.Error = "pipeline não concluiu: " + outcome.Status
			}
		}
	}

	artifactDir := filepath.Join(outDir, safeID(testCase.ID))
	if outcome.RunDir != "" {
		if err := copyDir(outcome.RunDir, artifactDir); err != nil {
			result.Error = strings.TrimSpace(result.Error + "; copiando artefatos: " + err.Error())
		}
		result.ArtifactsDir = artifactDir
		if result.VerifyOutput != "" {
			_ = os.WriteFile(filepath.Join(artifactDir, "verify.log"), []byte(result.VerifyOutput), 0o644)
		}
	}
	if options.Keep {
		result.WorkDir = caseRoot
	}
	result.DurationMS = time.Since(start).Milliseconds()

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = pipe.Telemetry.Close(closeCtx)
	closeCancel()
	return result
}

func openEvalSource(cfg *config.Config) (source.Source, source.Filter, error) {
	src, err := sources.NewRegistry(cfg).Open("eval")
	if err != nil {
		return nil, source.Filter{}, err
	}
	return src, source.Filter{}, nil
}

func caseConfig(base *config.Config, root, repo string, budget float64) *config.Config {
	cfg := *base
	cfg.Root = root
	cfg.Path = ""
	cfg.DefaultSource = "eval"
	cfg.Telemetry = config.TelemetryConfig{Provider: "noop"}
	cfg.Workspace.DefaultRepo = repo
	cfg.Workspace.TestCommands = nil
	cfg.Workspace.Gates = nil
	writeBack := false
	cfg.Sources = map[string]config.SourceConfig{
		"eval": {Driver: "folder", Path: "./tasks", WriteBack: &writeBack},
	}
	cfg.Defaults.Workers = 1
	if budget > 0 {
		cfg.Defaults.BudgetUSD = budget
	}
	return &cfg
}

func prepareRepo(ctx context.Context, src, dst, baseRef string) error {
	if _, err := os.Stat(filepath.Join(src, ".git")); err == nil {
		if _, err := runCommand(ctx, "", "git", "clone", "--quiet", src, dst); err != nil {
			return err
		}
		if baseRef != "" {
			if _, err := runCommand(ctx, dst, "git", "checkout", "--quiet", baseRef); err != nil {
				return err
			}
		}
		return nil
	}
	if err := copyDir(src, dst); err != nil {
		return err
	}
	steps := [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "eval@example.com"},
		{"config", "user.name", "eval"},
		{"add", "-A"},
		{"commit", "-qm", "eval fixture"},
	}
	for _, args := range steps {
		if _, err := runCommand(ctx, dst, "git", args...); err != nil {
			return err
		}
	}
	return nil
}

func runVerify(ctx context.Context, dir, command string) (string, int, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = dir
	var buffer bytes.Buffer
	cmd.Stdout = &buffer
	cmd.Stderr = &buffer
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return buffer.String(), exitErr.ExitCode(), nil
		}
		return buffer.String(), -1, err
	}
	return buffer.String(), 0, nil
}

func runCommand(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		return stdout.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, message)
	}
	return stdout.String(), nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func safeID(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.':
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(builder.String(), "-.")
	if out == "" {
		return "case"
	}
	return out
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "...(truncado)"
}

var judgeScorePattern = regexp.MustCompile(`(?im)^\s*SCORE:\s*([0-9]+(?:\.[0-9]+)?)\s*$`)

type judgePromptData struct {
	TaskTitle       string
	TaskDescription string
	Rubric          string
	Spec            string
	Diff            string
}

func runJudge(ctx context.Context, cfg *config.Config, pipe *pipeline.Pipeline, judge *JudgeConfig, task *source.Task, outcome runner.Outcome) (float64, string, float64, error) {
	if outcome.Worktree == "" {
		return 0, "", 0, errors.New("worktree ausente para o judge")
	}
	spec, _ := os.ReadFile(filepath.Join(outcome.RunDir, "spec.md"))
	diff, _ := os.ReadFile(filepath.Join(outcome.RunDir, "diff.patch"))
	raw, err := prompts.FS.ReadFile("eval-judge.md")
	if err != nil {
		return 0, "", 0, err
	}
	tmpl, err := template.New("eval-judge").Option("missingkey=zero").Parse(string(raw))
	if err != nil {
		return 0, "", 0, err
	}
	var promptBuffer bytes.Buffer
	if err := tmpl.Execute(&promptBuffer, judgePromptData{
		TaskTitle:       task.Title,
		TaskDescription: task.Description,
		Rubric:          judge.Rubric,
		Spec:            truncate(string(spec), 40000),
		Diff:            truncate(string(diff), 40000),
	}); err != nil {
		return 0, "", 0, err
	}
	engineName := judge.Engine
	if engineName == "" {
		engineName = cfg.Defaults.Engine
	}
	eng, err := pipe.Engines.Get(engineName)
	if err != nil {
		return 0, "", 0, err
	}
	result, err := eng.Run(ctx, engine.Request{
		Stage:   "eval-judge",
		Prompt:  promptBuffer.String(),
		Workdir: outcome.Worktree,
		Model:   judge.Model,
		Timeout: cfg.Defaults.Timeout.Std(),
	})
	if err != nil {
		return 0, result.Output, result.Usage.CostUSD, err
	}
	matches := judgeScorePattern.FindAllStringSubmatch(result.Output, -1)
	if len(matches) == 0 {
		return 0, result.Output, result.Usage.CostUSD, errors.New("judge não retornou SCORE")
	}
	score, err := strconv.ParseFloat(matches[len(matches)-1][1], 64)
	if err != nil {
		return 0, result.Output, result.Usage.CostUSD, err
	}
	return score, result.Output, result.Usage.CostUSD, nil
}

func diffSimilarity(generated, reference string) float64 {
	gen := changedLines(generated)
	ref := changedLines(reference)
	if len(gen) == 0 && len(ref) == 0 {
		return 1
	}
	intersection := 0
	for line := range gen {
		if _, ok := ref[line]; ok {
			intersection++
		}
	}
	union := len(gen) + len(ref) - intersection
	if union == 0 {
		return 1
	}
	return float64(intersection) / float64(union)
}

func changedLines(diff string) map[string]struct{} {
	lines := map[string]struct{}{}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"),
			strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "diff "),
			strings.HasPrefix(line, "index "):
			continue
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"):
			lines[strings.TrimSpace(line)] = struct{}{}
		}
	}
	return lines
}
