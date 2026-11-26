package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/telemetry"
	"github.com/donwellus/aiw/internal/worktree"
)

type externalReviewOutcome struct {
	Handled bool
	Fixed   bool
}

type externalVerdict struct {
	Decision string
	Reply    string
	Details  string
}

func (p *Pipeline) externalReviewMode() string {
	switch strings.ToLower(strings.TrimSpace(p.Cfg.Delivery.ExternalReview)) {
	case "consume", "require":
		return strings.ToLower(strings.TrimSpace(p.Cfg.Delivery.ExternalReview))
	default:
		return "ignore"
	}
}

func (p *Pipeline) externalReviewer(run *RunState) (source.PullRequestReviewer, error) {
	if p.externalReviewMode() == "ignore" {
		return nil, nil
	}
	src, err := p.Sources.Open(run.Source)
	if err != nil {
		return nil, err
	}
	reviewer, ok := src.(source.PullRequestReviewer)
	if !ok {
		return nil, fmt.Errorf("fonte %s não suporta ingestão de review externo", run.Source)
	}
	return reviewer, nil
}

func (p *Pipeline) externalState(run *RunState) *ExternalReviewState {
	if run.ExternalReview == nil {
		run.ExternalReview = &ExternalReviewState{}
	}
	return run.ExternalReview
}

func (p *Pipeline) pendingExternalFeedback(run *RunState, feedback []source.PullRequestFeedback) []source.PullRequestFeedback {
	state := p.externalState(run)
	processed := make(map[string]bool, len(state.Processed))
	for _, id := range state.Processed {
		processed[id] = true
	}
	pending := make([]source.PullRequestFeedback, 0, len(feedback))
	for _, item := range feedback {
		if processed[item.ID] || strings.TrimSpace(item.Body) == "" {
			continue
		}
		pending = append(pending, item)
	}
	return pending
}

func (p *Pipeline) handleExternalReview(ctx context.Context, run *RunState, trace telemetry.Trace, wt *worktree.Worktree, reviewer source.PullRequestReviewer, pr source.PullRequest, feedback []source.PullRequestFeedback, budget float64) (externalReviewOutcome, error) {
	pending := p.pendingExternalFeedback(run, feedback)
	if len(pending) == 0 {
		return externalReviewOutcome{}, nil
	}
	state := p.externalState(run)
	maxRounds := p.Cfg.Delivery.MaxFixRounds
	if maxRounds <= 0 {
		maxRounds = 3
	}
	if state.Rounds >= maxRounds {
		message := fmt.Sprintf("review externo excedeu %d rodada(s)", maxRounds)
		path := p.workArtifact(run, "external-review-limit.md")
		p.writeWork(run, "external-review-limit.md", "# Review externo bloqueado\n\n"+message+"\n\n## Feedback pendente\n\n"+externalFeedbackText(pending)+"\n")
		state.ConflictPath = path
		p.markExternalProcessed(run, pending)
		p.save(run, p.runDir(run))
		appendEvent(p.runDir(run), "external_review.limit", map[string]any{"round": state.Rounds, "feedback": len(pending)})
		p.mirror(run)
		return externalReviewOutcome{Handled: true}, fmt.Errorf("%w: %s", ErrDeliveryPending, message)
	}

	state.Rounds++
	round := state.Rounds
	data := p.promptData(run)
	data.ExternalFeedback = externalFeedbackText(pending)
	data.ExternalPath = p.workArtifact(run, fmt.Sprintf("external-review-round-%d.md", round))
	data.ExternalRound = round
	p.writeWork(run, fmt.Sprintf("external-review-feedback-round-%d.md", round), externalFeedbackText(pending))
	prompt, err := p.render("external-correct", data)
	if err != nil {
		return externalReviewOutcome{Handled: true}, err
	}
	engineName, model := p.engineFor(run, "correct")
	res, err := p.callEngineNamed(ctx, run, trace, "deliver", "external-review", engineName, model, prompt, budget)
	if err != nil {
		return externalReviewOutcome{Handled: true}, err
	}
	artifactPath := data.ExternalPath
	if _, statErr := os.Stat(artifactPath); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return externalReviewOutcome{Handled: true}, statErr
		}
		if strings.TrimSpace(res.Output) == "" {
			return externalReviewOutcome{Handled: true}, fmt.Errorf("engine %s não gerou external-review-round-%d.md", engineName, round)
		}
		p.writeWork(run, fmt.Sprintf("external-review-round-%d.md", round), res.Output+"\n")
	}
	verdict := parseExternalVerdict(p.readWork(run, fmt.Sprintf("external-review-round-%d.md", round)))
	state.LastDecision = verdict.Decision
	state.LastFeedbackAt = time.Now().UTC()
	p.save(run, p.runDir(run))

	switch verdict.Decision {
	case "FIXED":
		ok, log, err := p.runTests(ctx, run, trace)
		if err != nil {
			return externalReviewOutcome{Handled: true}, err
		}
		if !ok {
			p.writeWork(run, "external-review-blocked.md", "# Review externo bloqueado\n\nOs testes falharam após a correção.\n\n```\n"+truncateForPrompt(log, 20000)+"\n```\n")
			p.mirror(run)
			return externalReviewOutcome{Handled: true}, fmt.Errorf("%w: testes falharam após correção de review externo", ErrDeliveryPending)
		}
		if _, err := p.snapshotDiff(run); err != nil {
			return externalReviewOutcome{Handled: true}, err
		}
		committed, err := wt.Commit(ctx, fmt.Sprintf("aiw: address external review (%s)", run.ID))
		if err != nil {
			return externalReviewOutcome{Handled: true}, err
		}
		if !committed {
			return externalReviewOutcome{Handled: true}, fmt.Errorf("%w: corretor externo não alterou o worktree", ErrDeliveryPending)
		}
		if err := wt.Push(ctx); err != nil {
			return externalReviewOutcome{Handled: true}, fmt.Errorf("%w: push da correção externa: %v", ErrDeliveryPending, err)
		}
		p.replyExternalReview(ctx, reviewer, pr, pending, verdict.Reply, false)
		p.markExternalProcessed(run, pending)
		p.save(run, p.runDir(run))
		appendEvent(p.runDir(run), "external_review.fixed", map[string]any{"round": round, "feedback": len(pending)})
		p.mirror(run)
		return externalReviewOutcome{Handled: true, Fixed: true}, nil
	case "NO_ACTION":
		p.replyExternalReview(ctx, reviewer, pr, pending, verdict.Reply, false)
		p.markExternalProcessed(run, pending)
		p.save(run, p.runDir(run))
		appendEvent(p.runDir(run), "external_review.no_action", map[string]any{"round": round, "feedback": len(pending)})
		p.mirror(run)
		return externalReviewOutcome{Handled: true}, nil
	default:
		path := p.workArtifact(run, "external-review-conflict.md")
		p.writeWork(run, "external-review-conflict.md", "# Review externo exige decisão humana\n\nDECISION: "+verdict.Decision+"\n\n"+verdict.Details+"\n\n## Feedback\n\n"+externalFeedbackText(pending)+"\n")
		state.ConflictPath = path
		p.markExternalProcessed(run, pending)
		p.save(run, p.runDir(run))
		appendEvent(p.runDir(run), "external_review.conflict", map[string]any{"round": round, "decision": verdict.Decision})
		p.mirror(run)
		return externalReviewOutcome{Handled: true}, fmt.Errorf("%w: review externo exige mudança de requisitos/spec/plano; veja %s", ErrDeliveryPending, path)
	}
}

func (p *Pipeline) replyExternalReview(ctx context.Context, reviewer source.PullRequestReviewer, pr source.PullRequest, feedback []source.PullRequestFeedback, reply string, conflict bool) {
	if !p.externalReplyAllowed(conflict) {
		return
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		reply = "aiw: feedback verificado; nenhuma promessa de merge é feita por este comentário."
	}
	for _, item := range feedback {
		if err := reviewer.ReplyPullRequestFeedback(ctx, pr, item, reply); err != nil {
			p.Log.Warn("falha ao responder review externo", "feedback", item.ID, "error", err)
		}
	}
}

func (p *Pipeline) externalReplyAllowed(conflict bool) bool {
	switch strings.ToLower(strings.TrimSpace(p.Cfg.Delivery.ExternalReply)) {
	case "never":
		return false
	case "auto":
		return true
	default:
		return !conflict
	}
}

func (p *Pipeline) markExternalProcessed(run *RunState, feedback []source.PullRequestFeedback) {
	state := p.externalState(run)
	existing := make(map[string]bool, len(state.Processed))
	for _, id := range state.Processed {
		existing[id] = true
	}
	for _, item := range feedback {
		if item.ID == "" || existing[item.ID] {
			continue
		}
		state.Processed = append(state.Processed, item.ID)
		existing[item.ID] = true
	}
}

func externalFeedbackText(feedback []source.PullRequestFeedback) string {
	var builder strings.Builder
	for _, item := range feedback {
		location := item.Kind
		if item.Path != "" {
			location += " " + item.Path
			if item.Line > 0 {
				location += fmt.Sprintf(":%d", item.Line)
			}
		}
		fmt.Fprintf(&builder, "### %s — %s", item.ID, location)
		if item.Author != "" {
			fmt.Fprintf(&builder, " (%s)", item.Author)
		}
		if item.State != "" {
			fmt.Fprintf(&builder, " [%s]", item.State)
		}
		builder.WriteString("\n\n")
		builder.WriteString(truncateForPrompt(item.Body, 12000))
		builder.WriteString("\n\n")
	}
	return strings.TrimSpace(builder.String())
}

func parseExternalVerdict(text string) externalVerdict {
	verdict := externalVerdict{Decision: "NEEDS_CLARIFICATION"}
	lines := strings.Split(text, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "DECISION:"):
			verdict.Decision = normalizeExternalDecision(strings.TrimSpace(line[len("DECISION:"):]))
		case strings.HasPrefix(upper, "REPLY:"):
			verdict.Reply = strings.TrimSpace(line[len("REPLY:"):])
		}
	}
	if idx := strings.Index(strings.ToUpper(text), "DETAILS:"); idx >= 0 {
		verdict.Details = strings.TrimSpace(text[idx+len("DETAILS:"):])
	}
	return verdict
}

func normalizeExternalDecision(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "FIXED", "NO_ACTION", "CONFLICT", "NEEDS_CLARIFICATION":
		return value
	default:
		return "NEEDS_CLARIFICATION"
	}
}
