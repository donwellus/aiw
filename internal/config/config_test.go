package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Defaults.Engine != "opencode" {
		t.Fatalf("engine padrão = %q", cfg.Defaults.Engine)
	}
	if cfg.Defaults.Timeout.Std() != 30*time.Minute {
		t.Fatalf("timeout = %v", cfg.Defaults.Timeout.Std())
	}
	if cfg.Telemetry.Provider != "noop" {
		t.Fatalf("telemetria = %q", cfg.Telemetry.Provider)
	}
	if cfg.Defaults.Workers != 2 {
		t.Fatalf("workers = %d", cfg.Defaults.Workers)
	}
	if cfg.Defaults.ClaimTTL.Std() != 2*time.Hour {
		t.Fatalf("claim_ttl = %v", cfg.Defaults.ClaimTTL.Std())
	}
	if cfg.Workspace.MaxTestConcurrency != 1 {
		t.Fatalf("max_test_concurrency = %d", cfg.Workspace.MaxTestConcurrency)
	}
	if cfg.DefaultSource != "folder" {
		t.Fatalf("fonte padrão = %q", cfg.DefaultSource)
	}
	if !cfg.Workspace.GateEnabled("spec") {
		t.Fatal("gate spec deveria estar habilitado por padrão")
	}
}

func TestLoadExpandsEnvAndDurations(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AIW_TEST_ENGINE", "fake")
	t.Setenv("AIW_TEST_HOST", "http://example.test")
	yaml := `engines:
  fake:
    driver: fake
defaults:
  engine: ${AIW_TEST_ENGINE}
  timeout: 5m
  budget_usd: 2.5
telemetry:
  provider: langfuse
  host: ${AIW_TEST_HOST}
  public_key: pk
  secret_key: sk
sources:
  folder:
    driver: folder
default_source: folder
workspace:
  gates:
    spec: false
`
	path := filepath.Join(root, DefaultConfigPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Defaults.Engine != "fake" {
		t.Fatalf("engine = %q", cfg.Defaults.Engine)
	}
	if cfg.Defaults.Timeout.Std() != 5*time.Minute {
		t.Fatalf("timeout = %v", cfg.Defaults.Timeout.Std())
	}
	if cfg.Telemetry.Host != "http://example.test" {
		t.Fatalf("host = %q", cfg.Telemetry.Host)
	}
	if cfg.Workspace.GateEnabled("spec") {
		t.Fatal("gate spec deveria estar desabilitado")
	}
	if !cfg.Workspace.GateEnabled("plan") {
		t.Fatal("gate plan deveria continuar habilitado")
	}
}

func TestLoadInvalidStageEngine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DefaultConfigPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `engines:
  fake:
    driver: fake
defaults:
  engine: fake
stages:
  review:
    engine: inexistente
sources:
  folder:
    driver: folder
default_source: folder
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, ""); err == nil {
		t.Fatal("esperava erro de validação")
	}
}

func TestWriteDefault(t *testing.T) {
	root := t.TempDir()
	path, err := WriteDefault(root, false)
	if err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	if _, err := Load(root, ""); err != nil {
		t.Fatalf("Load após WriteDefault: %v", err)
	}
	if _, err := WriteDefault(root, false); err == nil {
		t.Fatal("esperava erro ao sobrescrever sem force")
	}
	if _, err := WriteDefault(root, true); err != nil {
		t.Fatalf("WriteDefault force: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config não existe: %v", err)
	}
}
