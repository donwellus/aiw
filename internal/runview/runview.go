package runview

import (
	"errors"
	"os"
	"sort"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/state"
	"github.com/donwellus/aiw/internal/taskkey"
)

type Entry struct {
	Run          *pipeline.RunState
	Current      bool
	SupersededBy string
}

func Load(cfg *config.Config) ([]*pipeline.RunState, error) {
	entries, err := os.ReadDir(cfg.RunsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	runs := make([]*pipeline.RunState, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		run, err := pipeline.LoadRun(cfg, entry.Name())
		if err != nil {
			continue
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func Build(cfg *config.Config, store *state.Store) ([]Entry, error) {
	runs, err := Load(cfg)
	if err != nil {
		return nil, err
	}
	return Annotate(cfg, store, runs), nil
}

func Annotate(cfg *config.Config, store *state.Store, runs []*pipeline.RunState) []Entry {
	keys := taskkey.New(cfg)
	records := map[string]state.TaskRecord{}
	for _, record := range store.All() {
		key := keys.Key(record.Source, record.ID, record.Key)
		current, ok := records[key]
		if !ok || record.UpdatedAt.After(current.UpdatedAt) {
			records[key] = record
		}
	}
	byKey := map[string][]*pipeline.RunState{}
	for _, run := range runs {
		key := runKey(run, keys)
		byKey[key] = append(byKey[key], run)
	}
	latest := map[string]*pipeline.RunState{}
	for key, list := range byKey {
		sort.Slice(list, func(i, j int) bool {
			left := runTime(list[i])
			right := runTime(list[j])
			if !left.Equal(right) {
				return left.After(right)
			}
			return list[i].ID > list[j].ID
		})
		latest[key] = list[0]
	}
	entries := make([]Entry, 0, len(runs))
	for _, run := range runs {
		key := runKey(run, keys)
		currentID := ""
		record, hasRecord := records[key]
		pending := hasRecord && (record.Status == "queued" || record.Status == "todo")
		if hasRecord && !pending && record.RunID != "" {
			currentID = record.RunID
		}
		if !pending && (currentID == "" || !hasRun(byKey[key], currentID)) {
			if latestRun := latest[key]; latestRun != nil {
				currentID = latestRun.ID
			}
		}
		entry := Entry{Run: run, Current: run.ID == currentID}
		if !entry.Current && currentID != "" {
			entry.SupersededBy = currentID
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		left := runTime(entries[i].Run)
		right := runTime(entries[j].Run)
		if !left.Equal(right) {
			return left.After(right)
		}
		return entries[i].Run.ID > entries[j].Run.ID
	})
	return entries
}

func hasRun(runs []*pipeline.RunState, runID string) bool {
	for _, run := range runs {
		if run.ID == runID {
			return true
		}
	}
	return false
}

func Current(entries []Entry) []Entry {
	current := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Current {
			current = append(current, entry)
		}
	}
	return current
}

func runKey(run *pipeline.RunState, keys *taskkey.Keyer) string {
	sourceName := run.Task.Source
	if sourceName == "" {
		sourceName = run.Source
	}
	claimKey := run.Task.ClaimKey
	if claimKey == "" {
		claimKey = run.Ref.ClaimKey
	}
	return keys.Key(sourceName, run.Task.ExternalID, claimKey)
}

func runTime(run *pipeline.RunState) time.Time {
	if run == nil {
		return time.Time{}
	}
	if !run.CreatedAt.IsZero() {
		return run.CreatedAt
	}
	return run.UpdatedAt
}
