package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCreatesAgentAndConfig(t *testing.T) {
	root := t.TempDir()
	result, err := Install(InstallOptions{Root: root, Binary: "/usr/local/bin/aiw"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AgentChanged || !result.ConfigChanged {
		t.Fatalf("result = %+v", result)
	}
	agent, err := os.ReadFile(result.AgentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agent), "<!-- aiw-coordinator: v"+CoordinatorVersion+" -->") {
		t.Fatal("marcador de versão ausente")
	}
	config, err := os.ReadFile(result.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "/usr/local/bin/aiw") {
		t.Fatalf("config sem binário: %s", config)
	}
	var parsed map[string]any
	if err := json.Unmarshal(config, &parsed); err != nil {
		t.Fatalf("config não é JSON válido: %v", err)
	}
	status, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.Current || status.Version != CoordinatorVersion {
		t.Fatalf("status = %+v", status)
	}
}

func TestInstallDoesNotOverwriteCustomAgentWithoutForce(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".opencode", "agents", "aiw-coordinator.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(InstallOptions{Root: root, Binary: "aiw"}); err == nil {
		t.Fatal("instalação deveria falhar sem --force")
	}
	if _, err := Install(InstallOptions{Root: root, Binary: "aiw", Force: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "custom" {
		t.Fatal("agente customizado não foi substituído com --force")
	}
}

func TestInstallPreservesExistingConfigWithoutForce(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "opencode.jsonc")
	if err := os.WriteFile(configPath, []byte(`{"custom":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Install(InstallOptions{Root: root, Binary: "aiw"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ConfigSkipped || result.ConfigChanged {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"custom":true}` {
		t.Fatalf("config alterada: %s", data)
	}
}

func TestInstallGlobalUsesHomeConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	result, err := Install(InstallOptions{Global: true, Binary: "aiw"})
	if err != nil {
		t.Fatal(err)
	}
	wantBase := filepath.Join(home, ".config", "opencode")
	if result.AgentPath != filepath.Join(wantBase, "agents", "aiw-coordinator.md") {
		t.Fatalf("agent = %s", result.AgentPath)
	}
	if result.ConfigPath != filepath.Join(wantBase, "opencode.jsonc") {
		t.Fatalf("config = %s", result.ConfigPath)
	}
}
