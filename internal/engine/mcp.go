package engine

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/donwellus/aiw/internal/config"
)

type claudeMCPFile struct {
	Servers map[string]claudeMCPServer `json:"mcpServers"`
}

type claudeMCPServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

type opencodeMCPFile struct {
	MCP struct {
		Servers map[string]opencodeMCPServer `json:"servers"`
	} `json:"mcp"`
	Permissions []opencodePermission `json:"permissions,omitempty"`
}

type opencodeMCPServer struct {
	Type     string            `json:"type"`
	URL      string            `json:"url"`
	OAuth    bool              `json:"oauth"`
	Codemode bool              `json:"codemode"`
	Headers  map[string]string `json:"headers,omitempty"`
}

type opencodePermission struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

func activeMCPServers(ec config.EngineConfig) map[string]config.MCPServerConfig {
	servers := map[string]config.MCPServerConfig{}
	for name, server := range ec.MCP {
		if server.Enabled != nil && !*server.Enabled {
			continue
		}
		if strings.TrimSpace(server.URL) == "" {
			continue
		}
		if !validMCPHeaders(server.Headers) {
			log.Printf("aviso: MCP %q ignorado: header de autenticação vazio", name)
			continue
		}
		servers[name] = server
	}
	return servers
}

func validMCPHeaders(headers map[string]string) bool {
	for key, value := range headers {
		trimmed := strings.TrimSpace(value)
		if strings.EqualFold(trimmed, "Bearer") && strings.Contains(strings.ToLower(key), "authorization") {
			return false
		}
		if trimmed == "" {
			return false
		}
	}
	return true
}

func mcpNames(servers map[string]config.MCPServerConfig) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func writeClaudeMCP(dataDir, engineName string, ec config.EngineConfig) (string, []string, error) {
	servers := activeMCPServers(ec)
	if len(servers) == 0 {
		return "", nil, nil
	}
	file := claudeMCPFile{Servers: map[string]claudeMCPServer{}}
	for name, server := range servers {
		file.Servers[name] = claudeMCPServer{Type: "http", URL: server.URL, Headers: server.Headers}
	}
	path, err := writeMCPFile(dataDir, engineName+"-claude.json", file)
	return path, mcpNames(servers), err
}

func writeOpencodeMCP(dataDir, engineName string, ec config.EngineConfig) (string, error) {
	servers := activeMCPServers(ec)
	if len(servers) == 0 {
		return "", nil
	}
	file := opencodeMCPFile{}
	file.MCP.Servers = map[string]opencodeMCPServer{}
	for name, server := range servers {
		file.MCP.Servers[name] = opencodeMCPServer{
			Type:     "remote",
			URL:      server.URL,
			OAuth:    false,
			Codemode: false,
			Headers:  server.Headers,
		}
		file.Permissions = append(file.Permissions, opencodePermission{
			Action:   name + "_*",
			Resource: "*",
			Effect:   "allow",
		})
	}
	return writeMCPFile(dataDir, engineName+"-opencode.json", file)
}

func writeMCPFile(dataDir, name string, value any) (string, error) {
	dir := filepath.Join(dataDir, "mcp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("gravando config MCP: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
