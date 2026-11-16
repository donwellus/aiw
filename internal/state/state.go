package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type TaskRecord struct {
	Source      string    `json:"source"`
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	RunID       string    `json:"run_id,omitempty"`
	ContentHash string    `json:"content_hash,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Store struct {
	path    string
	mu      sync.Mutex
	records map[string]TaskRecord
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		path:    filepath.Join(dataDir, "tasks.json"),
		records: map[string]TaskRecord{},
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s.records); err != nil {
		return nil, err
	}
	return s, nil
}

func key(source, id string) string {
	return source + ":" + id
}

func (s *Store) Get(source, id string) (TaskRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[key(source, id)]
	return rec, ok
}

func (s *Store) Put(record TaskRecord) error {
	record.UpdatedAt = time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[key(record.Source, record.ID)] = record
	return s.saveLocked()
}

func (s *Store) All() []TaskRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TaskRecord, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec)
	}
	return out
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "tasks-*.json")
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
	return os.Rename(tmpName, s.path)
}
