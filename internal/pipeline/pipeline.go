package pipeline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/engine"
	"github.com/donwellus/aiw/internal/pause"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/sources"
	"github.com/donwellus/aiw/internal/telemetry"
	"github.com/donwellus/aiw/internal/worktree"
	"github.com/donwellus/aiw/prompts"
)

var (
	ErrPaused         = errors.New("run pausada aguardando aprovação")
	ErrNeedsInput     = errors.New("tarefa precisa de input antes de continuar")
	ErrPauseRequested = errors.New("pausa solicitada")
)

const PauseMarker = "PAUSE"
const CancelMarker = "CANCEL"

type Options struct {
	RunID            string
	AutoApprove      bool
	EngineOverride   string
	BudgetUSD        float64
	CreatePR         bool
	DisableWriteBack bool
}

type Result struct {
	RunID    string
	Status   string
	Stage    string
	RunDir   string
	Worktree string
	Branch   string
	PRURL    string
	Summary  string
	Usage    engine.Usage
}

type Pipeline struct {
	Cfg       *config.Config
	Sources   *sources.Registry
	Engines   *engine.Registry
	Telemetry telemetry.Client
	Log       *slog.Logger
	Limiter   *engine.Limiter
}

func New(cfg *config.Config) *Pipeline {
	return &Pipeline{
		Cfg:       cfg,
		Sources:   sources.NewRegistry(cfg),
		Engines:   engine.NewRegistry(cfg),
		Telemetry: telemetry.New(cfg.Telemetry),
		Log:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
		Limiter:   engine.NewLimiter(),
	}
}

func (p *Pipeline) WithLogger(logger *slog.Logger) *Pipeline {
	clone := *p
	clone.Log = logger
	return &clone
}

func (p *Pipeline) NewRunID(task *source.Task) string {
	return p.newRunID(task)
}

func (p *Pipeline) Run(ctx context.Context, task *source.Task, ref source.Ref, repo string, opts Options) (*Result, error) {
	if task == nil {
		return nil, errors.New("tarefa vazia")
	}
	if repo == "" {
		return nil, errors.New("repositório não informado")
	}
	repoAbs, err := filepath.Abs(repo)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(repoAbs); err != nil {
		return nil, fmt.Errorf("repositório %s inacessível: %w", repoAbs, err)
	}

	runID := opts.RunID
	if runID == "" {
		runID = p.newRunID(task)
	}
	run := newRunState(runID, task, ref, task.Source, repoAbs)
	run.EngineOverride = opts.EngineOverride
	dir := p.runDir(run)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(dir, "task.json"), task); err != nil {
		return nil, err
	}

	wt, err := worktree.Create(ctx, repoAbs, p.Cfg.DataDir(), runID)
	if err != nil {
		run.Status = "failed"
		run.Error = err.Error()
		if err := saveRun(run, dir); err != nil {
			p.Log.Warn("falha ao salvar run.json", "error", err)
		}
		appendEvent(dir, "run.failed", map[string]any{"error": err.Error()})
		return &Result{RunID: run.ID, Status: "failed", RunDir: dir, Summary: "falha ao criar worktree"}, err
	}
	run.Worktree = wt.Path
	run.Branch = wt.Branch
	run.BaseCommit = wt.BaseCommit
	if err := saveRun(run, dir); err != nil {
		return nil, err
	}
	appendEvent(dir, "run.start", map[string]any{"task": run.Task.ExternalID, "repo": run.Repo, "branch": run.Branch, "engine": opts.EngineOverride})
	return p.runPipeline(ctx, run, opts)
}

func (p *Pipeline) Resume(ctx context.Context, runID string, opts Options) (*Result, error) {
	dir := filepath.Join(p.Cfg.RunsDir(), runID)
	run, err := loadRun(filepath.Join(dir, "run.json"))
	if err != nil {
		return nil, fmt.Errorf("carregando run %s: %w", runID, err)
	}
	if run.Status == "needs_input" {
		return nil, fmt.Errorf("run %s aguarda input; edite a tarefa na fonte e execute novamente", runID)
	}
	_ = os.Remove(filepath.Join(dir, PauseMarker))
	_ = os.Remove(filepath.Join(dir, CancelMarker))
	if _, err := os.Stat(run.Worktree); err != nil {
		return nil, fmt.Errorf("worktree %s não encontrada: %w", run.Worktree, err)
	}
	if opts.EngineOverride == "" {
		opts.EngineOverride = run.EngineOverride
	}
	run.Status = "running"
	run.Error = ""
	if err := saveRun(run, dir); err != nil {
		return nil, err
	}
	appendEvent(dir, "run.resume", map[string]any{"stage": run.Stage})
	return p.runPipeline(ctx, run, opts)
}

func (p *Pipeline) runPipeline(ctx context.Context, run *RunState, opts Options) (*Result, error) {
	dir := p.runDir(run)
	budget := opts.BudgetUSD
	if budget <= 0 {
		budget = p.Cfg.Defaults.BudgetUSD
	}
	trace := p.Telemetry.Start(ctx, telemetry.TraceInfo{
		ID:   run.ID,
		Name: "aiw.run",
		Input: map[string]any{
			"task": run.Task,
			"repo": run.Repo,
		},
		Metadata: map[string]any{
			"source":     run.Source,
			"branch":     run.Branch,
			"repo":       run.Repo,
			"task_id":    run.Task.ExternalID,
			"budget_usd": budget,
		},
		Tags:      []string{"aiw", run.Source, p.Cfg.Defaults.Engine},
		SessionID: run.ID,
	})

	var runErr error
	defer func() {
		trace.Score("success", boolScore(run.Status == "succeeded"), run.Status)
		trace.Score("cost_usd", run.Usage.CostUSD, "")
		trace.End(map[string]any{
			"status":        run.Status,
			"run_id":        run.ID,
			"branch":        run.Branch,
			"pr_url":        run.PRURL,
			"cost_usd":      run.Usage.CostUSD,
			"input_tokens":  run.Usage.InputTokens,
			"output_tokens": run.Usage.OutputTokens,
		}, runErr)
		if err := p.Telemetry.Flush(context.Background()); err != nil {
			p.Log.Warn("falha ao enviar telemetria", "error", err)
		}
	}()

	runErr = p.execute(ctx, run, trace, opts, budget)
	if runErr != nil {
		if errors.Is(runErr, ErrPauseRequested) {
			run.Status = "paused"
			run.Error = ""
			p.save(run, dir)
			appendEvent(dir, "run.pause_requested", map[string]any{"stage": run.Stage})
			return p.result(run, "pausada"), runErr
		}
		if errors.Is(runErr, ErrPaused) {
			run.Status = "paused"
			if err := saveRun(run, dir); err != nil {
				p.Log.Warn("falha ao salvar run.json", "error", err)
			}
			appendEvent(dir, "run.paused", map[string]any{"stage": run.Stage})
			res := p.result(run, "pausada")
			return res, runErr
		}
		if errors.Is(runErr, ErrNeedsInput) {
			run.Status = "needs_input"
			run.Error = runErr.Error()
			p.save(run, dir)
			appendEvent(dir, "run.needs_input", map[string]any{"stage": run.Stage, "error": runErr.Error()})
			return p.result(run, "aguardando input"), runErr
		}
		if errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			run.Status = "canceled"
			run.Error = runErr.Error()
			p.save(run, dir)
			appendEvent(dir, "run.canceled", map[string]any{"stage": run.Stage})
			return p.result(run, "cancelada"), runErr
		}
		run.Status = "failed"
		run.Error = runErr.Error()
		if err := saveRun(run, dir); err != nil {
			p.Log.Warn("falha ao salvar run.json", "error", err)
		}
		appendEvent(dir, "run.failed", map[string]any{"error": runErr.Error()})
		p.writeBack(ctx, run, runErr, opts)
		return p.result(run, "falhou"), runErr
	}

	run.Status = "succeeded"
	run.Stage = "done"
	p.save(run, dir)
	appendEvent(dir, "run.succeeded", map[string]any{"branch": run.Branch, "pr_url": run.PRURL})
	p.writeBack(ctx, run, nil, opts)
	return p.result(run, "concluída"), nil
}

func (p *Pipeline) execute(ctx context.Context, run *RunState, trace telemetry.Trace, opts Options, budget float64) error {
	dir := p.runDir(run)
	for i := range run.Stages {
		st := &run.Stages[i]
		if st.Status == "done" {
			continue
		}
		run.Stage = st.Name
		if st.Status == "gated" {
			gate := st.Name
			switch st.Name {
			case "requirements":
				gate = "spec"
			case "architect":
				gate = "plan"
			}
			if err := p.gate(ctx, run, gate, trace, opts.AutoApprove); err != nil {
				st.Status = "gated"
				p.save(run, dir)
				return err
			}
			st.Status = "done"
			st.EndedAt = time.Now().UTC()
			p.save(run, dir)
			continue
		}
		if p.cancelRequested(dir) {
			return context.Canceled
		}
		if p.pauseRequested(dir) {
			return ErrPauseRequested
		}
		st.StartedAt = time.Now().UTC()
		st.Status = "running"
		run.Stage = st.Name
		appendEvent(dir, "stage.start", map[string]any{"stage": st.Name})
		p.save(run, dir)

		var err error
		switch st.Name {
		case "intake":
			err = p.runIntakeStage(ctx, run, trace, budget, opts)
		case "requirements":
			err = p.runValidatedStage(ctx, run, trace, "requirements", "spec.md", "spec-validator", "spec", budget, opts)
		case "architect":
			err = p.runValidatedStage(ctx, run, trace, "architect", "plan.md", "plan-validator", "plan", budget, opts)
		case "implement":
			err = p.implement(ctx, run, trace, "", budget)
		case "test":
			var ok bool
			ok, err = p.ensureTests(ctx, run, trace, st, budget)
			if err == nil && !ok {
				err = errors.New("testes falharam após as tentativas de correção")
			}
		case "review":
			err = p.reviewLoop(ctx, run, trace, st, budget)
		case "deliver":
			err = p.deliver(ctx, run, trace, opts)
		default:
			err = fmt.Errorf("estágio desconhecido: %s", st.Name)
		}

		if err != nil {
			st.EndedAt = time.Now().UTC()
			if errors.Is(err, ErrPaused) {
				st.Status = "gated"
				p.save(run, dir)
				return err
			}
			if errors.Is(err, ErrNeedsInput) {
				st.Status = "needs_input"
				st.Error = err.Error()
				p.save(run, dir)
				appendEvent(dir, "stage.needs_input", map[string]any{"stage": st.Name, "error": err.Error()})
				return err
			}
			st.Status = "failed"
			st.Error = err.Error()
			p.save(run, dir)
			appendEvent(dir, "stage.failed", map[string]any{"stage": st.Name, "error": err.Error()})
			return err
		}
		st.Status = "done"
		st.Error = ""
		st.EndedAt = time.Now().UTC()
		p.save(run, dir)
		appendEvent(dir, "stage.done", map[string]any{"stage": st.Name, "cost_usd": st.CostUSD})
	}
	return nil
}

func (p *Pipeline) runArtifactStage(ctx context.Context, run *RunState, trace telemetry.Trace, stage, artifact string, budget float64, feedback string) error {
	data := p.promptData(run)
	data.Feedback = feedback
	prompt, err := p.render(stage, data)
	if err != nil {
		return err
	}
	engineName, model := p.engineFor(run, stage)
	res, err := p.callEngine(ctx, run, trace, stage, engineName, model, prompt, budget)
	if err != nil {
		return err
	}
	p.writeWork(run, stage+".output.md", res.Output+"\n")
	artifactPath := p.workArtifact(run, artifact)
	if _, statErr := os.Stat(artifactPath); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if strings.TrimSpace(res.Output) == "" {
			return fmt.Errorf("engine %s não gerou %s e não retornou saída", engineName, artifact)
		}
		p.writeWork(run, artifact, res.Output+"\n")
	}
	p.mirror(run)
	return nil
}

type intakeVerdict struct {
	Decision    string
	Risk        string
	Questions   []string
	Assumptions []string
	Acceptance  []string
}

func parseIntake(text string) intakeVerdict {
	verdict := intakeVerdict{}
	section := ""
	currentSection := ""
	currentIndex := -1
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "DECISION:") {
			verdict.Decision = cleanDecision(strings.TrimSpace(line[len("DECISION:"):]))
			currentSection = ""
			currentIndex = -1
			continue
		}
		if strings.HasPrefix(upper, "RISK:") {
			verdict.Risk = cleanDecision(strings.TrimSpace(line[len("RISK:"):]))
			currentSection = ""
			currentIndex = -1
			continue
		}
		if strings.HasPrefix(line, "##") {
			heading := strings.ToLower(strings.TrimSpace(strings.TrimLeft(line, "#")))
			switch {
			case strings.Contains(heading, "perguntas"):
				section = "questions"
			case strings.Contains(heading, "decis") && strings.Contains(heading, "assum"):
				section = "assumptions"
			case strings.Contains(heading, "crit") && strings.Contains(heading, "rio"):
				section = "acceptance"
			default:
				section = ""
			}
			currentSection = ""
			currentIndex = -1
			continue
		}
		if section == "" {
			continue
		}
		item := ""
		switch {
		case strings.HasPrefix(line, "- "):
			item = strings.TrimSpace(line[2:])
		case strings.HasPrefix(line, "* "):
			item = strings.TrimSpace(line[2:])
		}
		if item != "" {
			if isPlaceholderItem(item) {
				currentSection = ""
				currentIndex = -1
				continue
			}
			switch section {
			case "questions":
				verdict.Questions = append(verdict.Questions, item)
				currentIndex = len(verdict.Questions) - 1
			case "assumptions":
				verdict.Assumptions = append(verdict.Assumptions, item)
				currentIndex = len(verdict.Assumptions) - 1
			case "acceptance":
				verdict.Acceptance = append(verdict.Acceptance, item)
				currentIndex = len(verdict.Acceptance) - 1
			}
			currentSection = section
			continue
		}
		if line == "" || currentIndex < 0 || currentSection != section {
			continue
		}
		switch currentSection {
		case "questions":
			verdict.Questions[currentIndex] += " " + line
		case "assumptions":
			verdict.Assumptions[currentIndex] += " " + line
		case "acceptance":
			verdict.Acceptance[currentIndex] += " " + line
		}
	}
	for i := range verdict.Questions {
		verdict.Questions[i] = strings.Join(strings.Fields(verdict.Questions[i]), " ")
	}
	for i := range verdict.Assumptions {
		verdict.Assumptions[i] = strings.Join(strings.Fields(verdict.Assumptions[i]), " ")
	}
	for i := range verdict.Acceptance {
		verdict.Acceptance[i] = strings.Join(strings.Fields(verdict.Acceptance[i]), " ")
	}
	return verdict
}

func cleanDecision(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`*_ ")
	return strings.ToUpper(value)
}

func isPlaceholderItem(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return lower == "" || lower == "-" || lower == "n/a" || strings.HasPrefix(lower, "nenhuma") || strings.HasPrefix(lower, "(nenhuma")
}

func (p *Pipeline) riskyLabels(labels []string) []string {
	var risky []string
	for _, label := range labels {
		for _, configured := range p.Cfg.Intake.RiskLabels {
			if strings.EqualFold(strings.TrimSpace(label), strings.TrimSpace(configured)) {
				risky = append(risky, label)
				break
			}
		}
	}
	return risky
}

func (p *Pipeline) writeQuestions(run *RunState, questions, reasons []string) {
	var builder strings.Builder
	builder.WriteString("# Perguntas e bloqueios do intake\n\n")
	seen := map[string]bool{}
	for _, item := range append(append([]string{}, questions...), reasons...) {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		fmt.Fprintf(&builder, "- %s\n", item)
	}
	p.writeWork(run, "questions.md", builder.String())
}

func (p *Pipeline) intakeBlockingReasons(run *RunState, verdict intakeVerdict) []string {
	var reasons []string
	description := strings.TrimSpace(run.Task.Description)
	descriptionLength := len([]rune(description))
	if descriptionLength == 0 {
		reasons = append(reasons, "descrição vazia")
	} else if descriptionLength < p.Cfg.Intake.MinDescriptionChars {
		reasons = append(reasons, fmt.Sprintf("descrição curta (%d < %d caracteres)", descriptionLength, p.Cfg.Intake.MinDescriptionChars))
	}
	sourceAcceptance := 0
	for _, criterion := range run.Task.Acceptance {
		if strings.TrimSpace(criterion) != "" {
			sourceAcceptance++
		}
	}
	if sourceAcceptance == 0 && len(verdict.Acceptance) == 0 {
		reasons = append(reasons, "sem critérios de aceite")
	}
	if verdict.Decision != "READY" {
		if verdict.Decision == "" {
			reasons = append(reasons, "intake não retornou DECISION")
		} else {
			reasons = append(reasons, "intake retornou DECISION: "+verdict.Decision)
		}
		if len(verdict.Questions) > 0 {
			reasons = append(reasons, fmt.Sprintf("%d pergunta(s) bloqueante(s) no intake", len(verdict.Questions)))
		}
	}
	return reasons
}

func (p *Pipeline) writeAssumptions(run *RunState, items []string) {
	filtered := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		return
	}
	var builder strings.Builder
	builder.WriteString("# Decisões assumidas e perguntas não bloqueantes\n\n")
	for _, item := range filtered {
		fmt.Fprintf(&builder, "- %s\n", item)
	}
	p.writeWork(run, "assumptions.md", builder.String())
}

func (p *Pipeline) runIntakeStage(ctx context.Context, run *RunState, trace telemetry.Trace, budget float64, opts Options) error {
	if err := p.runArtifactStage(ctx, run, trace, "intake", "intake.md", budget, ""); err != nil {
		return err
	}
	verdict := parseIntake(p.readWork(run, "intake.md"))
	reasons := p.intakeBlockingReasons(run, verdict)
	if len(reasons) > 0 {
		blocking := verdict.Questions
		if verdict.Decision == "READY" {
			blocking = nil
		}
		p.writeQuestions(run, blocking, reasons)
		p.writeAssumptions(run, append(append([]string{}, verdict.Assumptions...), verdict.Questions...))
		p.mirror(run)
		return fmt.Errorf("%w: %s", ErrNeedsInput, strings.Join(reasons, "; "))
	}
	p.writeAssumptions(run, append(append([]string{}, verdict.Assumptions...), verdict.Questions...))

	risky := p.riskyLabels(run.Task.Labels)
	riskHigh := strings.EqualFold(verdict.Risk, "HIGH") || len(risky) > 0
	mode := p.Cfg.Workspace.GateModeFor("intake")
	if mode == config.GateHuman || (mode == config.GatePolicy && riskHigh) {
		if opts.AutoApprove {
			run.Gates["intake"] = "approved"
			p.save(run, p.runDir(run))
			appendEvent(p.runDir(run), "gate.approved", map[string]any{"gate": "intake", "mode": "auto"})
			trace.Event("gate.approved", map[string]any{"gate": "intake", "mode": "auto"})
			return nil
		}
		run.Gates["intake"] = "escalated"
		reason := "intake requer aprovação humana"
		if riskHigh {
			detail := "risco alto"
			if verdict.Risk != "" {
				detail = "risco " + verdict.Risk
			}
			if len(risky) > 0 {
				detail += ", labels: " + strings.Join(risky, ", ")
			}
			reason += " (" + detail + ")"
		}
		appendEvent(p.runDir(run), "gate.escalated", map[string]any{"gate": "intake", "reason": reason})
		trace.Event("gate.escalated", map[string]any{"gate": "intake", "reason": reason})
		return fmt.Errorf("%w: %s", ErrPaused, reason)
	}

	run.Gates["intake"] = "approved"
	p.save(run, p.runDir(run))
	appendEvent(p.runDir(run), "gate.approved", map[string]any{"gate": "intake", "mode": string(mode)})
	trace.Event("gate.approved", map[string]any{"gate": "intake", "mode": string(mode)})
	return nil
}

func (p *Pipeline) runValidatedStage(ctx context.Context, run *RunState, trace telemetry.Trace, stage, artifact, validator, gateName string, budget float64, opts Options) error {
	attempts := p.Cfg.Defaults.MaxGateRounds
	if attempts < 1 {
		attempts = 1
	}
	feedback := ""
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			_ = os.Remove(p.workArtifact(run, artifact))
		}
		if err := p.runArtifactStage(ctx, run, trace, stage, artifact, budget, feedback); err != nil {
			return err
		}
		approved, blockers, err := p.runValidator(ctx, run, trace, stage, validator, budget)
		if err != nil {
			return err
		}
		if approved {
			return p.gate(ctx, run, gateName, trace, opts.AutoApprove)
		}
		feedback = strings.Join(blockers, "\n")
		if strings.TrimSpace(feedback) == "" {
			feedback = "validador rejeitou o artefato sem lista de bloqueios"
		}
		appendEvent(p.runDir(run), "validation.rejected", map[string]any{
			"stage":    stage,
			"attempt":  attempt,
			"blockers": blockers,
		})
		trace.Event("validation.rejected", map[string]any{"stage": stage, "attempt": attempt})
	}
	run.Gates[gateName] = "escalated"
	p.save(run, p.runDir(run))
	appendEvent(p.runDir(run), "gate.escalated", map[string]any{"gate": gateName, "reason": "validação automática falhou"})
	trace.Event("gate.escalated", map[string]any{"gate": gateName})
	return fmt.Errorf("%w: %s não passou na validação após %d tentativa(s); aprove manualmente", ErrPaused, artifact, attempts)
}

func (p *Pipeline) runValidator(ctx context.Context, run *RunState, trace telemetry.Trace, targetStage, validator string, budget float64) (bool, []string, error) {
	data := p.promptData(run)
	prompt, err := p.render(validator, data)
	if err != nil {
		return false, nil, err
	}
	engineName, model := p.engineFor(run, targetStage)
	res, err := p.callEngineNamed(ctx, run, trace, targetStage, validator, engineName, model, prompt, budget)
	if err != nil {
		return false, nil, err
	}
	p.writeWork(run, validator+".output.md", res.Output+"\n")
	artifact := validatorArtifact(validator)
	artifactPath := p.workArtifact(run, artifact)
	if _, statErr := os.Stat(artifactPath); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return false, nil, statErr
		}
		if strings.TrimSpace(res.Output) == "" {
			return false, nil, fmt.Errorf("%s não gerou %s nem retornou saída", validator, artifact)
		}
		p.writeWork(run, artifact, res.Output+"\n")
	}
	p.mirror(run)
	approved, blockers := parseValidatorDecision(p.readWork(run, artifact))
	return approved, blockers, nil
}

func parseValidatorDecision(text string) (bool, []string) {
	approved := false
	decisionSeen := false
	var blockers []string
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "DECISION:") {
			decisionSeen = true
			value := cleanDecision(strings.TrimSpace(line[len("DECISION:"):]))
			approved = value == "APPROVED"
			continue
		}
		if strings.HasPrefix(upper, "BLOCKING:") {
			blockers = append(blockers, line)
		}
	}
	if !decisionSeen {
		blockers = append(blockers, "validador não retornou DECISION")
	} else if !approved && len(blockers) == 0 {
		blockers = append(blockers, "validador rejeitou sem BLOCKING detalhado")
	}
	return approved && len(blockers) == 0, blockers
}

func validatorArtifact(validator string) string {
	switch validator {
	case "spec-validator":
		return "spec-review.md"
	case "plan-validator":
		return "plan-review.md"
	default:
		return validator + ".md"
	}
}

func (p *Pipeline) implement(ctx context.Context, run *RunState, trace telemetry.Trace, feedback string, budget float64) error {
	data := p.promptData(run)
	data.Feedback = feedback
	prompt, err := p.render("implement", data)
	if err != nil {
		return err
	}
	engineName, model := p.engineFor(run, "implement")
	res, err := p.callEngine(ctx, run, trace, "implement", engineName, model, prompt, budget)
	if err != nil {
		return err
	}
	p.writeWork(run, "implement.output.md", res.Output+"\n")
	artifactPath := p.workArtifact(run, "implement.md")
	if _, statErr := os.Stat(artifactPath); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		p.writeWork(run, "implement.md", res.Output+"\n")
	}
	p.mirror(run)
	appendEvent(p.runDir(run), "implement.done", map[string]any{"feedback": feedback != ""})
	return nil
}

func (p *Pipeline) ensureTests(ctx context.Context, run *RunState, trace telemetry.Trace, st *StageState, budget float64) (bool, error) {
	ok, log, err := p.runTests(ctx, run, trace)
	if err != nil {
		return false, err
	}
	attempts := 0
	for !ok && attempts < p.Cfg.Defaults.MaxTestRetries {
		attempts++
		st.Rounds = attempts
		appendEvent(p.runDir(run), "tests.retry", map[string]any{"attempt": attempts})
		feedback := "Os testes falharam após a implementação. Corrija os problemas:\n\n" + truncateForPrompt(log, 30000)
		if err := p.implement(ctx, run, trace, feedback, budget); err != nil {
			return false, err
		}
		ok, log, err = p.runTests(ctx, run, trace)
		if err != nil {
			return false, err
		}
	}
	return ok, nil
}

func (p *Pipeline) runTests(ctx context.Context, run *RunState, trace telemetry.Trace) (bool, string, error) {
	span := trace.Span("stage.test", map[string]any{"commands": p.Cfg.Workspace.TestCommands})
	var buf bytes.Buffer
	ok := true
	if len(p.Cfg.Workspace.TestCommands) == 0 {
		buf.WriteString("nenhum comando de teste configurado em workspace.test_commands\n")
	} else {
		release, err := p.Limiter.Acquire(ctx, "test:"+filepath.Clean(run.Repo), p.Cfg.Workspace.MaxTestConcurrency)
		if err != nil {
			span.End(nil, err)
			return false, "", err
		}
		defer release()
		for _, command := range p.Cfg.Workspace.TestCommands {
			fmt.Fprintf(&buf, "$ %s\n", command)
			cctx := ctx
			var cancel context.CancelFunc
			if timeout := p.Cfg.Defaults.Timeout.Std(); timeout > 0 {
				cctx, cancel = context.WithTimeout(ctx, timeout)
			}
			cmd := exec.CommandContext(cctx, "/bin/sh", "-c", command)
			cmd.Dir = run.Worktree
			out, err := cmd.CombinedOutput()
			if cancel != nil {
				cancel()
			}
			buf.Write(out)
			if err != nil {
				ok = false
				fmt.Fprintf(&buf, "\n[falhou: %v]\n", err)
			} else {
				buf.WriteString("\n[ok]\n")
			}
			if ctx.Err() != nil {
				span.End(nil, ctx.Err())
				return false, buf.String(), ctx.Err()
			}
		}
	}
	log := buf.String()
	p.writeWork(run, "test.log", log)
	p.mirror(run)
	span.End(map[string]any{"ok": ok}, nil)
	appendEvent(p.runDir(run), "tests.result", map[string]any{"ok": ok})
	return ok, log, nil
}

func (p *Pipeline) reviewLoop(ctx context.Context, run *RunState, trace telemetry.Trace, st *StageState, budget float64) error {
	testLog := p.readWork(run, "test.log")
	if _, err := p.snapshotDiff(run); err != nil {
		return err
	}
	review, err := p.runReview(ctx, run, trace, testLog, 0, budget)
	if err != nil {
		return err
	}
	blocking := parseBlocking(review)
	rounds := 0
	maxRounds := p.Cfg.Defaults.MaxReviewRounds
	for len(blocking) > 0 && rounds < maxRounds {
		rounds++
		st.Rounds = rounds
		appendEvent(p.runDir(run), "review.blocking", map[string]any{"round": rounds, "blocking": blocking})
		if err := p.correct(ctx, run, trace, blocking, rounds, budget); err != nil {
			return err
		}
		ok, log, err := p.runTests(ctx, run, trace)
		if err != nil {
			return err
		}
		if !ok {
			message := "testes falharam após a correção da revisão"
			p.writeWork(run, "review-blockers.md", "# Revisão bloqueada\n\n"+message+"\n\n## Testes\n\n```\n"+truncateForPrompt(log, 20000)+"\n```\n")
			p.mirror(run)
			return fmt.Errorf("%w: %s", ErrPaused, message)
		}
		if _, err := p.snapshotDiff(run); err != nil {
			return err
		}
		review, err = p.runReview(ctx, run, trace, log, rounds, budget)
		if err != nil {
			return err
		}
		blocking = parseBlocking(review)
	}
	if len(blocking) > 0 {
		var builder strings.Builder
		builder.WriteString("# Revisão bloqueada\n\n")
		for _, item := range blocking {
			fmt.Fprintf(&builder, "- %s\n", item)
		}
		builder.WriteString("\nRevise os blockers, ajuste o código ou use um target de refine se a spec/plano precisarem mudar.\n")
		p.writeWork(run, "review-blockers.md", builder.String())
		p.mirror(run)
		return fmt.Errorf("%w: revisão ainda com %d bloqueio(s) após %d rodada(s)", ErrPaused, len(blocking), rounds)
	}
	return nil
}

func (p *Pipeline) correct(ctx context.Context, run *RunState, trace telemetry.Trace, blocking []string, round int, budget float64) error {
	data := p.promptData(run)
	data.Feedback = strings.Join(blocking, "\n")
	data.ReviewRound = round
	data.CorrectionPath = p.workArtifact(run, fmt.Sprintf("correction-round-%d.md", round))
	prompt, err := p.render("correct", data)
	if err != nil {
		return err
	}
	engineName, model := p.engineFor(run, "correct")
	res, err := p.callEngineNamed(ctx, run, trace, "correct", "correction", engineName, model, prompt, budget)
	if err != nil {
		return err
	}
	p.writeWork(run, "correction.output.md", res.Output+"\n")
	artifactPath := p.workArtifact(run, fmt.Sprintf("correction-round-%d.md", round))
	if _, statErr := os.Stat(artifactPath); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if strings.TrimSpace(res.Output) == "" {
			return fmt.Errorf("engine %s não gerou correction-round-%d.md", engineName, round)
		}
		p.writeWork(run, fmt.Sprintf("correction-round-%d.md", round), res.Output+"\n")
	}
	p.mirror(run)
	appendEvent(p.runDir(run), "correction.done", map[string]any{"round": round, "blocking": len(blocking)})
	return nil
}

func (p *Pipeline) runReview(ctx context.Context, run *RunState, trace telemetry.Trace, testLog string, round int, budget float64) (string, error) {
	data := p.promptData(run)
	data.TestLog = truncateForPrompt(testLog, 30000)
	data.Diff = truncateForPrompt(p.readWork(run, "diff.patch"), 60000)
	data.ReviewRound = round
	if round > 0 {
		data.CorrectionPath = p.workArtifact(run, fmt.Sprintf("correction-round-%d.md", round))
	}
	prompt, err := p.render("review", data)
	if err != nil {
		return "", err
	}
	engineName, model := p.engineFor(run, "review")
	res, err := p.callEngine(ctx, run, trace, "review", engineName, model, prompt, budget)
	if err != nil {
		return "", err
	}
	p.writeWork(run, "review.output.md", res.Output+"\n")
	artifactPath := p.workArtifact(run, "review.md")
	if _, statErr := os.Stat(artifactPath); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		if strings.TrimSpace(res.Output) == "" {
			return "", errors.New("revisão vazia: engine não gerou review.md nem retornou saída")
		}
		p.writeWork(run, "review.md", res.Output+"\n")
	}
	review := p.readWork(run, "review.md")
	if strings.TrimSpace(review) == "" {
		return "", errors.New("revisão vazia")
	}
	p.writeWork(run, fmt.Sprintf("review-round-%d.md", round), review+"\n")
	p.mirror(run)
	return review, nil
}

func (p *Pipeline) deliver(ctx context.Context, run *RunState, trace telemetry.Trace, opts Options) error {
	span := trace.Span("stage.deliver", map[string]any{"branch": run.Branch})
	wt := &worktree.Worktree{Repo: run.Repo, Path: run.Worktree, Branch: run.Branch, BaseCommit: run.BaseCommit}

	if _, err := p.snapshotDiff(run); err != nil {
		span.End(nil, err)
		return err
	}
	changed, err := wt.Changed(ctx)
	if err != nil {
		span.End(nil, err)
		return err
	}
	if changed {
		committed, err := wt.Commit(ctx, fmt.Sprintf("aiw: %s (%s)", run.Task.Title, run.ID))
		if err != nil {
			span.End(nil, err)
			return err
		}
		if !committed {
			err := errors.New("commit não criado: nenhuma alteração")
			span.End(nil, err)
			return err
		}
	} else {
		ahead, err := wt.CommitsAhead(ctx)
		if err != nil {
			span.End(nil, err)
			return err
		}
		if ahead == 0 {
			err := errors.New("nenhuma alteração detectada no worktree")
			span.End(nil, err)
			return err
		}
	}

	if opts.CreatePR {
		if err := p.createPR(ctx, run, wt); err != nil {
			span.End(nil, err)
			return err
		}
		if p.autoMerge() {
			if err := p.awaitAndMergePR(ctx, run); err != nil {
				span.End(nil, err)
				return err
			}
		}
	}

	summary := fmt.Sprintf("# Entrega\n\nRun: %s\nBranch: %s\nPR: %s\n\n## Spec\n\n%s\n", run.ID, run.Branch, run.PRURL, truncateForPrompt(p.readWork(run, "spec.md"), 8000))
	p.writeWork(run, "deliver.md", summary)
	p.mirror(run)
	span.End(map[string]any{"pr_url": run.PRURL, "branch": run.Branch}, nil)
	return nil
}

func (p *Pipeline) autoMerge() bool {
	switch strings.ToLower(strings.TrimSpace(p.Cfg.Delivery.Merge)) {
	case "auto", "auto_after_approval":
		return true
	default:
		return false
	}
}

type prCheck struct {
	TypeName   string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	State      string `json:"state"`
	Conclusion string `json:"conclusion"`
}

type prStatus struct {
	State          string    `json:"state"`
	Mergeable      string    `json:"mergeable"`
	ReviewDecision string    `json:"reviewDecision"`
	StatusChecks   []prCheck `json:"statusCheckRollup"`
}

func (p *Pipeline) awaitAndMergePR(ctx context.Context, run *RunState) error {
	poll := p.Cfg.Delivery.CIPollInterval.Std()
	if poll <= 0 {
		poll = 10 * time.Second
	}
	timeout := p.Cfg.Delivery.CITimeout.Std()
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		status, err := p.readPRStatus(ctx, run.PRURL)
		if err != nil {
			return err
		}
		ready, pending, reason := p.evaluatePRStatus(status)
		if reason != "" {
			return fmt.Errorf("%w: entrega bloqueada: %s", ErrPaused, reason)
		}
		if ready {
			break
		}
		if !pending {
			return fmt.Errorf("%w: PR ainda não está pronto para merge", ErrPaused)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: timeout aguardando CI do PR %s", ErrPaused, run.PRURL)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}

	concurrency := p.Cfg.Delivery.MergeConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	release, err := p.Limiter.Acquire(ctx, "merge:"+filepath.Clean(run.Repo), concurrency)
	if err != nil {
		return err
	}
	defer release()

	status, err := p.readPRStatus(ctx, run.PRURL)
	if err != nil {
		return err
	}
	ready, pending, reason := p.evaluatePRStatus(status)
	if reason != "" {
		return fmt.Errorf("%w: entrega bloqueada: %s", ErrPaused, reason)
	}
	if !ready {
		if pending {
			return fmt.Errorf("%w: PR ainda não está pronto para merge", ErrPaused)
		}
		return fmt.Errorf("%w: PR ainda não está pronto para merge", ErrPaused)
	}
	method := strings.ToLower(strings.TrimSpace(p.Cfg.Delivery.Method))
	args := []string{"pr", "merge", run.PRURL}
	switch method {
	case "merge":
		args = append(args, "--merge")
	case "rebase":
		args = append(args, "--rebase")
	default:
		args = append(args, "--squash")
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = run.Worktree
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		merged, _ := p.readPRStatus(ctx, run.PRURL)
		if merged.State != "MERGED" {
			return fmt.Errorf("%w: merge do PR: %v: %s", ErrPaused, err, strings.TrimSpace(stderr.String()))
		}
	}
	run.PRMerged = true
	run.MergedAt = time.Now().UTC()
	p.save(run, p.runDir(run))
	appendEvent(p.runDir(run), "pr.merged", map[string]any{"pr_url": run.PRURL, "method": method})
	p.mirror(run)
	return nil
}

func (p *Pipeline) readPRStatus(ctx context.Context, prURL string) (prStatus, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", prURL, "--json", "state,mergeable,reviewDecision,statusCheckRollup")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return prStatus{}, fmt.Errorf("consultando PR %s: %w: %s", prURL, err, strings.TrimSpace(stderr.String()))
	}
	var status prStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		return prStatus{}, fmt.Errorf("parseando status do PR: %w", err)
	}
	return status, nil
}

func (p *Pipeline) evaluatePRStatus(status prStatus) (ready bool, pending bool, reason string) {
	if status.State == "MERGED" {
		return true, false, ""
	}
	if status.State != "" && status.State != "OPEN" {
		return false, false, "PR está " + status.State
	}
	if status.Mergeable == "CONFLICTING" {
		return false, false, "PR com conflito de merge"
	}
	if strings.EqualFold(p.Cfg.Delivery.ExternalReview, "require") && !strings.EqualFold(status.ReviewDecision, "APPROVED") {
		return false, false, "PR aguardando aprovação externa"
	}
	if !p.Cfg.Delivery.RequireCIEnabled() {
		if status.Mergeable == "MERGEABLE" {
			return true, false, ""
		}
		return false, true, ""
	}
	required := p.Cfg.Delivery.RequiredChecks
	if len(required) == 0 {
		for _, check := range status.StatusChecks {
			required = append(required, checkName(check))
		}
	}
	if len(required) == 0 {
		return false, true, ""
	}
	checkPending := false
	for _, name := range required {
		found := false
		for _, check := range status.StatusChecks {
			if checkName(check) != name {
				continue
			}
			found = true
			if checkFailed(check) {
				return false, false, "check " + name + " falhou"
			}
			if !checkPassed(check) {
				checkPending = true
			}
		}
		if !found {
			checkPending = true
		}
	}
	if checkPending {
		return false, true, ""
	}
	if status.Mergeable == "" || status.Mergeable == "UNKNOWN" {
		return false, true, ""
	}
	if status.Mergeable != "MERGEABLE" {
		return false, false, "PR não está mergeável: " + status.Mergeable
	}
	return true, false, ""
}

func checkName(check prCheck) string {
	if check.Name != "" {
		return check.Name
	}
	return check.Context
}

func checkPassed(check prCheck) bool {
	if check.TypeName == "StatusContext" {
		return check.State == "SUCCESS"
	}
	return check.Status == "COMPLETED" && check.Conclusion == "SUCCESS"
}

func checkFailed(check prCheck) bool {
	if check.TypeName == "StatusContext" {
		return check.State == "FAILURE" || check.State == "ERROR"
	}
	return check.Status == "COMPLETED" && check.Conclusion != "SUCCESS" && check.Conclusion != "SKIPPED"
}

func (p *Pipeline) createPR(ctx context.Context, run *RunState, wt *worktree.Worktree) error {
	if err := wt.Push(ctx); err != nil {
		return fmt.Errorf("push da branch %s: %w", run.Branch, err)
	}
	base := worktree.DefaultBranch(ctx, run.Repo)
	title := fmt.Sprintf("aiw: %s", run.Task.Title)
	body := p.prBody(run, p.readWork(run, "spec.md"))

	cmd := exec.CommandContext(ctx, "gh", "pr", "create", "--title", title, "--body", body, "--base", base, "--head", run.Branch)
	cmd.Dir = run.Worktree
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh pr create: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	run.PRURL = strings.TrimSpace(stdout.String())
	p.writeWork(run, "pr.md", run.PRURL+"\n")
	p.mirror(run)
	return nil
}

func (p *Pipeline) prBody(run *RunState, spec string) string {
	body := fmt.Sprintf("## Tarefa\n\n%s (%s)\n\n## Run\n\n- id: %s\n- branch: %s\n- custo: US$ %.2f\n\n## Spec\n\n%s\n",
		run.Task.Title, run.Task.ExternalID, run.ID, run.Branch, run.Usage.CostUSD, truncateForPrompt(spec, 8000))
	if closing := p.prClosingLine(run); closing != "" {
		body += "\n" + closing + "\n"
	}
	return body
}

func (p *Pipeline) prClosingLine(run *RunState) string {
	sourceName := run.Source
	if sourceName == "" {
		sourceName = run.Task.Source
	}
	sc, err := p.Cfg.SourceConfig(sourceName)
	if err != nil || sc.Driver != "github" {
		return ""
	}
	id := strings.TrimPrefix(strings.TrimSpace(run.Task.ExternalID), "#")
	if _, err := strconv.Atoi(id); err != nil {
		return ""
	}
	return "Closes #" + id
}

func (p *Pipeline) gate(ctx context.Context, run *RunState, name string, trace telemetry.Trace, autoApprove bool) error {
	mode := p.Cfg.Workspace.GateModeFor(name)
	forced := run.Gates[name] == "escalated"
	if !forced {
		switch mode {
		case config.GateAuto, config.GateOff, config.GatePolicy:
			run.Gates[name] = "approved"
			p.save(run, p.runDir(run))
			appendEvent(p.runDir(run), "gate.approved", map[string]any{"gate": name, "mode": string(mode)})
			trace.Event("gate.approved", map[string]any{"gate": name, "mode": string(mode)})
			return nil
		}
	}
	if run.Gates[name] == "approved" {
		return nil
	}
	if autoApprove {
		run.Gates[name] = "approved"
		p.save(run, p.runDir(run))
		appendEvent(p.runDir(run), "gate.approved", map[string]any{"gate": name, "mode": "auto"})
		trace.Event("gate.approved", map[string]any{"gate": name, "mode": "auto"})
		return nil
	}
	if !isInteractive() {
		return fmt.Errorf("%w: gate %s. Aprove com 'aiw approve %s %s' ou use --auto", ErrPaused, name, run.ID, name)
	}
	fmt.Printf("\nGate %s: artefatos em %s\nAprovar? [y/N] ", name, filepath.Join(run.Worktree, ".aiw-run"))
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" || answer == "s" || answer == "sim" {
		run.Gates[name] = "approved"
		p.save(run, p.runDir(run))
		appendEvent(p.runDir(run), "gate.approved", map[string]any{"gate": name, "mode": "interactive"})
		trace.Event("gate.approved", map[string]any{"gate": name, "mode": "interactive"})
		return nil
	}
	appendEvent(p.runDir(run), "gate.rejected", map[string]any{"gate": name})
	trace.Event("gate.rejected", map[string]any{"gate": name})
	return fmt.Errorf("%w: gate %s rejeitado; edite o artefato e rode 'aiw resume %s'", ErrPaused, name, run.ID)
}

func (p *Pipeline) callEngine(ctx context.Context, run *RunState, trace telemetry.Trace, stage, engineName, model, prompt string, budget float64) (engine.Result, error) {
	return p.callEngineNamed(ctx, run, trace, stage, stage, engineName, model, prompt, budget)
}

func (p *Pipeline) callEngineNamed(ctx context.Context, run *RunState, trace telemetry.Trace, billingStage, displayName, engineName, model, prompt string, budget float64) (engine.Result, error) {
	if budget > 0 && run.Usage.CostUSD >= budget {
		return engine.Result{}, fmt.Errorf("orçamento de US$ %.2f excedido (gasto atual US$ %.2f)", budget, run.Usage.CostUSD)
	}
	limit := 0
	if ec, err := p.Cfg.EngineConfig(engineName); err == nil {
		limit = ec.MaxConcurrency
	}
	release, err := p.Limiter.Acquire(ctx, "engine:"+engineName, limit)
	if err != nil {
		return engine.Result{}, err
	}
	defer release()

	eng, err := p.Engines.Get(engineName)
	if err != nil {
		return engine.Result{}, err
	}
	span := trace.Span("stage."+displayName, prompt)
	start := time.Now()
	res, err := eng.Run(ctx, engine.Request{
		Stage:   displayName,
		Prompt:  prompt,
		Workdir: run.Worktree,
		Model:   model,
		Timeout: p.Cfg.Defaults.Timeout.Std(),
	})
	end := time.Now()
	level := "DEFAULT"
	if err != nil {
		level = "ERROR"
	}
	span.Generation(telemetry.Generation{
		Name:   displayName,
		Model:  model,
		Input:  prompt,
		Output: res.Output,
		Usage: telemetry.Usage{
			InputTokens:  res.Usage.InputTokens,
			OutputTokens: res.Usage.OutputTokens,
			CostUSD:      res.Usage.CostUSD,
		},
		Metadata: map[string]any{
			"engine":      engineName,
			"command":     res.Command,
			"duration_ms": res.Duration.Milliseconds(),
		},
		Level: level,
		Start: start,
		End:   end,
	})
	span.End(res.Output, err)
	run.Usage = run.Usage.Add(res.Usage)
	if st := run.stage(billingStage); st != nil {
		st.Engine = engineName
		st.Model = model
		st.CostUSD += res.Usage.CostUSD
	}
	p.save(run, p.runDir(run))
	appendEvent(p.runDir(run), "engine.call", map[string]any{
		"stage":         billingStage,
		"step":          displayName,
		"engine":        engineName,
		"model":         model,
		"cost_usd":      res.Usage.CostUSD,
		"input_tokens":  res.Usage.InputTokens,
		"output_tokens": res.Usage.OutputTokens,
		"duration_ms":   res.Duration.Milliseconds(),
	})
	return res, err
}

func (p *Pipeline) engineFor(run *RunState, stage string) (string, string) {
	name := p.Cfg.StageEngine(stage)
	if run.EngineOverride != "" {
		name = run.EngineOverride
	}
	model := p.Cfg.StageModel(stage)
	if run.EngineOverride != "" {
		if ec, err := p.Cfg.EngineConfig(name); err == nil {
			model = ec.Model
		}
	}
	return name, model
}

func (p *Pipeline) promptData(run *RunState) prompts.Data {
	return prompts.Data{
		Task:           run.Task,
		Repo:           run.Repo,
		Worktree:       run.Worktree,
		RunDir:         p.runDir(run),
		IntakePath:     p.workArtifact(run, "intake.md"),
		QuestionsPath:  p.workArtifact(run, "questions.md"),
		SpecPath:       p.workArtifact(run, "spec.md"),
		SpecReviewPath: p.workArtifact(run, "spec-review.md"),
		PlanPath:       p.workArtifact(run, "plan.md"),
		PlanReviewPath: p.workArtifact(run, "plan-review.md"),
		ImplementPath:  p.workArtifact(run, "implement.md"),
		ReviewPath:     p.workArtifact(run, "review.md"),
		DiffPath:       p.workArtifact(run, "diff.patch"),
		TestLogPath:    p.workArtifact(run, "test.log"),
		TestCommands:   p.Cfg.Workspace.TestCommands,
		HumanFeedback:  p.humanFeedback(run, run.Stage),
	}
}

func (p *Pipeline) humanFeedback(run *RunState, stage string) string {
	name := ""
	switch stage {
	case "intake":
		name = "intake.md"
	case "requirements":
		name = "spec.md"
	case "architect":
		name = "plan.md"
	default:
		return ""
	}
	data, err := os.ReadFile(filepath.Join(p.runDir(run), "feedback", name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (p *Pipeline) render(name string, data prompts.Data) (string, error) {
	raw, err := fs.ReadFile(prompts.FS, name+".md")
	if err != nil {
		return "", fmt.Errorf("prompt %s não encontrado: %w", name, err)
	}
	tmpl, err := template.New(name).Option("missingkey=zero").Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("prompt %s inválido: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (p *Pipeline) snapshotDiff(run *RunState) (string, error) {
	wt := &worktree.Worktree{Repo: run.Repo, Path: run.Worktree, Branch: run.Branch, BaseCommit: run.BaseCommit}
	diff, err := wt.Diff(context.Background())
	if err != nil {
		return "", err
	}
	p.writeWork(run, "diff.patch", diff)
	p.mirror(run)
	return diff, nil
}

func (p *Pipeline) runDir(run *RunState) string {
	return filepath.Join(p.Cfg.RunsDir(), run.ID)
}

func (p *Pipeline) workArtifact(run *RunState, name string) string {
	return filepath.Join(run.Worktree, ".aiw-run", name)
}

func (p *Pipeline) writeWork(run *RunState, name, content string) string {
	path := p.workArtifact(run, name)
	if err := writeFile(path, content); err != nil {
		p.Log.Warn("falha ao gravar artefato", "path", path, "error", err)
	}
	return path
}

func (p *Pipeline) readWork(run *RunState, name string) string {
	data, err := os.ReadFile(p.workArtifact(run, name))
	if err != nil {
		return ""
	}
	return string(data)
}

func (p *Pipeline) mirror(run *RunState) {
	if run.Artifacts == nil {
		run.Artifacts = map[string]string{}
	}
	src := filepath.Join(run.Worktree, ".aiw-run")
	dest := p.runDir(run)
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dest, rel), string(data)); err != nil {
			return err
		}
		run.Artifacts[rel] = filepath.Join(dest, rel)
		return nil
	})
	if err != nil {
		p.Log.Warn("falha ao espelhar artefatos", "error", err)
	}
}

func (p *Pipeline) writeBack(ctx context.Context, run *RunState, runErr error, opts Options) {
	if opts.DisableWriteBack {
		return
	}
	src, err := p.Sources.Open(run.Source)
	if err != nil {
		p.Log.Warn("write-back ignorado: fonte indisponível", "source", run.Source, "error", err)
		return
	}
	writer, ok := src.(source.StatusWriter)
	if !ok {
		return
	}
	dir := p.runDir(run)
	if runErr == nil {
		err = writer.MarkDone(ctx, run.Ref, source.RunResult{
			RunID:   run.ID,
			PRURL:   run.PRURL,
			Summary: "run concluída",
			Branch:  run.Branch,
			CostUSD: run.Usage.CostUSD,
		})
	} else {
		err = writer.MarkFailed(ctx, run.Ref, runErr)
	}
	if err != nil {
		p.Log.Warn("falha no write-back", "error", err)
		appendEvent(dir, "writeback.failed", map[string]any{"error": err.Error()})
		return
	}
	appendEvent(dir, "writeback.done", map[string]any{"success": runErr == nil})
}

func (p *Pipeline) cancelRequested(runDir string) bool {
	_, err := os.Stat(filepath.Join(runDir, CancelMarker))
	return err == nil
}

func (p *Pipeline) pauseRequested(runDir string) bool {
	if pause.Active(p.Cfg.DataDir()) {
		return true
	}
	_, err := os.Stat(filepath.Join(runDir, PauseMarker))
	return err == nil
}

func (p *Pipeline) save(run *RunState, dir string) {
	if err := saveRun(run, dir); err != nil {
		p.Log.Warn("falha ao salvar run.json", "error", err)
	}
}

func (p *Pipeline) result(run *RunState, summary string) *Result {
	return &Result{
		RunID:    run.ID,
		Status:   run.Status,
		Stage:    run.Stage,
		RunDir:   p.runDir(run),
		Worktree: run.Worktree,
		Branch:   run.Branch,
		PRURL:    run.PRURL,
		Summary:  summary,
		Usage:    run.Usage,
	}
}

func (p *Pipeline) newRunID(task *source.Task) string {
	slug := slugify(task.ExternalID)
	if slug == "" {
		slug = "task"
	}
	if len(slug) > 24 {
		slug = slug[:24]
	}
	id := fmt.Sprintf("%s-%s-%06x", time.Now().UTC().Format("20060102-150405"), slug, time.Now().UnixNano()&0xffffff)
	if _, err := os.Stat(filepath.Join(p.Cfg.RunsDir(), id)); err == nil {
		id = fmt.Sprintf("%s-%d", id, time.Now().UnixNano())
	}
	return id
}

func slugify(value string) string {
	var buf strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			buf.WriteRune(r)
			lastDash = false
		case !lastDash:
			buf.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(buf.String(), "-")
}

func parseBlocking(review string) []string {
	var blocking []string
	for _, line := range strings.Split(review, "\n") {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "- ")
		trimmed = strings.TrimPrefix(trimmed, "* ")
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "BLOCKING:") {
			blocking = append(blocking, trimmed)
		}
	}
	return blocking
}

func boolScore(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func truncateForPrompt(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "\n...(truncado)\n"
}

func isInteractive() bool {
	if os.Getenv("AIW_NONINTERACTIVE") == "1" {
		return false
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, string(data))
}
