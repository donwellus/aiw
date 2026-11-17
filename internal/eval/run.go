package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/runner"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/sources"
	"github.com/donwellus/aiw/internal/state"
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
	if outcome.Status == "succeeded" && result.VerifyExit == 0 && result.Error == "" {
		result.Status = "passed"
	} else {
		result.Status = "failed"
		if result.Error == "" {
			result.Error = "pipeline não concluiu: " + outcome.Status
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
