package control

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/sources"
	"github.com/donwellus/aiw/internal/state"
)

type CloseIssueResult struct {
	IssueID  string `json:"issue_id"`
	Source   string `json:"source"`
	RunID    string `json:"run_id,omitempty"`
	Closed   bool   `json:"closed"`
	Canceled bool   `json:"canceled,omitempty"`
	Status   string `json:"status,omitempty"`
	Message  string `json:"message,omitempty"`
}

func CloseIssue(ctx context.Context, cfg *config.Config, store *state.Store, issueID, reason string, cancelRun bool) (CloseIssueResult, error) {
	issueID = strings.TrimSpace(issueID)
	reason = strings.TrimSpace(reason)
	if issueID == "" {
		return CloseIssueResult{}, errors.New("issue_id vazio")
	}
	if reason == "" {
		return CloseIssueResult{}, errors.New("motivo do fechamento é obrigatório")
	}
	result := CloseIssueResult{IssueID: issueID, Message: "issue fechada"}
	sourceName := cfg.DefaultSource
	if info, active := DaemonActive(cfg, 0); active && strings.TrimSpace(info.Source) != "" {
		sourceName = info.Source
	}
	ref := source.Ref{Source: sourceName, ID: issueID}
	record, found := store.FindByID(issueID)
	if found {
		result.Source = record.Source
		if record.Source != "" {
			sourceName = record.Source
			ref.Source = record.Source
		}
		result.RunID = record.RunID
	}
	if result.Source == "" {
		result.Source = sourceName
	}
	if record.RunID != "" {
		run, err := pipeline.LoadRun(cfg, record.RunID)
		if err != nil {
			return result, fmt.Errorf("carregando run %s: %w", record.RunID, err)
		}
		ref = run.Ref
		ref.Source = sourceName
		if ref.ID == "" {
			ref.ID = issueID
		}
		if strings.TrimSpace(run.PRURL) != "" && !run.PRMerged {
			return result, fmt.Errorf("run %s tem PR aberto (%s); feche o PR ou faça o merge antes de fechar a issue", run.ID, run.PRURL)
		}
		if !IsTerminalStatus(run.Status) {
			if !cancelRun {
				return result, fmt.Errorf("run %s está %s; use cancel_run=true para cancelar antes de fechar", run.ID, run.Status)
			}
			canceled, err := Cancel(ctx, cfg, store, run.ID)
			if err != nil {
				return result, err
			}
			result.Canceled = canceled.Canceled
			result.Status = canceled.Status
			if !canceled.Canceled {
				result.Closed = false
				result.Message = "cancelamento solicitado; a issue não será fechada até a run parar: " + canceled.Message
				return result, nil
			}
			result.Message = "run cancelada; " + result.Message
		}
	}
	src, err := sources.NewRegistry(cfg).Open(sourceName)
	if err != nil {
		return result, err
	}
	closer, ok := src.(source.IssueCloser)
	if !ok {
		return result, fmt.Errorf("fonte %s não suporta fechamento de issue", sourceName)
	}
	if err := closer.CloseIssue(ctx, ref, reason); err != nil {
		return result, err
	}
	result.Closed = true
	if found {
		record.Status = "canceled"
		if err := store.Put(record); err != nil {
			return result, err
		}
	}
	return result, nil
}
