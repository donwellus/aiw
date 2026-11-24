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

type CancelResult struct {
	RunID    string `json:"run_id"`
	Status   string `json:"status"`
	Canceled bool   `json:"canceled"`
	Message  string `json:"message"`
}

func Cancel(ctx context.Context, cfg *config.Config, store *state.Store, runID string) (CancelResult, error) {
	if err := ValidateRunID(runID); err != nil {
		return CancelResult{}, err
	}
	run, err := pipeline.LoadRun(cfg, runID)
	if err != nil {
		return CancelResult{}, err
	}
	result := CancelResult{RunID: run.ID, Status: run.Status}
	if IsTerminalStatus(run.Status) {
		return result, fmt.Errorf("run %s já foi encerrada com status %s", run.ID, run.Status)
	}
	marker := filepath.Join(cfg.RunsDir(), run.ID, pipeline.CancelMarker)
	if run.Status == "running" {
		if _, found := store.FindClaimByRunID(run.ID); found {
			if err := writeMarker(marker); err != nil {
				return result, err
			}
			result.Status = "canceling"
			result.Message = "cancelamento solicitado; a run para no próximo boundary/tool call"
			return result, nil
		}
	}
	return finalizeCanceled(ctx, cfg, store, run)
}

func finalizeCanceled(ctx context.Context, cfg *config.Config, store *state.Store, run *pipeline.RunState) (CancelResult, error) {
	result := CancelResult{RunID: run.ID, Status: "canceled", Canceled: true, Message: "run cancelada"}
	_ = os.Remove(filepath.Join(cfg.RunsDir(), run.ID, pipeline.PauseMarker))
	_ = os.Remove(filepath.Join(cfg.RunsDir(), run.ID, pipeline.CancelMarker))
	run.Status = "canceled"
	run.Error = "cancelada"
	if err := pipeline.SaveRun(cfg, run); err != nil {
		return result, err
	}
	if record, found := store.FindByRunID(run.ID); found {
		record.Status = "todo"
		if err := store.Put(record); err != nil {
			return result, err
		}
	}
	if _, found, err := store.ReleaseClaimByRunID(run.ID); err != nil {
		return result, err
	} else if found {
		result.Message += "; claim liberado"
	}
	src, err := sources.NewRegistry(cfg).Open(run.Source)
	if err != nil {
		result.Message += "; aviso: fonte indisponível para devolver tarefa"
		return result, nil
	}
	if pending, ok := src.(source.PendingWriter); ok {
		if err := pending.MarkPending(ctx, run.Ref); err != nil {
			result.Message += "; aviso: falha ao devolver tarefa ao inbox"
		} else {
			result.Message += "; tarefa devolvida ao inbox"
		}
	}
	return result, nil
}

func writeMarker(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
}

type AnswerResult struct {
	RunID       string `json:"run_id"`
	Source      string `json:"source,omitempty"`
	Answered    bool   `json:"answered"`
	OperationID string `json:"operation_id,omitempty"`
	Warning     string `json:"warning,omitempty"`
	Message     string `json:"message"`
}

func Answer(ctx context.Context, cfg *config.Config, runID, text string) (AnswerResult, error) {
	if err := ValidateRunID(runID); err != nil {
		return AnswerResult{}, err
	}
	if text = strings.TrimSpace(text); text == "" {
		return AnswerResult{}, errors.New("resposta vazia")
	}
	run, err := pipeline.LoadRun(cfg, runID)
	if err != nil {
		return AnswerResult{}, err
	}
	result := AnswerResult{RunID: run.ID, Source: run.Source}
	if run.Status != "needs_input" {
		return result, fmt.Errorf("run %s não está aguardando input (status %s)", run.ID, run.Status)
	}
	src, err := sources.NewRegistry(cfg).Open(run.Source)
	if err != nil {
		return result, err
	}
	commenter, ok := src.(source.Commenter)
	if !ok {
		return result, fmt.Errorf("fonte %s não suporta respostas inline", run.Source)
	}
	body := "## Resposta do usuário\n\n" + text + "\n"
	if err := commenter.Comment(ctx, run.Ref, body); err != nil {
		return result, fmt.Errorf("registrando resposta na fonte: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.RunsDir(), run.ID), 0o755); err != nil {
		result.Warning = "resposta publicada, mas answer.md não foi gravado: " + err.Error()
	} else if err := os.WriteFile(filepath.Join(cfg.RunsDir(), run.ID, "answer.md"), []byte(body), 0o644); err != nil {
		result.Warning = "resposta publicada, mas answer.md não foi gravado: " + err.Error()
	}
	result.Answered = true
	result.Message = "resposta publicada na fonte"
	ops, err := OpenOperationStore(cfg)
	if err != nil {
		result.Warning = appendWarning(result.Warning, "fila de rerun indisponível: "+err.Error())
		return result, nil
	}
	operation, err := ops.Enqueue(ActionRerun, run.ID, map[string]any{"answer": true})
	if err != nil {
		result.Warning = appendWarning(result.Warning, "rerun não enfileirado: "+err.Error())
		return result, nil
	}
	result.OperationID = operation.ID
	result.Message += "; rerun enfileirado"
	return result, nil
}

func appendWarning(current, warning string) string {
	if current == "" {
		return warning
	}
	return current + "; " + warning
}
