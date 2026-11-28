package opencode

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const CoordinatorVersion = "1"

//go:embed templates/agents/aiw-coordinator.md
var coordinatorTemplate []byte

//go:embed templates/opencode.jsonc
var configTemplate string

type InstallOptions struct {
	Root   string
	Global bool
	Force  bool
	Binary string
}

type InstallResult struct {
	AgentPath      string `json:"agent_path"`
	ConfigPath     string `json:"config_path"`
	AgentChanged   bool   `json:"agent_changed"`
	ConfigChanged  bool   `json:"config_changed"`
	ConfigSkipped  bool   `json:"config_skipped,omitempty"`
	AlreadyCurrent bool   `json:"already_current,omitempty"`
	Message        string `json:"message,omitempty"`
}

type AgentStatus struct {
	Path      string
	Installed bool
	Version   string
	Current   bool
}

func Install(opts InstallOptions) (InstallResult, error) {
	baseDir, configPath, err := paths(opts.Root, opts.Global)
	if err != nil {
		return InstallResult{}, err
	}
	binary := strings.TrimSpace(opts.Binary)
	if binary == "" {
		binary, err = os.Executable()
		if err != nil || strings.TrimSpace(binary) == "" {
			binary = "aiw"
		}
	}
	result := InstallResult{}
	agentPath := filepath.Join(baseDir, "agents", "aiw-coordinator.md")
	result.AgentPath = agentPath
	result.ConfigPath = configPath
	if err := os.MkdirAll(filepath.Dir(agentPath), 0o755); err != nil {
		return result, err
	}
	existing, readErr := os.ReadFile(agentPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return result, readErr
	}
	if readErr == nil && !bytes.Equal(existing, coordinatorTemplate) {
		if !opts.Force {
			return result, fmt.Errorf("coordinator já existe em %s com conteúdo diferente; use --force para substituir", agentPath)
		}
	}
	if readErr != nil || !bytes.Equal(existing, coordinatorTemplate) {
		if err := os.WriteFile(agentPath, coordinatorTemplate, 0o644); err != nil {
			return result, err
		}
		result.AgentChanged = true
	}
	config, err := renderConfig(binary)
	if err != nil {
		return result, err
	}
	existingConfig, configErr := os.ReadFile(configPath)
	if configErr != nil && !os.IsNotExist(configErr) {
		return result, configErr
	}
	switch {
	case configErr != nil || opts.Force:
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			return result, err
		}
		if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
			return result, err
		}
		result.ConfigChanged = true
	case bytes.Equal(existingConfig, []byte(config)):
		result.AlreadyCurrent = true
	default:
		result.ConfigSkipped = true
		result.Message = "opencode.jsonc existente preservado; use --force para substituir"
	}
	if result.Message == "" {
		result.Message = "coordinator instalado"
	}
	return result, nil
}

func Inspect(root string) (AgentStatus, error) {
	baseDir, _, err := paths(root, false)
	if err != nil {
		return AgentStatus{}, err
	}
	path := filepath.Join(baseDir, "agents", "aiw-coordinator.md")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return AgentStatus{Path: path}, nil
	}
	if err != nil {
		return AgentStatus{}, err
	}
	version := commentVersion(string(data))
	return AgentStatus{
		Path:      path,
		Installed: true,
		Version:   version,
		Current:   bytes.Equal(data, coordinatorTemplate) && version == CoordinatorVersion,
	}, nil
}

func renderConfig(binary string) (string, error) {
	encoded, err := json.Marshal(binary)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(configTemplate, "{{AIW_BIN_JSON}}", string(encoded)), nil
}

func paths(root string, global bool) (string, string, error) {
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		base := filepath.Join(home, ".config", "opencode")
		return base, filepath.Join(base, "opencode.jsonc"), nil
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(abs, ".opencode"), filepath.Join(abs, "opencode.jsonc"), nil
}

func commentVersion(content string) string {
	marker := "<!-- aiw-coordinator: v"
	index := strings.Index(content, marker)
	if index < 0 {
		return ""
	}
	rest := content[index+len(marker):]
	end := strings.Index(rest, " -->")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}
