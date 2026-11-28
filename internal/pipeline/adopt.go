package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (p *Pipeline) AdoptPR(ctx context.Context, runID, prURL string) (*RunState, error) {
	prURL = strings.TrimSpace(prURL)
	if prURL == "" {
		return nil, fmt.Errorf("PR URL é obrigatória")
	}
	run, err := LoadRun(p.Cfg, runID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(run.Branch) == "" {
		return nil, fmt.Errorf("run %s não tem branch", runID)
	}
	status, err := p.readPRStatus(ctx, prURL)
	if err != nil {
		return nil, err
	}
	if status.HeadRefName != "" && status.HeadRefName != run.Branch {
		return nil, fmt.Errorf("PR pertence à branch %q, mas a run usa %q", status.HeadRefName, run.Branch)
	}
	if status.State != "OPEN" && status.State != "MERGED" {
		return nil, fmt.Errorf("PR %s está %s; só é possível adotar PR aberto ou mergeado", prURL, status.State)
	}
	run.PRURL = prURL
	run.CreatePR = true
	if run.Delivery == nil {
		run.Delivery = &DeliveryState{}
	}
	run.Delivery.Provider = "github"
	run.Delivery.Kind = "pull_request"
	run.Delivery.ExternalID = prNumberFromURL(prURL)
	run.Delivery.URL = prURL
	run.Delivery.Branch = run.Branch
	if status.State == "MERGED" {
		run.PRMerged = true
		if run.MergedAt.IsZero() {
			run.MergedAt = time.Now().UTC()
		}
		run.Delivery.Status = "merged"
		run.Delivery.MergedAt = run.MergedAt
	} else {
		run.Delivery.Status = "open"
	}
	if err := saveRun(run, p.runDir(run)); err != nil {
		return nil, err
	}
	appendEvent(p.runDir(run), "pr.adopted", map[string]any{"pr_url": prURL, "state": status.State})
	p.mirror(run)
	return run, nil
}
