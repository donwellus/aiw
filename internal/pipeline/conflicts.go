package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/telemetry"
	"github.com/donwellus/aiw/internal/worktree"
)

type conflictVerdict struct {
	Decision string
	Details  string
}

func (p *Pipeline) resolveConflictsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(p.Cfg.Delivery.ResolveConflicts)) {
	case "", "auto":
		return true
	default:
		return false
	}
}

func staleHead(status prStatus, fixedHead string) bool {
	return fixedHead != "" && status.State != "MERGED" && status.HeadRefOid != "" && status.HeadRefOid != fixedHead
}

func prHasConflict(status prStatus) bool {
	return strings.EqualFold(strings.TrimSpace(status.Mergeable), "CONFLICTING") ||
		strings.EqualFold(strings.TrimSpace(status.MergeStateStatus), "DIRTY")
}

func (p *Pipeline) conflictState(run *RunState) *ConflictState {
	if run.Conflict == nil {
		run.Conflict = &ConflictState{}
	}
	return run.Conflict
}

func (p *Pipeline) resolveMergeConflict(ctx context.Context, run *RunState, trace telemetry.Trace, wt *worktree.Worktree, status prStatus, budget float64) (bool, error) {
	if !p.resolveConflictsEnabled() {
		return false, nil
	}
	state := p.conflictState(run)
	maxRounds := p.Cfg.Delivery.ConflictMaxRounds
	if maxRounds <= 0 {
		maxRounds = 2
	}
	if state.Attempts >= maxRounds {
		message := fmt.Sprintf("conflito de merge excedeu %d rodada(s)", maxRounds)
		p.writeWork(run, "conflict-limit.md", "# Conflito de merge bloqueado\n\n"+message+"\n")
		state.Status = "paused"
		state.LastError = message
		p.save(run, p.runDir(run))
		appendEvent(p.runDir(run), "conflict.limit", map[string]any{"attempts": state.Attempts})
		p.mirror(run)
		return false, fmt.Errorf("%w: %s", ErrDeliveryPending, message)
	}

	baseRef := strings.TrimSpace(status.BaseRefName)
	if baseRef == "" {
		baseRef = "main"
	}
	state.Attempts++
	state.BaseRef = baseRef
	state.Status = "resolving"
	state.LastError = ""
	p.save(run, p.runDir(run))
	appendEvent(p.runDir(run), "conflict.detected", map[string]any{"attempt": state.Attempts, "base": baseRef})

	if err := wt.Fetch(ctx, "origin", baseRef); err != nil {
		return p.failConflict(ctx, run, wt, state, fmt.Errorf("fetch da base %s: %w", baseRef, err))
	}
	conflicts, err := wt.Merge(ctx, "origin/"+baseRef)
	if err != nil {
		return p.failConflict(ctx, run, wt, state, fmt.Errorf("merge da base %s: %w", baseRef, err))
	}
	if len(conflicts) > 0 {
		state.Files = append([]string(nil), conflicts...)
		p.writeWork(run, "conflict.md", "# Conflito de merge\n\nBase: origin/"+baseRef+"\n\n```\n"+strings.Join(conflicts, "\n")+"\n```\n")
		for _, file := range conflicts {
			if protectedConflictPath(file) {
				message := "conflito em arquivo de requisitos/spec/plano; decisão humana necessária: " + file
				p.abortMerge(ctx, wt)
				return p.failConflict(ctx, run, wt, state, errors.New(message))
			}
		}
		handled, err := p.resolveConflictWithEngine(ctx, run, trace, wt, baseRef, conflicts, state, budget)
		if err != nil {
			return handled, err
		}
	}

	ok, log, err := p.runTests(ctx, run, trace)
	if err != nil {
		p.abortMerge(ctx, wt)
		return p.failConflict(ctx, run, wt, state, err)
	}
	if !ok {
		p.writeWork(run, "conflict-blocked.md", "# Conflito de merge bloqueado\n\nOs testes falharam após a resolução.\n\n```\n"+truncateForPrompt(log, 20000)+"\n```\n")
		p.abortMerge(ctx, wt)
		return p.failConflict(ctx, run, wt, state, errors.New("testes falharam após resolver conflito de merge"))
	}
	if err := wt.Push(ctx); err != nil {
		return p.failConflict(ctx, run, wt, state, fmt.Errorf("push da resolução de conflito: %w", err))
	}
	if baseSHA, err := wt.RevParse(ctx, "origin/"+baseRef); err == nil {
		run.BaseCommit = baseSHA
		wt.BaseCommit = baseSHA
		state.BaseCommit = baseSHA
	}
	state.Status = "resolved"
	state.ResolvedAt = time.Now().UTC()
	state.LastError = ""
	p.save(run, p.runDir(run))
	appendEvent(p.runDir(run), "conflict.resolved", map[string]any{"attempt": state.Attempts, "base": baseRef, "files": state.Files})
	p.mirror(run)
	return true, nil
}

func (p *Pipeline) resolveConflictWithEngine(ctx context.Context, run *RunState, trace telemetry.Trace, wt *worktree.Worktree, baseRef string, conflicts []string, state *ConflictState, budget float64) (bool, error) {
	round := state.Attempts
	data := p.promptData(run)
	data.ConflictFiles = strings.Join(conflicts, "\n")
	data.ConflictPath = p.workArtifact(run, fmt.Sprintf("conflict-round-%d.md", round))
	data.ConflictRound = round
	prompt, err := p.render("conflict-correct", data)
	if err != nil {
		p.abortMerge(ctx, wt)
		return p.failConflict(ctx, run, wt, state, err)
	}
	engineName, model := p.engineFor(run, "correct")
	res, err := p.callEngineNamed(ctx, run, trace, "deliver", "conflict", engineName, model, prompt, budget)
	if err != nil {
		p.abortMerge(ctx, wt)
		return p.failConflict(ctx, run, wt, state, err)
	}
	artifactPath := data.ConflictPath
	if _, statErr := os.Stat(artifactPath); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			p.abortMerge(ctx, wt)
			return p.failConflict(ctx, run, wt, state, statErr)
		}
		if strings.TrimSpace(res.Output) == "" {
			p.abortMerge(ctx, wt)
			return p.failConflict(ctx, run, wt, state, fmt.Errorf("engine %s não gerou %s", engineName, filepath.Base(artifactPath)))
		}
		p.writeWork(run, fmt.Sprintf("conflict-round-%d.md", round), res.Output+"\n")
	}
	verdict := parseConflictVerdict(p.readWork(run, fmt.Sprintf("conflict-round-%d.md", round)))
	if verdict.Decision != "RESOLVED" {
		p.writeWork(run, "conflict.md", "# Conflito de merge exige decisão\n\nDECISION: "+verdict.Decision+"\n\n"+verdict.Details+"\n\n## Arquivos\n\n```\n"+strings.Join(conflicts, "\n")+"\n```\n")
		p.abortMerge(ctx, wt)
		return p.failConflict(ctx, run, wt, state, errors.New("resolvedor não concluiu o conflito: "+verdict.Decision))
	}
	for _, file := range conflicts {
		data, err := os.ReadFile(filepath.Join(run.Worktree, file))
		if err != nil {
			p.abortMerge(ctx, wt)
			return p.failConflict(ctx, run, wt, state, err)
		}
		if hasConflictMarkers(string(data)) {
			p.abortMerge(ctx, wt)
			return p.failConflict(ctx, run, wt, state, fmt.Errorf("marcadores de conflito permanecem em %s", file))
		}
	}
	if err := wt.Add(ctx, conflicts...); err != nil {
		p.abortMerge(ctx, wt)
		return p.failConflict(ctx, run, wt, state, err)
	}
	remaining, err := wt.ConflictFiles(ctx)
	if err != nil {
		p.abortMerge(ctx, wt)
		return p.failConflict(ctx, run, wt, state, err)
	}
	if len(remaining) > 0 {
		p.abortMerge(ctx, wt)
		return p.failConflict(ctx, run, wt, state, fmt.Errorf("conflitos ainda não resolvidos em %s", strings.Join(remaining, ", ")))
	}
	if err := wt.CommitMerge(ctx); err != nil {
		p.abortMerge(ctx, wt)
		return p.failConflict(ctx, run, wt, state, err)
	}
	appendEvent(p.runDir(run), "conflict.engine_resolved", map[string]any{"attempt": round, "base": baseRef, "files": conflicts})
	p.mirror(run)
	return true, nil
}

func (p *Pipeline) failConflict(ctx context.Context, run *RunState, wt *worktree.Worktree, state *ConflictState, err error) (bool, error) {
	p.abortMerge(ctx, wt)
	state.Status = "paused"
	state.LastError = err.Error()
	p.save(run, p.runDir(run))
	appendEvent(p.runDir(run), "conflict.failed", map[string]any{"attempt": state.Attempts, "error": err.Error()})
	p.mirror(run)
	return true, fmt.Errorf("%w: conflito de merge: %v", ErrDeliveryPending, err)
}

func (p *Pipeline) abortMerge(ctx context.Context, wt *worktree.Worktree) {
	if wt == nil {
		return
	}
	if err := wt.AbortMerge(ctx); err != nil {
		p.Log.Warn("falha ao abortar merge de conflito", "error", err)
	}
}

func protectedConflictPath(path string) bool {
	switch strings.ToLower(filepath.Clean(path)) {
	case "intake.md", "spec.md", "plan.md":
		return true
	default:
		return false
	}
}

func hasConflictMarkers(value string) bool {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "<<<<<<<") || strings.HasPrefix(line, "=======") || strings.HasPrefix(line, ">>>>>>>") {
			return true
		}
	}
	return false
}

func parseConflictVerdict(text string) conflictVerdict {
	verdict := conflictVerdict{Decision: "CONFLICT"}
	upper := strings.ToUpper(text)
	if idx := strings.Index(upper, "DETAILS:"); idx >= 0 {
		verdict.Details = strings.TrimSpace(text[idx+len("DETAILS:"):])
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "DECISION:") {
			value := strings.ToUpper(strings.TrimSpace(line[len("DECISION:"):]))
			switch value {
			case "RESOLVED":
				verdict.Decision = "RESOLVED"
			case "CONFLICT", "NEEDS_CLARIFICATION":
				verdict.Decision = value
			default:
				verdict.Decision = "CONFLICT"
			}
			break
		}
	}
	return verdict
}
