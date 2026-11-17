package pipeline

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/engine"
	"github.com/donwellus/aiw/internal/source"
)

type StageState struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Engine    string    `json:"engine,omitempty"`
	Model     string    `json:"model,omitempty"`
	Error     string    `json:"error,omitempty"`
	CostUSD   float64   `json:"cost_usd,omitempty"`
	Rounds    int       `json:"rounds,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

type RunState struct {
	ID             string            `json:"id"`
	Task           source.Task       `json:"task"`
	Source         string            `json:"source"`
	Ref            source.Ref        `json:"ref"`
	Repo           string            `json:"repo"`
	Worktree       string            `json:"worktree"`
	Branch         string            `json:"branch"`
	BaseCommit     string            `json:"base_commit"`
	Status         string            `json:"status"`
	Stage          string            `json:"stage"`
	EngineOverride string            `json:"engine_override,omitempty"`
	Stages         []StageState      `json:"stages"`
	Gates          map[string]string `json:"gates"`
	Usage          engine.Usage      `json:"usage"`
	Artifacts      map[string]string `json:"artifacts"`
	PRURL          string            `json:"pr_url,omitempty"`
	Error          string            `json:"error,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

var stageOrder = []string{"intake", "requirements", "architect", "implement", "test", "review", "deliver"}

func newRunState(id string, task *source.Task, ref source.Ref, sourceName, repo string) *RunState {
	now := time.Now().UTC()
	stages := make([]StageState, 0, len(stageOrder))
	for _, name := range stageOrder {
		stages = append(stages, StageState{Name: name, Status: "pending"})
	}
	return &RunState{
		ID:        id,
		Task:      *task,
		Source:    sourceName,
		Ref:       ref,
		Repo:      repo,
		Status:    "running",
		Stages:    stages,
		Gates:     map[string]string{"spec": "pending", "plan": "pending"},
		Artifacts: map[string]string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (r *RunState) stage(name string) *StageState {
	for i := range r.Stages {
		if r.Stages[i].Name == name {
			return &r.Stages[i]
		}
	}
	return nil
}

func LoadRun(cfg *config.Config, runID string) (*RunState, error) {
	return loadRun(filepath.Join(cfg.RunsDir(), runID, "run.json"))
}

func SaveRun(cfg *config.Config, run *RunState) error {
	return saveRun(run, filepath.Join(cfg.RunsDir(), run.ID))
}

func loadRun(path string) (*RunState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var run RunState
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	if run.ID == "" {
		return nil, errors.New("run.json sem id")
	}
	return &run, nil
}

func saveRun(run *RunState, dir string) error {
	run.UpdatedAt = time.Now().UTC()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "run-*.json")
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
	return os.Rename(tmpName, filepath.Join(dir, "run.json"))
}

func appendEvent(dir, name string, meta map[string]any) {
	entry := map[string]any{
		"time": time.Now().UTC().Format(time.RFC3339Nano),
		"name": name,
	}
	for k, v := range meta {
		entry[k] = v
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(data, '\n'))
}
