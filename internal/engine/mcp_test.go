package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/donwellus/aiw/internal/config"
)

func TestMCPConfigFiles(t *testing.T) {
	dir := t.TempDir()
	ec := config.EngineConfig{
		Driver: "claude",
		MCP: map[string]config.MCPServerConfig{
			"linear": {
				URL:     "https://mcp.linear.app/mcp/readonly",
				Headers: map[string]string{"Authorization": "Bearer secret"},
			},
		},
	}
	path, names, err := writeClaudeMCP(dir, "claude", ec)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "linear" {
		t.Fatalf("names = %v", names)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var claude struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &claude); err != nil {
		t.Fatal(err)
	}
	if claude.Servers["linear"].Type != "http" || claude.Servers["linear"].URL == "" {
		t.Fatalf("config claude inesperada: %s", data)
	}
	if claude.Servers["linear"].Headers["Authorization"] != "Bearer secret" {
		t.Fatalf("header ausente: %s", data)
	}

	openPath, err := writeOpencodeMCP(dir, "opencode", ec)
	if err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(openPath)
	if err != nil {
		t.Fatal(err)
	}
	var open struct {
		MCP struct {
			Servers map[string]struct {
				Type  string `json:"type"`
				URL   string `json:"url"`
				OAuth bool   `json:"oauth"`
			} `json:"servers"`
		} `json:"mcp"`
		Permissions []struct {
			Action string `json:"action"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &open); err != nil {
		t.Fatal(err)
	}
	server := open.MCP.Servers["linear"]
	if server.Type != "remote" || server.URL == "" || server.OAuth {
		t.Fatalf("config opencode inesperada: %s", data)
	}
	if len(open.Permissions) != 1 || open.Permissions[0].Action != "linear_*" {
		t.Fatalf("permissões inesperadas: %s", data)
	}
}

func TestMCPConfigSkipsEmptyAuth(t *testing.T) {
	dir := t.TempDir()
	ec := config.EngineConfig{
		Driver: "claude",
		MCP: map[string]config.MCPServerConfig{
			"linear": {
				URL:     "https://mcp.linear.app/mcp/readonly",
				Headers: map[string]string{"Authorization": "Bearer "},
			},
		},
	}
	path, names, err := writeClaudeMCP(dir, "claude", ec)
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || len(names) != 0 {
		t.Fatalf("MCP sem token deveria ser ignorado: path=%q names=%v", path, names)
	}
	if _, err := os.Stat(filepath.Join(dir, "mcp")); !os.IsNotExist(err) {
		t.Fatal("diretório MCP não deveria ter sido criado")
	}
}
