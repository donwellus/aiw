package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
)

type Claude struct {
	name string
	cfg  config.EngineConfig
}

func NewClaude(name string, cfg config.EngineConfig) (*Claude, error) {
	if cfg.Bin == "" {
		cfg.Bin = "claude"
	}
	if _, err := exec.LookPath(cfg.Bin); err != nil {
		return nil, fmt.Errorf("binário %q do engine %s não encontrado: %w", cfg.Bin, name, err)
	}
	return &Claude{name: name, cfg: cfg}, nil
}

func (c *Claude) Name() string {
	return c.name
}

func (c *Claude) Run(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	cctx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	model := req.Model
	if model == "" {
		model = c.cfg.Model
	}
	args := []string{"-p", "--output-format", "json"}
	if c.cfg.PermissionMode != "" {
		args = append(args, "--permission-mode", c.cfg.PermissionMode)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if req.SessionID != "" {
		args = append(args, "--session-id", req.SessionID)
	}
	if len(c.cfg.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(c.cfg.AllowedTools, ","))
	}
	args = append(args, c.cfg.ExtraArgs...)
	args = append(args, req.Prompt)

	cmd := exec.CommandContext(cctx, c.cfg.Bin, args...)
	cmd.Dir = req.Workdir
	cmd.Env = append(os.Environ(), req.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := Result{
		Raw:      stdout.Bytes(),
		Command:  displayCommand(c.cfg.Bin, args[:len(args)-1]),
		Duration: time.Since(start),
	}
	parsed, parseErr := parseClaudeOutput(stdout.Bytes())
	if parseErr == nil {
		result.Output = parsed.Result
		result.SessionID = parsed.SessionID
		result.Usage = Usage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
			CostUSD:      parsed.TotalCostUSD,
		}
	} else {
		result.Output = strings.TrimSpace(stdout.String())
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return result, fmt.Errorf("claude falhou no estágio %s: %w: %s", req.Stage, err, truncate(msg, 2000))
	}
	if parsed != nil && parsed.IsError {
		return result, fmt.Errorf("claude retornou erro no estágio %s: %s", req.Stage, truncate(parsed.Result, 2000))
	}
	return result, nil
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type claudeResult struct {
	Type         string      `json:"type"`
	Subtype      string      `json:"subtype"`
	IsError      bool        `json:"is_error"`
	Result       string      `json:"result"`
	SessionID    string      `json:"session_id"`
	TotalCostUSD float64     `json:"total_cost_usd"`
	Usage        claudeUsage `json:"usage"`
}

func parseClaudeOutput(raw []byte) (*claudeResult, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("saída vazia")
	}
	var parsed claudeResult
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return nil, err
	}
	if parsed.Type == "" && parsed.Result == "" && parsed.SessionID == "" {
		return nil, fmt.Errorf("json sem campos esperados")
	}
	return &parsed, nil
}

func displayCommand(bin string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, bin)
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncado)"
}
