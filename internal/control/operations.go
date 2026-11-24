package control

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"

	ActionResume    = "resume"
	ActionResumeAll = "resume_all"
	ActionRerun     = "rerun"
	ActionRefine    = "refine"
)

type Operation struct {
	ID          string         `json:"id"`
	Action      string         `json:"action"`
	RunID       string         `json:"run_id,omitempty"`
	Status      string         `json:"status"`
	Args        map[string]any `json:"args,omitempty"`
	RequestedAt time.Time      `json:"requested_at"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	FinishedAt  *time.Time     `json:"finished_at,omitempty"`
	Result      map[string]any `json:"result,omitempty"`
	Error       string         `json:"error,omitempty"`
}

type OperationStore struct {
	dir string
}

func OpenOperationStore(cfg *config.Config) (*OperationStore, error) {
	dir := filepath.Join(cfg.DataDir(), "control", "operations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &OperationStore{dir: dir}, nil
}

func (s *OperationStore) Dir() string {
	return s.dir
}

func (s *OperationStore) Enqueue(action, runID string, args map[string]any) (*Operation, error) {
	if action != ActionResume && action != ActionResumeAll && action != ActionRerun && action != ActionRefine {
		return nil, fmt.Errorf("ação de operação desconhecida: %s", action)
	}
	operation := &Operation{
		ID:          newOperationID(),
		Action:      action,
		RunID:       runID,
		Status:      StatusQueued,
		Args:        args,
		RequestedAt: time.Now().UTC(),
	}
	if err := s.write(operation); err != nil {
		return nil, err
	}
	return operation, nil
}

func (s *OperationStore) Get(id string) (*Operation, error) {
	if !ValidOperationID(id) {
		return nil, errors.New("operation_id inválido")
	}
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var operation Operation
	if err := json.Unmarshal(data, &operation); err != nil {
		return nil, err
	}
	return &operation, nil
}

func (s *OperationStore) List(limit int) ([]Operation, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	operations := make([]Operation, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !ValidOperationID(id) {
			continue
		}
		operation, err := s.Get(id)
		if err != nil {
			continue
		}
		operations = append(operations, *operation)
	}
	sort.Slice(operations, func(i, j int) bool {
		if !operations[i].RequestedAt.Equal(operations[j].RequestedAt) {
			return operations[i].RequestedAt.After(operations[j].RequestedAt)
		}
		return operations[i].ID > operations[j].ID
	})
	if limit > 0 && len(operations) > limit {
		operations = operations[:limit]
	}
	return operations, nil
}

func (s *OperationStore) ClaimNext() (*Operation, error) {
	operations, err := s.List(0)
	if err != nil {
		return nil, err
	}
	// List is newest-first; claim the oldest queued operation.
	for i := len(operations) - 1; i >= 0; i-- {
		if operations[i].Status != StatusQueued {
			continue
		}
		claimPath := s.claimPath(operations[i].ID)
		file, err := os.OpenFile(claimPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, err
		}
		_ = file.Close()
		operation, err := s.Get(operations[i].ID)
		if err != nil || operation.Status != StatusQueued {
			_ = os.Remove(claimPath)
			continue
		}
		now := time.Now().UTC()
		operation.Status = StatusRunning
		operation.StartedAt = &now
		if err := s.write(operation); err != nil {
			_ = os.Remove(claimPath)
			return nil, err
		}
		return operation, nil
	}
	return nil, nil
}

func (s *OperationStore) Finish(id, status string, result map[string]any, errMessage string) error {
	operation, err := s.Get(id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	operation.Status = status
	operation.FinishedAt = &now
	operation.Result = result
	operation.Error = errMessage
	if err := s.write(operation); err != nil {
		return err
	}
	_ = os.Remove(s.claimPath(id))
	_ = os.Remove(s.cancelPath(id))
	return nil
}

func (s *OperationStore) Cancel(id string) (*Operation, error) {
	if !ValidOperationID(id) {
		return nil, errors.New("operation_id inválido")
	}
	operation, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	switch operation.Status {
	case StatusQueued:
		claimPath := s.claimPath(id)
		file, claimErr := os.OpenFile(claimPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if claimErr == nil {
			_ = file.Close()
			now := time.Now().UTC()
			operation.Status = StatusCanceled
			operation.FinishedAt = &now
			if err := s.write(operation); err != nil {
				_ = os.Remove(claimPath)
				return nil, err
			}
			_ = os.Remove(claimPath)
			return operation, nil
		}
		if !errors.Is(claimErr, os.ErrExist) {
			return nil, claimErr
		}
		if err := s.writeCancelMarker(id); err != nil {
			return nil, err
		}
		return operation, nil
	case StatusRunning:
		if err := s.writeCancelMarker(id); err != nil {
			return nil, err
		}
		return operation, nil
	case StatusSucceeded, StatusFailed, StatusCanceled:
		return operation, fmt.Errorf("operação %s já finalizada com status %s", id, operation.Status)
	default:
		return nil, fmt.Errorf("status de operação desconhecido: %s", operation.Status)
	}
}

func (s *OperationStore) CancelRequested(id string) bool {
	_, err := os.Stat(s.cancelPath(id))
	return err == nil
}

func (s *OperationStore) write(operation *Operation) error {
	data, err := json.MarshalIndent(operation, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.path(operation.ID), append(data, '\n'))
}

func (s *OperationStore) writeCancelMarker(id string) error {
	return os.WriteFile(s.cancelPath(id), []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
}

func (s *OperationStore) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *OperationStore) claimPath(id string) string {
	return filepath.Join(s.dir, id+".claim")
}

func (s *OperationStore) cancelPath(id string) string {
	return filepath.Join(s.dir, id+".cancel")
}

func ValidOperationID(id string) bool {
	if !strings.HasPrefix(id, "op-") || len(id) < 4 {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func newOperationID() string {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		return "op-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "op-" + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(buffer)
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".op-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
