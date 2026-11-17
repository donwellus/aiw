package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		var n int64
		if err2 := value.Decode(&n); err2 == nil {
			*d = Duration(time.Duration(n))
			return nil
		}
		return err
	}
	if strings.TrimSpace(s) == "" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duração inválida %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

type EngineConfig struct {
	Driver         string   `yaml:"driver"`
	Bin            string   `yaml:"bin"`
	Model          string   `yaml:"model"`
	PermissionMode string   `yaml:"permission_mode"`
	AllowedTools   []string `yaml:"allowed_tools"`
	ExtraArgs      []string `yaml:"extra_args"`
	Command        []string `yaml:"command"`
	PromptVia      string   `yaml:"prompt_via"`
	Output         string   `yaml:"output"`
	AutoApprove    bool     `yaml:"auto_approve"`
	MaxConcurrency int      `yaml:"max_concurrency"`
}

type Defaults struct {
	Engine          string   `yaml:"engine"`
	Timeout         Duration `yaml:"timeout"`
	BudgetUSD       float64  `yaml:"budget_usd"`
	MaxTestRetries  int      `yaml:"max_test_retries"`
	MaxReviewRounds int      `yaml:"max_review_rounds"`
	MaxGateRounds   int      `yaml:"max_gate_rounds"`
	Workers         int      `yaml:"workers"`
	ClaimTTL        Duration `yaml:"claim_ttl"`
}

type GateMode string

const (
	GateAuto   GateMode = "auto"
	GateHuman  GateMode = "human"
	GatePolicy GateMode = "policy"
	GateOff    GateMode = "off"
)

func (g *GateMode) UnmarshalYAML(node *yaml.Node) error {
	switch strings.ToLower(strings.TrimSpace(node.Value)) {
	case "off":
		*g = GateOff
		return nil
	case "on":
		*g = GateHuman
		return nil
	}
	var enabled bool
	if err := node.Decode(&enabled); err == nil {
		if enabled {
			*g = GateHuman
		} else {
			*g = GateAuto
		}
		return nil
	}
	var text string
	if err := node.Decode(&text); err == nil {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "auto":
			*g = GateAuto
		case "human":
			*g = GateHuman
		case "policy":
			*g = GatePolicy
		case "off":
			*g = GateOff
		default:
			return fmt.Errorf("gate inválido %q (use auto, human, policy ou off)", text)
		}
		return nil
	}
	return fmt.Errorf("valor de gate inválido")
}

type IntakeConfig struct {
	RiskLabels          []string `yaml:"risk_labels"`
	MinDescriptionChars int      `yaml:"min_description_chars"`
}

type StageConfig struct {
	Engine string `yaml:"engine"`
	Model  string `yaml:"model"`
}

type TelemetryConfig struct {
	Provider      string `yaml:"provider"`
	Host          string `yaml:"host"`
	PublicKey     string `yaml:"public_key"`
	SecretKey     string `yaml:"secret_key"`
	Capture       string `yaml:"capture"`
	FailOpen      *bool  `yaml:"fail_open"`
	RedactSecrets *bool  `yaml:"redact_secrets"`
}

func (t TelemetryConfig) FailOpenEnabled() bool {
	if t.FailOpen == nil {
		return true
	}
	return *t.FailOpen
}

func (t TelemetryConfig) RedactEnabled() bool {
	if t.RedactSecrets == nil {
		return true
	}
	return *t.RedactSecrets
}

type SourceConfig struct {
	Driver        string   `yaml:"driver"`
	Path          string   `yaml:"path"`
	DoneDir       string   `yaml:"done_dir"`
	FailedDir     string   `yaml:"failed_dir"`
	Labels        []string `yaml:"labels"`
	ExcludeLabels []string `yaml:"exclude_labels"`
	Limit         int      `yaml:"limit"`
	WriteBack     *bool    `yaml:"write_back"`
}

func (s SourceConfig) WriteBackEnabled() bool {
	if s.WriteBack == nil {
		return true
	}
	return *s.WriteBack
}

type WorkspaceConfig struct {
	DefaultRepo        string              `yaml:"default_repo"`
	RepoMap            map[string]string   `yaml:"repo_map"`
	TestCommands       []string            `yaml:"test_commands"`
	Gates              map[string]GateMode `yaml:"gates"`
	MaxTestConcurrency int                 `yaml:"max_test_concurrency"`
}

func (w WorkspaceConfig) GateModeFor(name string) GateMode {
	if w.Gates != nil {
		if mode, ok := w.Gates[name]; ok {
			return mode
		}
	}
	switch name {
	case "intake":
		return GatePolicy
	case "spec", "plan":
		return GateAuto
	default:
		return GateAuto
	}
}

type Config struct {
	Engines       map[string]EngineConfig `yaml:"engines"`
	Defaults      Defaults                `yaml:"defaults"`
	Stages        map[string]StageConfig  `yaml:"stages"`
	Telemetry     TelemetryConfig         `yaml:"telemetry"`
	Sources       map[string]SourceConfig `yaml:"sources"`
	DefaultSource string                  `yaml:"default_source"`
	Workspace     WorkspaceConfig         `yaml:"workspace"`
	Intake        IntakeConfig            `yaml:"intake"`

	Path string `yaml:"-"`
	Root string `yaml:"-"`
}

const DefaultConfigPath = ".aiw/config.yaml"

func Load(root, explicitPath string) (*Config, error) {
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = wd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	path := explicitPath
	if path == "" {
		path = filepath.Join(absRoot, DefaultConfigPath)
	}
	cfg := &Config{Path: path, Root: absRoot}

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("lendo config %s: %w", path, err)
		}
		cfg.applyDefaults()
		return cfg, nil
	}

	expanded := os.ExpandEnv(string(data))
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("config inválida em %s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Engines == nil {
		c.Engines = map[string]EngineConfig{}
	}
	if _, ok := c.Engines["claude"]; !ok {
		c.Engines["claude"] = EngineConfig{Driver: "claude", Bin: "claude"}
	}
	if _, ok := c.Engines["opencode"]; !ok {
		c.Engines["opencode"] = EngineConfig{Driver: "opencode", Bin: "opencode"}
	}
	if _, ok := c.Engines["fake"]; !ok {
		c.Engines["fake"] = EngineConfig{Driver: "fake"}
	}
	for name, ec := range c.Engines {
		if ec.Driver == "" {
			ec.Driver = name
		}
		if ec.Bin == "" {
			switch ec.Driver {
			case "claude":
				ec.Bin = "claude"
			case "opencode":
				ec.Bin = "opencode"
			}
		}
		c.Engines[name] = ec
	}

	if c.Defaults.Engine == "" {
		c.Defaults.Engine = "opencode"
	}
	if c.Defaults.Timeout == 0 {
		c.Defaults.Timeout = Duration(30 * time.Minute)
	}
	if c.Defaults.BudgetUSD == 0 {
		c.Defaults.BudgetUSD = 5
	}
	if c.Defaults.MaxTestRetries == 0 {
		c.Defaults.MaxTestRetries = 1
	}
	if c.Defaults.MaxReviewRounds == 0 {
		c.Defaults.MaxReviewRounds = 2
	}
	if c.Defaults.MaxGateRounds == 0 {
		c.Defaults.MaxGateRounds = 2
	}
	if c.Defaults.Workers == 0 {
		c.Defaults.Workers = 2
	}
	if c.Defaults.ClaimTTL == 0 {
		c.Defaults.ClaimTTL = Duration(2 * time.Hour)
	}

	if c.Telemetry.Provider == "" {
		c.Telemetry.Provider = "noop"
	}
	if c.Telemetry.Host == "" {
		c.Telemetry.Host = "http://localhost:3000"
	}
	if c.Telemetry.Capture == "" {
		c.Telemetry.Capture = "full"
	}

	if c.Sources == nil {
		c.Sources = map[string]SourceConfig{}
	}
	if _, ok := c.Sources["folder"]; !ok {
		c.Sources["folder"] = SourceConfig{Driver: "folder", Path: "./tasks", DoneDir: "./tasks/done", FailedDir: "./tasks/failed"}
	}
	for name, sc := range c.Sources {
		if sc.Driver == "" {
			sc.Driver = name
		}
		if sc.Driver == "folder" {
			if sc.Path == "" {
				sc.Path = "./tasks"
			}
			if sc.DoneDir == "" {
				sc.DoneDir = "./tasks/done"
			}
			if sc.FailedDir == "" {
				sc.FailedDir = "./tasks/failed"
			}
		}
		c.Sources[name] = sc
	}
	if c.DefaultSource == "" {
		c.DefaultSource = "folder"
	}

	if c.Stages == nil {
		c.Stages = map[string]StageConfig{}
	}
	if c.Workspace.MaxTestConcurrency == 0 {
		c.Workspace.MaxTestConcurrency = 1
	}
	if c.Intake.RiskLabels == nil {
		c.Intake.RiskLabels = []string{"risk:high", "security", "db-migration"}
	}
	if c.Intake.MinDescriptionChars == 0 {
		c.Intake.MinDescriptionChars = 10
	}
}

func (c *Config) Validate() error {
	if _, ok := c.Engines[c.Defaults.Engine]; !ok {
		return fmt.Errorf("engine padrão %q não está configurado", c.Defaults.Engine)
	}
	for stage, sc := range c.Stages {
		if sc.Engine == "" {
			continue
		}
		if _, ok := c.Engines[sc.Engine]; !ok {
			return fmt.Errorf("engine %q do estágio %q não está configurado", sc.Engine, stage)
		}
	}
	if _, ok := c.Sources[c.DefaultSource]; !ok {
		return fmt.Errorf("fonte padrão %q não está configurada", c.DefaultSource)
	}
	return nil
}

func (c *Config) DataDir() string {
	return filepath.Join(c.Root, ".aiw")
}

func (c *Config) RunsDir() string {
	return filepath.Join(c.DataDir(), "runs")
}

func (c *Config) QueueDir() string {
	return filepath.Join(c.DataDir(), "queue")
}

func (c *Config) StateDir() string {
	return filepath.Join(c.DataDir(), "state")
}

func (c *Config) WorktreesDir() string {
	return filepath.Join(c.DataDir(), "worktrees")
}

func (c *Config) SourceConfig(name string) (SourceConfig, error) {
	if name == "" {
		name = c.DefaultSource
	}
	sc, ok := c.Sources[name]
	if !ok {
		return SourceConfig{}, fmt.Errorf("fonte %q não configurada", name)
	}
	return sc, nil
}

func (c *Config) EngineConfig(name string) (EngineConfig, error) {
	if name == "" {
		name = c.Defaults.Engine
	}
	ec, ok := c.Engines[name]
	if !ok {
		return EngineConfig{}, fmt.Errorf("engine %q não configurado", name)
	}
	return ec, nil
}

func (c *Config) StageEngine(stage string) string {
	if sc, ok := c.Stages[stage]; ok && sc.Engine != "" {
		return sc.Engine
	}
	return c.Defaults.Engine
}

func (c *Config) StageModel(stage string) string {
	if sc, ok := c.Stages[stage]; ok && sc.Model != "" {
		return sc.Model
	}
	ec, err := c.EngineConfig(c.StageEngine(stage))
	if err != nil {
		return ""
	}
	return ec.Model
}

func (c *Config) ResolvePath(p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Root, p)
}

const DefaultConfigYAML = `engines:
  claude:
    driver: claude
    bin: claude
    max_concurrency: 1
    permission_mode: acceptEdits
    allowed_tools:
      - Read
      - Grep
      - Glob
      - Edit
      - Write
      - "Bash(git *)"
      - "Bash(go test *)"
  opencode:
    driver: opencode
    bin: opencode
    max_concurrency: 2
    # auto_approve: true   # apenas em worktree isolado
  fake:
    driver: fake

defaults:
  engine: opencode
  timeout: 30m
  budget_usd: 5.00
  max_test_retries: 1
  max_review_rounds: 2
  max_gate_rounds: 2
  workers: 2
  claim_ttl: 2h

stages:
  intake:
    engine: claude
  requirements:
    engine: claude
  architect:
    engine: claude
  implement:
    engine: opencode
  review:
    engine: opencode

telemetry:
  provider: langfuse
  host: ${LANGFUSE_HOST}
  public_key: ${LANGFUSE_PUBLIC_KEY}
  secret_key: ${LANGFUSE_SECRET_KEY}
  capture: full
  fail_open: true
  redact_secrets: true

sources:
  folder:
    driver: folder
    path: ./tasks
    done_dir: ./tasks/done
    failed_dir: ./tasks/failed
default_source: folder

workspace:
  default_repo: .
  test_commands: []
  max_test_concurrency: 1
  gates:
    intake: policy
    spec: auto
    plan: auto

intake:
  risk_labels: ["risk:high", "security", "db-migration"]
  min_description_chars: 10
`

func WriteDefault(root string, force bool) (string, error) {
	path := filepath.Join(root, DefaultConfigPath)
	if !force {
		if _, err := os.Stat(path); err == nil {
			return path, fmt.Errorf("config já existe em %s (use --force para sobrescrever)", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	if err := os.WriteFile(path, []byte(DefaultConfigYAML), 0o644); err != nil {
		return path, err
	}
	return path, nil
}
