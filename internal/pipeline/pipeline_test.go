package pipeline

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/source/folder"
	"github.com/donwellus/aiw/internal/telemetry"
	"github.com/donwellus/aiw/internal/worktree"
)

func setupProject(t *testing.T, gates string) (string, *config.Config, source.Ref, *source.Task) {
	t.Helper()
	root := t.TempDir()

	cfgYAML := `engines:
  fake:
    driver: fake
defaults:
  engine: fake
  timeout: 1m
  budget_usd: 5
  max_test_retries: 1
  max_review_rounds: 1
telemetry:
  provider: noop
sources:
  folder:
    driver: folder
    path: ./tasks
    done_dir: ./tasks/done
    failed_dir: ./tasks/failed
default_source: folder
workspace:
  default_repo: ./demo
  test_commands: ["true"]
  gates:
` + gates
	writeTestFile(t, filepath.Join(root, ".aiw", "config.yaml"), cfgYAML)
	writeTestFile(t, filepath.Join(root, "tasks", "001.md"), `---
id: 001
title: Tarefa de demonstração
repo: ./demo
labels: [aiw]
acceptance:
  - O pipeline executa de ponta a ponta.
---
Implemente algo simples para validar o pipeline.
`)

	repo := filepath.Join(root, "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, "init", "-b", "main")
	runTestGit(t, repo, "config", "user.email", "test@example.com")
	runTestGit(t, repo, "config", "user.name", "Test")
	writeTestFile(t, filepath.Join(repo, "README.md"), "# demo\n")
	runTestGit(t, repo, "add", "-A")
	runTestGit(t, repo, "commit", "-m", "initial")

	cfg, err := config.Load(root, "")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	driver, err := folder.New("folder", root, cfg.Sources["folder"])
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	refs, err := driver.List(ctx, source.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %d", len(refs))
	}
	task, err := driver.Fetch(ctx, refs[0])
	if err != nil {
		t.Fatal(err)
	}
	return root, cfg, refs[0], task
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runTestGit(t *testing.T, dir string, args ...string) string {
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

func TestRunEndToEndWithFakeEngine(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: false\n    plan: false\n")
	recorder := telemetry.NewRecorder()
	p := New(cfg)
	p.Telemetry = recorder

	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{AutoApprove: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "succeeded" {
		t.Fatalf("status = %q", res.Status)
	}

	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if run.Status != "succeeded" {
		t.Fatalf("run.status = %q (erro: %s)", run.Status, run.Error)
	}
	for _, name := range []string{"spec.md", "plan.md", "implement.md", "test.log", "diff.patch", "review.md", "deliver.md"} {
		if _, err := os.Stat(filepath.Join(run.Artifacts[name])); err != nil {
			t.Fatalf("artefato %s ausente: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, "AIW_FAKE_CHANGE.md")); err != nil {
		t.Fatalf("mudança do fake ausente: %v", err)
	}
	log := runTestGit(t, res.Worktree, "log", "--oneline", "-1")
	if !strings.Contains(log, "aiw:") {
		t.Fatalf("commit não criado: %q", log)
	}

	if _, err := os.Stat(filepath.Join(root, "tasks", "001.md")); !os.IsNotExist(err) {
		t.Fatal("tarefa deveria ter sido movida para done")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "done", "001.md")); err != nil {
		t.Fatalf("tarefa não está em done: %v", err)
	}

	if _, err := os.Stat(filepath.Join(cfg.RunsDir(), res.RunID, "events.jsonl")); err != nil {
		t.Fatalf("events.jsonl ausente: %v", err)
	}

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("traces = %d", len(traces))
	}
	spans := map[string]bool{}
	for _, span := range traces[0].Spans {
		spans[span.Name] = true
	}
	for _, stage := range []string{"stage.requirements", "stage.architect", "stage.implement", "stage.test", "stage.review", "stage.deliver"} {
		if !spans[stage] {
			t.Fatalf("span %s ausente (spans: %v)", stage, spans)
		}
	}
	if len(traces[0].Scores) == 0 || traces[0].Scores[0].Value != 1 {
		t.Fatalf("score de sucesso ausente: %+v", traces[0].Scores)
	}
}

func TestPausedGateAndResume(t *testing.T) {
	t.Setenv("AIW_NONINTERACTIVE", "1")
	root, cfg, ref, task := setupProject(t, "    spec: true\n    plan: false\n")
	p := New(cfg)
	p.Telemetry = telemetry.NewRecorder()

	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{})
	if err == nil || !strings.Contains(err.Error(), "pausada") {
		t.Fatalf("esperava ErrPaused, obtive %v", err)
	}
	if res == nil {
		t.Fatal("resultado vazio")
	}
	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "paused" {
		t.Fatalf("status = %q", run.Status)
	}
	if run.Gates["spec"] != "pending" {
		t.Fatalf("gate spec = %q", run.Gates["spec"])
	}
	run.Gates["spec"] = "approved"
	if err := SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}

	p2 := New(cfg)
	p2.Telemetry = telemetry.NewRecorder()
	resumed, err := p2.Resume(context.Background(), res.RunID, Options{AutoApprove: true})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Status != "succeeded" {
		t.Fatalf("status após resume = %q", resumed.Status)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "done", "001.md")); err != nil {
		t.Fatalf("tarefa não foi movida após resume: %v", err)
	}
}

func TestIntakeNeedsInput(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: auto\n    plan: auto\n")
	task.Description += "\nneeds-input"
	p := New(cfg)
	p.Telemetry = telemetry.NewRecorder()

	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{})
	if !errors.Is(err, ErrNeedsInput) {
		t.Fatalf("esperava ErrNeedsInput, obtive %v", err)
	}
	if res == nil || res.Status != "needs_input" {
		t.Fatalf("resultado inesperado: %+v", res)
	}
	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "needs_input" {
		t.Fatalf("status = %q", run.Status)
	}
	if run.Gates["intake"] == "approved" {
		t.Fatal("intake não deveria estar aprovado")
	}
	questions, err := os.ReadFile(filepath.Join(cfg.RunsDir(), res.RunID, "questions.md"))
	if err != nil {
		t.Fatalf("questions.md ausente: %v", err)
	}
	if !strings.Contains(string(questions), "pergunta") && !strings.Contains(string(questions), "critérios") {
		t.Fatalf("questions.md inesperado: %s", questions)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "001.md")); err != nil {
		t.Fatalf("tarefa não deveria ter sido movida: %v", err)
	}
}

func TestIntakeRiskyLabelEscalatesToHuman(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: auto\n    plan: auto\n")
	task.Labels = append(task.Labels, "risk:high")
	p := New(cfg)
	p.Telemetry = telemetry.NewRecorder()

	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{})
	if !errors.Is(err, ErrPaused) {
		t.Fatalf("esperava ErrPaused, obtive %v", err)
	}
	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "paused" || run.Gates["intake"] != "escalated" {
		t.Fatalf("estado inesperado: status=%q gate=%q", run.Status, run.Gates["intake"])
	}

	run.Gates["intake"] = "approved"
	if err := SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	p2 := New(cfg)
	p2.Telemetry = telemetry.NewRecorder()
	resumed, err := p2.Resume(context.Background(), res.RunID, Options{AutoApprove: true})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Status != "succeeded" {
		t.Fatalf("status após resume = %q", resumed.Status)
	}
}

func TestRunDrainPausesAtStageBoundary(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: auto\n    plan: auto\n")
	drain := make(chan struct{})
	close(drain)
	p := New(cfg)
	p.Telemetry = telemetry.NewRecorder()
	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{Drain: drain})
	if !errors.Is(err, ErrPauseRequested) {
		t.Fatalf("esperava ErrPauseRequested, obtive %v", err)
	}
	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "paused" {
		t.Fatalf("status = %q", run.Status)
	}
	for _, stage := range run.Stages {
		if stage.Status != "pending" {
			t.Fatalf("nenhum estágio deveria iniciar: %+v", run.Stages)
		}
	}
}

func TestValidatorEscalatesAfterMaxRounds(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: auto\n    plan: auto\n")
	task.Title += " validator-rejects"
	cfg.Defaults.MaxGateRounds = 2
	p := New(cfg)
	p.Telemetry = telemetry.NewRecorder()

	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{})
	if !errors.Is(err, ErrPaused) {
		t.Fatalf("esperava ErrPaused, obtive %v", err)
	}
	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "paused" || run.Gates["spec"] != "escalated" {
		t.Fatalf("estado inesperado: status=%q gate=%q", run.Status, run.Gates["spec"])
	}
	review, err := os.ReadFile(filepath.Join(cfg.RunsDir(), res.RunID, "spec-review.md"))
	if err != nil {
		t.Fatalf("spec-review.md ausente: %v", err)
	}
	if !strings.Contains(strings.ToUpper(string(review)), "DECISION: REJECTED") {
		t.Fatalf("review inesperado: %s", review)
	}

	run.Gates["spec"] = "approved"
	if err := SaveRun(cfg, run); err != nil {
		t.Fatal(err)
	}
	p2 := New(cfg)
	p2.Telemetry = telemetry.NewRecorder()
	resumed, err := p2.Resume(context.Background(), res.RunID, Options{AutoApprove: true})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Status != "succeeded" {
		t.Fatalf("status após resume = %q", resumed.Status)
	}
}

func TestIntakeRiskyLabelWithAutoApprove(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: auto\n    plan: auto\n")
	task.Labels = append(task.Labels, "risk:high")
	p := New(cfg)
	p.Telemetry = telemetry.NewRecorder()

	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{AutoApprove: true})
	if err != nil {
		t.Fatalf("Run com --auto: %v", err)
	}
	if res.Status != "succeeded" {
		t.Fatalf("status = %q", res.Status)
	}
}

func TestPRBodyClosesGitHubIssueOnMerge(t *testing.T) {
	cfg := &config.Config{Sources: map[string]config.SourceConfig{
		"github": {Driver: "github"},
		"folder": {Driver: "folder"},
	}}
	p := &Pipeline{Cfg: cfg}
	githubRun := &RunState{
		ID:     "run-1",
		Source: "github",
		Task:   source.Task{Source: "github", ExternalID: "6", Title: "Issue 6"},
	}
	body := p.prBody(githubRun, "# Spec\n")
	if !strings.Contains(body, "Closes #6") {
		t.Fatalf("PR body sem closing keyword:\n%s", body)
	}
	folderRun := &RunState{
		ID:     "run-2",
		Source: "folder",
		Task:   source.Task{Source: "folder", ExternalID: "6", Title: "Folder 6"},
	}
	if body := p.prBody(folderRun, "# Spec\n"); strings.Contains(body, "Closes #") {
		t.Fatalf("fonte folder não deveria fechar issue:\n%s", body)
	}
}

func TestParseIntakeJoinsWrappedItemsAndSeparatesAssumptions(t *testing.T) {
	text := `# Intake

## Decisões assumidas

- Timeout fixo de 2000 ms; configuração
  fica para depois.
- Usar SQLite local.

## Perguntas bloqueantes

Nenhuma pergunta bloqueante.

## Critérios de aceite

- O painel mostra o diagnóstico
  mesmo com o worker morto.

DECISION: READY
RISK: LOW
`
	verdict := parseIntake(text)
	if verdict.Decision != "READY" || verdict.Risk != "LOW" {
		t.Fatalf("verdict = %+v", verdict)
	}
	if len(verdict.Assumptions) != 2 || !strings.Contains(verdict.Assumptions[0], "configuração fica") {
		t.Fatalf("assumptions = %#v", verdict.Assumptions)
	}
	if len(verdict.Questions) != 0 {
		t.Fatalf("questions = %#v", verdict.Questions)
	}
	if len(verdict.Acceptance) != 1 || !strings.Contains(verdict.Acceptance[0], "worker morto") {
		t.Fatalf("acceptance = %#v", verdict.Acceptance)
	}
}

func TestIntakeReadyDoesNotBlockOnNonBlockingQuestions(t *testing.T) {
	cfg := &config.Config{Intake: config.IntakeConfig{MinDescriptionChars: 10}}
	p := &Pipeline{Cfg: cfg}
	run := &RunState{Task: source.Task{
		Description: "Descrição suficientemente longa para o intake.",
		Acceptance:  []string{"Critério verificável."},
	}}
	verdict := intakeVerdict{
		Decision:  "READY",
		Questions: []string{"Qual timeout usar?", "Como detectar SW antigo?"},
	}
	if reasons := p.intakeBlockingReasons(run, verdict); len(reasons) != 0 {
		t.Fatalf("READY com perguntas não bloqueantes parou: %v", reasons)
	}
	verdict.Decision = "NEEDS_INPUT"
	reasons := p.intakeBlockingReasons(run, verdict)
	if len(reasons) == 0 {
		t.Fatal("NEEDS_INPUT deveria bloquear")
	}
}

func TestEvaluatePRStatus(t *testing.T) {
	yes := true
	cfg := &config.Config{Delivery: config.DeliveryConfig{
		RequireCI:      &yes,
		RequiredChecks: []string{"verify", "security"},
		ExternalReview: "ignore",
	}}
	p := &Pipeline{Cfg: cfg}
	status := prStatus{
		State:     "OPEN",
		Mergeable: "MERGEABLE",
		StatusChecks: []prCheck{
			{Name: "verify", Status: "COMPLETED", Conclusion: "SUCCESS"},
			{Name: "security", Status: "COMPLETED", Conclusion: "SUCCESS"},
		},
	}
	if ready, pending, reason := p.evaluatePRStatus(status); !ready || pending || reason != "" {
		t.Fatalf("status pronto inesperado: ready=%v pending=%v reason=%q", ready, pending, reason)
	}
	status.StatusChecks[0].Conclusion = ""
	status.StatusChecks[0].Status = "IN_PROGRESS"
	if ready, pending, reason := p.evaluatePRStatus(status); ready || !pending || reason != "" {
		t.Fatalf("status pendente inesperado: ready=%v pending=%v reason=%q", ready, pending, reason)
	}
	status.StatusChecks = []prCheck{{Name: "verify", Status: "COMPLETED", Conclusion: "FAILURE"}}
	if _, _, reason := p.evaluatePRStatus(status); !strings.Contains(reason, "falhou") {
		t.Fatalf("falha de check não detectada: %q", reason)
	}
	status.StatusChecks = nil
	status.Mergeable = "CONFLICTING"
	if _, _, reason := p.evaluatePRStatus(status); !strings.Contains(reason, "conflito") {
		t.Fatalf("conflito não detectado: %q", reason)
	}
}

func TestParseExternalVerdict(t *testing.T) {
	verdict := parseExternalVerdict("DECISION: FIXED\nREPLY: corrigido em a.go\nDETAILS:\n- ajuste\n")
	if verdict.Decision != "FIXED" || verdict.Reply != "corrigido em a.go" || !strings.Contains(verdict.Details, "ajuste") {
		t.Fatalf("verdict = %+v", verdict)
	}
	if got := parseExternalVerdict("DECISION: no-action\n").Decision; got != "NO_ACTION" {
		t.Fatalf("normalização = %q", got)
	}
	if got := parseExternalVerdict("sem formato").Decision; got != "NEEDS_CLARIFICATION" {
		t.Fatalf("fallback = %q", got)
	}
}

func TestEvaluateExternalReview(t *testing.T) {
	yes := true
	base := config.DeliveryConfig{
		RequireCI:      &yes,
		RequiredChecks: []string{"verify"},
	}
	status := prStatus{
		State:        "OPEN",
		Mergeable:    "MERGEABLE",
		StatusChecks: []prCheck{{Name: "verify", Status: "COMPLETED", Conclusion: "SUCCESS"}},
	}
	for _, tc := range []struct {
		mode        string
		decision    string
		wantReady   bool
		wantPending bool
	}{
		{"consume", "", true, false},
		{"consume", "CHANGES_REQUESTED", false, true},
		{"consume", "REVIEW_REQUIRED", false, true},
		{"consume", "APPROVED", true, false},
		{"require", "", false, true},
		{"require", "APPROVED", true, false},
		{"ignore", "CHANGES_REQUESTED", true, false},
	} {
		cfg := base
		cfg.ExternalReview = tc.mode
		cfg.ExternalReply = "auto_unless_conflict"
		p := &Pipeline{Cfg: &config.Config{Delivery: cfg}}
		gotStatus := status
		gotStatus.ReviewDecision = tc.decision
		ready, pending, reason := p.evaluatePRStatus(gotStatus)
		if ready != tc.wantReady || pending != tc.wantPending || reason != "" {
			t.Fatalf("%s/%s: ready=%v pending=%v reason=%q", tc.mode, tc.decision, ready, pending, reason)
		}
	}
}

func TestPendingExternalFeedbackDeduplicates(t *testing.T) {
	p := &Pipeline{Cfg: &config.Config{}}
	run := &RunState{ExternalReview: &ExternalReviewState{Processed: []string{"a"}}}
	feedback := []source.PullRequestFeedback{
		{ID: "a", Body: "já tratado"},
		{ID: "b", Body: "novo"},
		{ID: "c", Body: "  "},
	}
	pending := p.pendingExternalFeedback(run, feedback)
	if len(pending) != 1 || pending[0].ID != "b" {
		t.Fatalf("pending = %+v", pending)
	}
}

type fakeReviewer struct {
	replies []string
}

func (f *fakeReviewer) ReadPullRequestReview(ctx context.Context, pr source.PullRequest) (*source.PullRequestReview, error) {
	return &source.PullRequestReview{}, nil
}

func (f *fakeReviewer) ReplyPullRequestFeedback(ctx context.Context, pr source.PullRequest, feedback source.PullRequestFeedback, body string) error {
	f.replies = append(f.replies, body)
	return nil
}

func TestExternalReviewNoAction(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: false\n    plan: false\n")
	p := New(cfg)
	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := &fakeReviewer{}
	trace := p.Telemetry.Start(context.Background(), telemetry.TraceInfo{ID: run.ID, Name: "test.external"})
	defer trace.End(nil, nil)
	wt := &worktree.Worktree{Repo: run.Repo, Path: run.Worktree, Branch: run.Branch, BaseCommit: run.BaseCommit}
	outcome, err := p.handleExternalReview(context.Background(), run, trace, wt, reviewer, source.PullRequest{URL: "https://github.com/o/r/pull/1"}, []source.PullRequestFeedback{{ID: "thread-comment:1", ThreadID: "thread", Kind: "review_thread", Body: "já atendido"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Handled || outcome.Fixed {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(reviewer.replies) != 1 || len(run.ExternalReview.Processed) != 1 || run.ExternalReview.Processed[0] != "thread-comment:1" {
		t.Fatalf("reviewer=%+v state=%+v", reviewer, run.ExternalReview)
	}
}

func TestExternalReviewConflictPauses(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: false\n    plan: false\n")
	p := New(cfg)
	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := &fakeReviewer{}
	trace := p.Telemetry.Start(context.Background(), telemetry.TraceInfo{ID: run.ID, Name: "test.external"})
	defer trace.End(nil, nil)
	wt := &worktree.Worktree{Repo: run.Repo, Path: run.Worktree, Branch: run.Branch, BaseCommit: run.BaseCommit}
	_, err = p.handleExternalReview(context.Background(), run, trace, wt, reviewer, source.PullRequest{URL: "https://github.com/o/r/pull/1"}, []source.PullRequestFeedback{{ID: "review:1", Kind: "review", Body: "force-conflict: mude a spec"}}, 0)
	if !errors.Is(err, ErrDeliveryPending) {
		t.Fatalf("esperava ErrDeliveryPending, obtive %v", err)
	}
	if len(reviewer.replies) != 0 {
		t.Fatalf("não deveria responder conflito: %+v", reviewer.replies)
	}
	if run.ExternalReview.ConflictPath == "" || len(run.ExternalReview.Processed) != 1 {
		t.Fatalf("estado = %+v", run.ExternalReview)
	}
}

func TestConflictDetectionAndVerdict(t *testing.T) {
	if !prHasConflict(prStatus{Mergeable: "CONFLICTING"}) || !prHasConflict(prStatus{MergeStateStatus: "DIRTY"}) {
		t.Fatal("conflito não detectado")
	}
	if prHasConflict(prStatus{Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN"}) {
		t.Fatal("status limpo detectado como conflito")
	}
	verdict := parseConflictVerdict("DECISION: RESOLVED\nDETAILS:\npreservei os dois lados")
	if verdict.Decision != "RESOLVED" || !strings.Contains(verdict.Details, "preservei") {
		t.Fatalf("verdict = %+v", verdict)
	}
	if got := parseConflictVerdict("DECISION: NEEDS_CLARIFICATION\nDETAILS: ambíguo").Decision; got != "NEEDS_CLARIFICATION" {
		t.Fatalf("decisão = %q", got)
	}
	if got := parseConflictVerdict("sem formato").Decision; got != "CONFLICT" {
		t.Fatalf("fallback = %q", got)
	}
}

func TestRequirementsBlocksUnresolvedVisualReference(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: false\n    plan: false\n")
	task.Description += "\n![Wireframe](missing-wireframe.png)\n"
	p := New(cfg)
	p.Telemetry = telemetry.NewRecorder()
	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{AutoApprove: true})
	if !errors.Is(err, ErrNeedsInput) {
		t.Fatalf("esperava ErrNeedsInput, obtive %v", err)
	}
	if res == nil || res.Status != "needs_input" {
		t.Fatalf("resultado = %+v", res)
	}
	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "needs_input" || run.Stage != "requirements" {
		t.Fatalf("run = %s/%s", run.Status, run.Stage)
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, ".aiw-run", "spec.md")); !os.IsNotExist(err) {
		t.Fatalf("spec não deveria existir após gate visual: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, ".aiw-run", "requirements-blockers.md")); err != nil {
		t.Fatalf("blockers ausente: %v", err)
	}
}

func TestRequirementsFetchesLocalVisualReference(t *testing.T) {
	root, cfg, ref, task := setupProject(t, "    spec: false\n    plan: false\n")
	if err := os.WriteFile(filepath.Join(root, "tasks", "wireframe.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	task.Description += "\n![Wireframe](wireframe.png)\n"
	p := New(cfg)
	p.Telemetry = telemetry.NewRecorder()
	res, err := p.Run(context.Background(), task, ref, filepath.Join(root, "demo"), Options{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "succeeded" {
		t.Fatalf("status = %s", res.Status)
	}
	run, err := LoadRun(cfg, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Task.Attachments) != 1 {
		t.Fatalf("attachments = %+v", run.Task.Attachments)
	}
	if _, err := os.Stat(run.Task.Attachments[0]); err != nil {
		t.Fatal(err)
	}
}
