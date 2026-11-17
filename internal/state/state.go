package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

type ClaimInfo struct {
	Source    string    `json:"source"`
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	PID       int       `json:"pid"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	stateDir  string
	tasksDir  string
	claimsDir string
}

func Open(stateDir string) (*Store, error) {
	store := &Store{
		stateDir:  stateDir,
		tasksDir:  filepath.Join(stateDir, "tasks"),
		claimsDir: filepath.Join(stateDir, "claims"),
	}
	if err := os.MkdirAll(store.tasksDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(store.claimsDir, 0o755); err != nil {
		return nil, err
	}
	if err := store.migrateLegacy(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Get(source, id string) (TaskRecord, bool) {
	data, err := os.ReadFile(filepath.Join(s.tasksDir, taskFileName(source, id)))
	if err != nil {
		return TaskRecord{}, false
	}
	var record TaskRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return TaskRecord{}, false
	}
	if record.Source == "" {
		record.Source = source
	}
	if record.ID == "" {
		record.ID = id
	}
	return record, true
}

func (s *Store) Put(record TaskRecord) error {
	if record.Source == "" || record.ID == "" {
		return errors.New("registro de tarefa sem source ou id")
	}
	record.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.tasksDir, taskFileName(record.Source, record.ID)), data)
}

func (s *Store) All() []TaskRecord {
	entries, err := os.ReadDir(s.tasksDir)
	if err != nil {
		return nil
	}
	records := make([]TaskRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.tasksDir, entry.Name()))
		if err != nil {
			continue
		}
		var record TaskRecord
		if err := json.Unmarshal(data, &record); err != nil {
			continue
		}
		if record.Source == "" || record.ID == "" {
			continue
		}
		records = append(records, record)
	}
	sortRecords(records)
	return records
}

func (s *Store) FindByRunID(runID string) (TaskRecord, bool) {
	if runID == "" {
		return TaskRecord{}, false
	}
	for _, record := range s.All() {
		if record.RunID == runID {
			return record, true
		}
	}
	return TaskRecord{}, false
}

type Claim struct {
	store    *Store
	info     ClaimInfo
	path     string
	mu       sync.Mutex
	released bool
}

func (s *Store) Claim(source, id, runID string, ttl time.Duration) (*Claim, bool, error) {
	path := filepath.Join(s.claimsDir, claimFileName(source, id))
	info := ClaimInfo{
		Source:    source,
		ID:        id,
		RunID:     runID,
		PID:       os.Getpid(),
		CreatedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(info)
	if err != nil {
		return nil, false, err
	}

	tryCreate := func() (*Claim, bool, error) {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return nil, false, nil
			}
			return nil, false, err
		}
		if _, writeErr := file.Write(data); writeErr != nil {
			file.Close()
			os.Remove(path)
			return nil, false, writeErr
		}
		if closeErr := file.Close(); closeErr != nil {
			os.Remove(path)
			return nil, false, closeErr
		}
		return &Claim{store: s, info: info, path: path}, true, nil
	}

	for attempt := 0; attempt < 2; attempt++ {
		claim, acquired, err := tryCreate()
		if err != nil || acquired {
			return claim, acquired, err
		}
		stat, statErr := os.Stat(path)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			return nil, false, statErr
		}
		if ttl <= 0 || time.Since(stat.ModTime()) <= ttl {
			return nil, false, nil
		}

		lockDir := filepath.Join(s.claimsDir, ".takeover.lock")
		if err := acquireDirLock(lockDir, 2*time.Second); err != nil {
			return nil, false, err
		}
		stat, statErr = os.Stat(path)
		if statErr != nil {
			os.Remove(lockDir)
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			return nil, false, statErr
		}
		if time.Since(stat.ModTime()) <= ttl {
			os.Remove(lockDir)
			return nil, false, nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			os.Remove(lockDir)
			return nil, false, err
		}
		claim, acquired, err = tryCreate()
		os.Remove(lockDir)
		return claim, acquired, err
	}
	return nil, false, nil
}

func acquireDirLock(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := os.Mkdir(path, 0o700); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > 10*time.Second {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout aguardando lock de claim")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (c *Claim) Info() ClaimInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

func (c *Claim) KeepAlive(interval time.Duration) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				info, err := readClaim(c.path)
				if err != nil || info.RunID != c.info.RunID {
					return
				}
				now := time.Now()
				_ = os.Chtimes(c.path, now, now)
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}
}

func (c *Claim) Release() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.released {
		return nil
	}
	c.released = true
	if err := os.Remove(c.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) ClaimInfo(source, id string) (ClaimInfo, bool) {
	info, err := readClaim(filepath.Join(s.claimsDir, claimFileName(source, id)))
	if err != nil {
		return ClaimInfo{}, false
	}
	return info, true
}

func (s *Store) ReleaseClaim(source, id string) error {
	err := os.Remove(filepath.Join(s.claimsDir, claimFileName(source, id)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) ReleaseClaimByRunID(runID string) (ClaimInfo, bool, error) {
	if runID == "" {
		return ClaimInfo{}, false, nil
	}
	entries, err := os.ReadDir(s.claimsDir)
	if err != nil {
		return ClaimInfo{}, false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		path := filepath.Join(s.claimsDir, entry.Name())
		info, err := readClaim(path)
		if err != nil || info.RunID != runID {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return info, true, err
		}
		return info, true, nil
	}
	return ClaimInfo{}, false, nil
}

func (s *Store) FindClaimByRunID(runID string) (ClaimInfo, bool) {
	if runID == "" {
		return ClaimInfo{}, false
	}
	entries, err := os.ReadDir(s.claimsDir)
	if err != nil {
		return ClaimInfo{}, false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		info, err := readClaim(filepath.Join(s.claimsDir, entry.Name()))
		if err != nil {
			continue
		}
		if info.RunID == runID {
			return info, true
		}
	}
	return ClaimInfo{}, false
}

func (s *Store) Claims() []ClaimInfo {
	entries, err := os.ReadDir(s.claimsDir)
	if err != nil {
		return nil
	}
	claims := make([]ClaimInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		info, err := readClaim(filepath.Join(s.claimsDir, entry.Name()))
		if err != nil {
			continue
		}
		claims = append(claims, info)
	}
	return claims
}

func (s *Store) migrateLegacy() error {
	legacyPath := filepath.Join(s.stateDir, "tasks.json")
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return os.Rename(legacyPath, legacyPath+".migrated")
	}
	var records map[string]TaskRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("migrando %s: %w", legacyPath, err)
	}
	for key, record := range records {
		if record.Source == "" || record.ID == "" {
			parts := strings.SplitN(key, ":", 2)
			if len(parts) == 2 {
				record.Source = parts[0]
				record.ID = parts[1]
			}
		}
		if record.Source == "" || record.ID == "" {
			continue
		}
		if err := s.Put(record); err != nil {
			return err
		}
	}
	backup := legacyPath + ".migrated"
	if _, err := os.Stat(backup); err == nil {
		backup = fmt.Sprintf("%s.migrated-%d", backup, time.Now().Unix())
	}
	if err := os.Rename(legacyPath, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func readClaim(path string) (ClaimInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ClaimInfo{}, err
	}
	var info ClaimInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return ClaimInfo{}, err
	}
	return info, nil
}

func taskFileName(source, id string) string {
	return keyPrefix(source, id) + ".json"
}

func claimFileName(source, id string) string {
	return keyPrefix(source, id) + ".lock"
}

func keyPrefix(source, id string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + id))
	return sanitize(source) + "-" + sanitize(id) + "-" + hex.EncodeToString(sum[:4])
}

func sanitize(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.':
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
		if builder.Len() >= 40 {
			break
		}
	}
	out := strings.Trim(builder.String(), "-.")
	if out == "" {
		return "task"
	}
	return out
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
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

func sortRecords(records []TaskRecord) {
	for i := 1; i < len(records); i++ {
		for j := i; j > 0; j-- {
			left, right := records[j-1], records[j]
			if left.Source < right.Source || (left.Source == right.Source && left.ID <= right.ID) {
				break
			}
			records[j-1], records[j] = records[j], records[j-1]
		}
	}
}
