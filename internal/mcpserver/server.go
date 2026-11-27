package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/state"
)

const protocolVersion = "2025-06-18"

var supportedVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

type Server struct {
	cfg        *config.Config
	store      *state.Store
	version    string
	allowWrite bool
	in         io.Reader
	out        io.Writer
	ready      bool
}

func New(cfg *config.Config, store *state.Store, version string, allowWrite bool, in io.Reader, out io.Writer) *Server {
	return &Server{cfg: cfg, store: store, version: version, allowWrite: allowWrite, in: in, out: out}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (s *Server) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(s.in)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	writer := bufio.NewWriter(s.out)
	defer writer.Flush()
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := writeResponse(writer, response{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "parse error", Data: err.Error()},
			}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			s.handleNotification(req)
			continue
		}
		resp := s.handle(ctx, req)
		if err := writeResponse(writer, resp); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Server) handleNotification(req request) {
	if req.Method == "notifications/initialized" {
		s.ready = true
	}
}

func (s *Server) handle(ctx context.Context, req request) response {
	resp := response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		result, err := s.initialize(req.Params)
		if err != nil {
			resp.Error = invalidParams(err)
		} else {
			resp.Result = result
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": toolDefinitions(s.allowWrite)}
	case "tools/call":
		result, err := s.callTool(ctx, req.Params)
		if err != nil {
			resp.Error = err
		} else {
			resp.Result = result
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found", Data: req.Method}
	}
	return resp
}

func (s *Server) initialize(raw json.RawMessage) (map[string]any, error) {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
	}
	version := params.ProtocolVersion
	if !supportedVersions[version] {
		version = protocolVersion
	}
	title := "aiw control plane (read-only)"
	instructions := "Control plane read-only do aiw. Use aiw_status, aiw_report, aiw_runs, aiw_run_show e aiw_events para inspecionar o estado. Não há ferramentas de escrita nesta versão."
	if s.allowWrite {
		title = "aiw control plane"
		instructions = "Control plane do aiw com leitura e controle. Use pause, resume, approve, release, clean e operations. Resume é assíncrono: acompanhe pelo operation_id. Para criar issues, use sources, prepare_issue e só então create_issue com o preview_id; campos faltantes exigem allow_incomplete e acknowledged_missing. Para refinar, use refine_preview, mostre o preview ao humano e só enfileire refine com o preview_id após confirmação explícita. Peça confirmação antes de ações destrutivas."
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "aiw",
			"title":   title,
			"version": s.version,
		},
		"instructions": instructions,
	}, nil
}

func writeResponse(writer *bufio.Writer, resp response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	if bytes.Contains(data, []byte("\n")) {
		return errors.New("resposta MCP contém newline")
	}
	if _, err := writer.Write(append(data, '\n')); err != nil {
		return err
	}
	return writer.Flush()
}

func invalidParams(err error) *rpcError {
	return &rpcError{Code: -32602, Message: "invalid params", Data: err.Error()}
}

func toolError(message string) map[string]any {
	return callResult{IsError: true, Content: []content{{Type: "text", Text: message}}}.toMap()
}

func toolResult(value any) (map[string]any, error) {
	text, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return callResult{
		Content:           []content{{Type: "text", Text: string(text)}},
		StructuredContent: value,
	}.toMap(), nil
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content           []content `json:"content"`
	StructuredContent any       `json:"structuredContent,omitempty"`
	IsError           bool      `json:"isError,omitempty"`
}

func (r callResult) toMap() map[string]any {
	result := map[string]any{
		"content": r.Content,
		"isError": r.IsError,
	}
	if r.StructuredContent != nil {
		result["structuredContent"] = r.StructuredContent
	}
	return result
}

func validRunID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." {
		return false
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return false
	}
	return true
}
