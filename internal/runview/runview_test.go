package runview

import (
	"testing"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/state"
)

func TestBuildAnnotatesCurrentRuns(t *testing.T) {
	cfg := &config.Config{
		Root:          t.TempDir(),
		DefaultSource: "folder",
		Sources: map[string]config.SourceConfig{
			"folder": {Driver: "folder", Path: "./tasks"},
			"alias":  {Driver: "folder", Path: "./tasks"},
		},
	}
	store, err := state.Open(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	save := func(id, taskID, title, status, sourceName string, created time.Time) {
		t.Helper()
		run := &pipeline.RunState{
			ID:        id,
			Task:      source.Task{Source: sourceName, ExternalID: taskID, Title: title},
			Source:    sourceName,
			Status:    status,
			Stage:     "done",
			CreatedAt: created,
		}
		if err := pipeline.SaveRun(cfg, run); err != nil {
			t.Fatal(err)
		}
	}
	save("a-old", "a", "A antiga", "needs_input", "folder", now.Add(-3*time.Hour))
	save("a-new", "a", "A nova", "succeeded", "folder", now.Add(-2*time.Hour))
	save("b-current", "b", "B atual", "running", "folder", now.Add(-time.Hour))
	save("c-old", "c", "C antiga", "paused", "alias", now.Add(-4*time.Hour))
	save("c-new", "c", "C nova", "succeeded", "folder", now.Add(-3*time.Hour))
	save("d-old", "d", "D antiga", "failed", "folder", now.Add(-5*time.Hour))
	save("d-new", "d", "D nova", "failed", "folder", now.Add(-time.Hour))

	put := func(sourceName, id, status, runID string) {
		t.Helper()
		if err := store.Put(state.TaskRecord{Source: sourceName, ID: id, Status: status, RunID: runID}); err != nil {
			t.Fatal(err)
		}
	}
	put("folder", "a", "succeeded", "a-new")
	put("folder", "b", "running", "b-current")
	put("alias", "c", "needs_input", "c-old") // registro antigo de alias
	put("folder", "c", "succeeded", "c-new")
	// d fica sem estado; a run mais nova é a atual.

	entries, err := Build(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Entry{}
	for _, entry := range entries {
		byID[entry.Run.ID] = entry
	}
	if !byID["a-new"].Current || byID["a-old"].Current || byID["a-old"].SupersededBy != "a-new" {
		t.Fatalf("a: %+v %+v", byID["a-new"], byID["a-old"])
	}
	if !byID["b-current"].Current {
		t.Fatalf("b deveria ser atual: %+v", byID["b-current"])
	}
	if !byID["c-new"].Current || byID["c-old"].Current || byID["c-old"].SupersededBy != "c-new" {
		t.Fatalf("c: %+v %+v", byID["c-new"], byID["c-old"])
	}
	if !byID["d-new"].Current || byID["d-old"].Current || byID["d-old"].SupersededBy != "d-new" {
		t.Fatalf("d: %+v %+v", byID["d-new"], byID["d-old"])
	}
	if got := len(Current(entries)); got != 4 {
		t.Fatalf("current = %d, esperado 4", got)
	}
}
