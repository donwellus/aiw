package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/telemetry"
	"github.com/donwellus/aiw/internal/worktree"
)

var ciJobURL = regexp.MustCompile(`/actions/runs/([0-9]+)(?:/job/([0-9]+))?`)

type ciVerdict struct {
	Decision string
	Details  string
}

func (p *Pipeline) ciFixState(run *RunState) *CIFixState {
	if run.CIFix == nil {
		run.CIFix = &CIFixState{}
	}
	return run.CIFix
}

func (p *Pipeline) ciFixCheckAllowed(name string) bool {
	name = strings.TrimSpace(name)
	for _, allowed := range p.Cfg.Delivery.CIFixChecks {
		if strings.EqualFold(strings.TrimSpace(allowed), name) {
			return true
		}
	}
	return false
}

func (p *Pipeline) failedCIFixChecks(status prStatus) []prCheck {
	if !p.Cfg.Delivery.CIFixEnabled() {
		return nil
	}
	var failed []prCheck
	for _, check := range status.StatusChecks {
		name := checkName(check)
		if p.ciFixCheckAllowed(name) && checkFailed(check) {
			failed = append(failed, check)
		}
	}
	return failed
}

func (p *Pipeline) remediateCIFailure(ctx context.Context, run *RunState, trace telemetry.Trace, wt *worktree.Worktree, check prCheck, budget float64) (bool, error) {
	state := p.ciFixState(run)
	maxRounds := p.Cfg.Delivery.MaxCIFixRounds
	if maxRounds <= 0 {
		maxRounds = 3
	}
	checkLabel := checkName(check)
	if state.Rounds >= maxRounds {
		reason := fmt.Sprintf("check %s excedeu %d rodada(s) de correção automática", checkLabel, maxRounds)
		return false, p.pauseCIFix(run, checkLabel, reason, "")
	}

	logText, err := p.fetchCIFailureLog(ctx, run, check)
	if err != nil {
		return false, p.pauseCIFix(run, checkLabel, "não foi possível obter o log do CI: "+err.Error(), "")
	}
	state.Rounds++
	state.LastCheck = checkLabel
	state.LastError = ""
	state.UpdatedAt = time.Now().UTC()
	p.save(run, p.runDir(run))

	logName := fmt.Sprintf("ci-failure-round-%d.log", state.Rounds)
	p.writeWork(run, logName, logText)
	fixName := fmt.Sprintf("ci-fix-round-%d.md", state.Rounds)
	fixPath := p.workArtifact(run, fixName)

	data := p.promptData(run)
	data.CICheck = checkLabel
	data.CIFailure = truncateForPrompt(logText, 60000)
	data.CIFailurePath = p.workArtifact(run, logName)
	data.CIFixPath = fixPath
	data.CIFixRound = state.Rounds
	prompt, err := p.render("ci-correct", data)
	if err != nil {
		return false, err
	}
	engineName, model := p.engineFor(run, "correct")
	res, err := p.callEngineNamed(ctx, run, trace, "deliver", "ci-correct", engineName, model, prompt, budget)
	if err != nil {
		state.LastError = err.Error()
		state.UpdatedAt = time.Now().UTC()
		p.save(run, p.runDir(run))
		return false, p.pauseCIFix(run, checkLabel, "agente de qualidade falhou: "+err.Error(), logText)
	}
	if _, statErr := os.Stat(fixPath); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return false, statErr
		}
		if strings.TrimSpace(res.Output) == "" {
			return false, p.pauseCIFix(run, checkLabel, "agente de qualidade não gerou resposta", logText)
		}
		p.writeWork(run, fixName, res.Output+"\n")
	}
	verdict := parseCIVerdict(p.readWork(run, fixName))
	state.LastDecision = verdict.Decision
	state.UpdatedAt = time.Now().UTC()
	appendEvent(p.runDir(run), "ci.fix.decision", map[string]any{"round": state.Rounds, "check": checkLabel, "decision": verdict.Decision})
	p.save(run, p.runDir(run))

	switch verdict.Decision {
	case "FIX_CODE", "FIX_TEST":
		files, err := wt.WorkingFiles(ctx)
		if err != nil {
			return false, err
		}
		if len(files) == 0 {
			return false, p.pauseCIFix(run, checkLabel, "agente decidiu "+verdict.Decision+" mas não alterou arquivos", logText)
		}
		if err := validateCIFixFiles(verdict.Decision, files); err != nil {
			return false, p.pauseCIFix(run, checkLabel, err.Error(), logText)
		}
		ok, localLog, err := p.runTests(ctx, run, trace)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, p.pauseCIFix(run, checkLabel, "testes locais falharam após a correção:\n"+truncateForPrompt(localLog, 12000), logText)
		}
		if _, err := p.snapshotDiff(run); err != nil {
			return false, err
		}
		committed, err := wt.Commit(ctx, fmt.Sprintf("aiw: fix CI %s (%s)", checkLabel, run.ID))
		if err != nil {
			return false, err
		}
		if !committed {
			return false, p.pauseCIFix(run, checkLabel, "agente não produziu alteração para commit", logText)
		}
		if err := wt.Push(ctx); err != nil {
			return false, p.pauseCIFix(run, checkLabel, "push da correção de CI falhou: "+err.Error(), logText)
		}
		head, _ := wt.RevParse(ctx, "HEAD")
		state.LastHead = strings.TrimSpace(head)
		state.BlockersPath = ""
		state.UpdatedAt = time.Now().UTC()
		p.save(run, p.runDir(run))
		p.mirror(run)
		appendEvent(p.runDir(run), "ci.fix.pushed", map[string]any{"round": state.Rounds, "check": checkLabel, "head": state.LastHead})
		return true, nil
	case "RETRY_CI":
		if err := rerunCIFailure(ctx, run, check); err != nil {
			return false, p.pauseCIFix(run, checkLabel, "não foi possível reexecutar o CI: "+err.Error(), logText)
		}
		state.LastError = ""
		state.UpdatedAt = time.Now().UTC()
		p.save(run, p.runDir(run))
		appendEvent(p.runDir(run), "ci.fix.retry", map[string]any{"round": state.Rounds, "check": checkLabel})
		return true, nil
	case "NEEDS_CLARIFICATION":
		return false, p.pauseCIFix(run, checkLabel, "agente de qualidade precisa de decisão humana:\n"+verdict.Details, logText)
	default:
		return false, p.pauseCIFix(run, checkLabel, "decisão de CI desconhecida: "+verdict.Decision, logText)
	}
}

func (p *Pipeline) pauseCIFix(run *RunState, check, reason, logText string) error {
	state := p.ciFixState(run)
	var builder strings.Builder
	builder.WriteString("# Correção de CI bloqueada\n\n")
	fmt.Fprintf(&builder, "Check: %s\n\n", check)
	if state.Rounds > 0 {
		fmt.Fprintf(&builder, "Rodada: %d\n\n", state.Rounds)
	}
	builder.WriteString(reason)
	builder.WriteString("\n\n")
	if strings.TrimSpace(logText) != "" {
		builder.WriteString("## Log do CI\n\n```\n")
		builder.WriteString(truncateForPrompt(logText, 20000))
		builder.WriteString("\n```\n")
	}
	p.writeWork(run, "ci-blockers.md", builder.String())
	state.BlockersPath = p.workArtifact(run, "ci-blockers.md")
	state.LastError = reason
	state.UpdatedAt = time.Now().UTC()
	p.save(run, p.runDir(run))
	p.mirror(run)
	appendEvent(p.runDir(run), "ci.fix.blocked", map[string]any{"check": check, "reason": reason})
	return fmt.Errorf("%w: %s", ErrDeliveryPending, reason)
}

func (p *Pipeline) fetchCIFailureLog(ctx context.Context, run *RunState, check prCheck) (string, error) {
	url := strings.TrimSpace(check.DetailsURL)
	if url == "" {
		url = strings.TrimSpace(check.TargetURL)
	}
	if url == "" {
		return "", errors.New("check sem URL de detalhes")
	}
	match := ciJobURL.FindStringSubmatch(url)
	if len(match) < 2 {
		return "", fmt.Errorf("URL de check não é uma execução do GitHub Actions: %s", url)
	}
	args := []string{"run", "view", match[1], "--log-failed"}
	if len(match) >= 3 && match[2] != "" {
		args = append(args, "--job", match[2])
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = run.Worktree
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh run view: %w: %s", err, truncateForPrompt(stderr.String(), 4000))
	}
	logText := strings.TrimSpace(stdout.String())
	if logText == "" {
		return "", errors.New("log do check vazio")
	}
	return logText, nil
}

func rerunCIFailure(ctx context.Context, run *RunState, check prCheck) error {
	url := strings.TrimSpace(check.DetailsURL)
	if url == "" {
		url = strings.TrimSpace(check.TargetURL)
	}
	match := ciJobURL.FindStringSubmatch(url)
	if len(match) < 2 {
		return errors.New("URL de check inválida para rerun")
	}
	cmd := exec.CommandContext(ctx, "gh", "run", "rerun", match[1], "--failed")
	cmd.Dir = run.Worktree
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh run rerun: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func parseCIVerdict(text string) ciVerdict {
	var verdict ciVerdict
	var details []string
	inDetails := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "DECISION:") {
			verdict.Decision = strings.ToUpper(strings.TrimSpace(trimmed[len("DECISION:"):]))
			continue
		}
		if strings.HasPrefix(upper, "DETAILS:") {
			inDetails = true
			if rest := strings.TrimSpace(trimmed[len("DETAILS:"):]); rest != "" {
				details = append(details, rest)
			}
			continue
		}
		if inDetails {
			details = append(details, line)
		}
	}
	verdict.Details = strings.TrimSpace(strings.Join(details, "\n"))
	return verdict
}

func validateCIFixFiles(decision string, files []string) error {
	for _, file := range files {
		clean := filepath.ToSlash(strings.TrimSpace(file))
		if clean == "" {
			continue
		}
		if strings.HasPrefix(clean, ".github/workflows/") || strings.HasPrefix(clean, ".aiw-run/") {
			return fmt.Errorf("correção de CI não pode alterar %s", clean)
		}
		if decision != "FIX_TEST" {
			continue
		}
		if !isTestFile(clean) {
			return fmt.Errorf("FIX_TEST só pode alterar testes/fixtures; arquivo rejeitado: %s", clean)
		}
	}
	return nil
}

func isTestFile(file string) bool {
	file = strings.ToLower(file)
	if strings.Contains(file, "/test/") || strings.Contains(file, "/tests/") ||
		strings.Contains(file, "/e2e/") || strings.Contains(file, "/__tests__/") ||
		strings.Contains(file, "/fixtures/") || strings.Contains(file, "testdata/") {
		return true
	}
	base := filepath.Base(file)
	return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}
