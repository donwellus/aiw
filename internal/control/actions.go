package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pause"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/sources"
	"github.com/donwellus/aiw/internal/state"
)

type PauseResult struct {
	Scope         string `json:"scope"`
	RunID         string `json:"run_id,omitempty"`
	Status        string `json:"status,omitempty"`
	AlreadyActive bool   `json:"already_active,omitempty"`
	Message       string `json:"message"`
}

func Pause(cfg *config.Config, runID, reason string) (PauseResult, error) {
	if runID == "" {
		if pause.Active(cfg.DataDir()) {
			return PauseResult{Scope: "global", AlreadyActive: true, Message: "pausa global já está ativa"}, nil
		}
		if reason == "" {
			reason = "manual"
		}
		if err := pause.Set(cfg.DataDir(), reason); err != nil {
			return PauseResult{}, err
		}
		return PauseResult{Scope: "global", Message: "pausa global solicitada"}, nil
	}
	run, err := pipeline.LoadRun(cfg, runID)
	if err != nil {
		return PauseResult{}, err
	}
	result := PauseResult{Scope: "run", RunID: run.ID, Status: run.Status}
	switch run.Status {
	case "succeeded", "failed", "canceled":
		return result, fmt.Errorf("run %s já foi encerrada com status %s", run.ID, run.Status)
	case "needs_input":
		return result, fmt.Errorf("run %s aguarda input; pausa não se aplica", run.ID)
	case "paused":
		result.AlreadyActive = true
		result.Message = "run já está pausada"
		return result, nil
	}
	marker := filepath.Join(cfg.RunsDir(), run.ID, pipeline.PauseMarker)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return result, err
	}
	if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644); err != nil {
		return result, err
	}
	result.Message = "pausa solicitada; o pipeline para no próximo boundary de estágio"
	return result, nil
}

func Approve(cfg *config.Config, runID, gate string) (*pipeline.RunState, error) {
	run, err := pipeline.LoadRun(cfg, runID)
	if err != nil {
		return nil, err
	}
	switch gate {
	case "intake", "spec", "plan":
	default:
		return nil, fmt.Errorf("gate inválido %q (use intake, spec ou plan)", gate)
	}
	if run.Gates == nil {
		run.Gates = map[string]string{}
	}
	run.Gates[gate] = "approved"
	if err := pipeline.SaveRun(cfg, run); err != nil {
		return nil, err
	}
	return run, nil
}

type ReleaseResult struct {
	RunID          string           `json:"run_id"`
	Claim          *state.ClaimInfo `json:"claim,omitempty"`
	TaskMarked     bool             `json:"task_marked"`
	TaskSource     string           `json:"task_source,omitempty"`
	TaskID         string           `json:"task_id,omitempty"`
	SourceReturned bool             `json:"source_returned"`
	Warnings       []string         `json:"warnings,omitempty"`
}

func Release(ctx context.Context, cfg *config.Config, store *state.Store, runID string) (ReleaseResult, error) {
	result := ReleaseResult{RunID: runID}
	record, recordFound := store.FindByRunID(runID)
	run, runErr := pipeline.LoadRun(cfg, runID)
	claim, claimFound, err := store.ReleaseClaimByRunID(runID)
	if err != nil {
		return result, err
	}
	if !recordFound && !claimFound {
		return result, fmt.Errorf("run %s não encontrada no estado nem nos claims", runID)
	}
	if claimFound {
		result.Claim = &claim
	}
	if recordFound {
		record.Status = "todo"
		record.RunID = ""
		if err := store.Put(record); err != nil {
			return result, err
		}
		result.TaskMarked = true
		result.TaskSource = record.Source
		result.TaskID = record.ID
	}
	sourceName := ""
	ref := source.Ref{}
	if runErr == nil {
		sourceName = run.Source
		ref = run.Ref
	}
	if sourceName == "" && claimFound {
		sourceName = claim.Source
		ref = source.Ref{Source: claim.Source, ID: claim.ID}
	}
	if sourceName == "" {
		return result, nil
	}
	src, err := sources.NewRegistry(cfg).Open(sourceName)
	if err != nil {
		result.Warnings = append(result.Warnings, "fonte indisponível para devolver tarefa: "+err.Error())
		return result, nil
	}
	releaser, ok := src.(source.Releaser)
	if !ok {
		return result, nil
	}
	if err := releaser.Release(ctx, ref); err != nil {
		result.Warnings = append(result.Warnings, "falha ao devolver tarefa ao inbox: "+err.Error())
		return result, nil
	}
	result.SourceReturned = true
	return result, nil
}

func ResumeCandidates(store *state.Store) []string {
	seen := map[string]bool{}
	var runs []string
	for _, record := range state.LatestByID(store.All()) {
		if record.RunID == "" {
			continue
		}
		if record.Status != "paused" && record.Status != "todo" {
			continue
		}
		if seen[record.RunID] {
			continue
		}
		seen[record.RunID] = true
		runs = append(runs, record.RunID)
	}
	return runs
}

func IsTerminalStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "canceled":
		return true
	default:
		return false
	}
}

func ValidateRunID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return errors.New("run_id inválido")
	}
	return nil
}
