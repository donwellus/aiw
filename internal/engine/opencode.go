package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
)

type Opencode struct {
	name    string
	cfg     config.EngineConfig
	mcpPath string
}

func NewOpencode(name string, cfg config.EngineConfig, dataDir string) (*Opencode, error) {
	if cfg.Bin == "" {
		cfg.Bin = "opencode"
	}
	if _, err := exec.LookPath(cfg.Bin); err != nil {
		return nil, fmt.Errorf("binário %q do engine %s não encontrado: %w", cfg.Bin, name, err)
	}
	mcpPath, err := writeOpencodeMCP(dataDir, name, cfg)
	if err != nil {
		return nil, err
	}
	return &Opencode{name: name, cfg: cfg, mcpPath: mcpPath}, nil
}

func (o *Opencode) Name() string {
	return o.name
}

func (o *Opencode) Run(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	cctx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	model := req.Model
	if model == "" {
		model = o.cfg.Model
	}
	args := []string{"run", "--format", "json", "--dir", req.Workdir}
	if model != "" {
		args = append(args, "--model", model)
	}
	if o.cfg.AutoApprove {
		args = append(args, "--auto")
	}
	args = append(args, o.cfg.ExtraArgs...)
	if strings.HasPrefix(strings.TrimSpace(req.Prompt), "-") {
		args = append(args, "--")
	}
	args = append(args, req.Prompt)

	cmd := exec.CommandContext(cctx, o.cfg.Bin, args...)
	cmd.Dir = req.Workdir
	env := append([]string{}, req.Env...)
	if o.mcpPath != "" {
		env = append(env, "OPENCODE_CONFIG="+o.mcpPath)
	}
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	acc := parseOpencodeEvents(stdout.Bytes())
	result := Result{
		Output:    acc.text(),
		SessionID: acc.sessionID,
		Usage:     Usage{InputTokens: acc.inputTokens, OutputTokens: acc.outputTokens, CostUSD: acc.cost},
		Raw:       stdout.Bytes(),
		Command:   displayCommand(o.cfg.Bin, args[:len(args)-1]),
		Duration:  time.Since(start),
	}
	if result.Output == "" {
		result.Output = strings.TrimSpace(stdout.String())
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return result, fmt.Errorf("opencode falhou no estágio %s: %w: %s", req.Stage, err, truncate(msg, 2000))
	}
	return result, nil
}

type opencodeAcc struct {
	sessionID    string
	cost         float64
	inputTokens  int
	outputTokens int
	texts        []string
}

func (a *opencodeAcc) text() string {
	if len(a.texts) == 0 {
		return ""
	}
	return truncate(strings.Join(a.texts, "\n"), 200000)
}

func parseOpencodeEvents(raw []byte) *opencodeAcc {
	acc := &opencodeAcc{}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var value any
		if err := json.Unmarshal(line, &value); err != nil {
			continue
		}
		walkOpencode(value, acc)
	}
	return acc
}

func walkOpencode(value any, acc *opencodeAcc) {
	switch typed := value.(type) {
	case map[string]any:
		for key, raw := range typed {
			switch key {
			case "sessionID", "sessionId", "session_id":
				if s, ok := raw.(string); ok && s != "" {
					acc.sessionID = s
				}
			case "cost":
				if f, ok := toFloat(raw); ok && f > acc.cost {
					acc.cost = f
				}
			case "tokens":
				if m, ok := raw.(map[string]any); ok {
					acc.inputTokens = maxInt(acc.inputTokens, tokenField(m, "input", "input_tokens", "prompt"))
					acc.outputTokens = maxInt(acc.outputTokens, tokenField(m, "output", "output_tokens", "completion"))
				}
			case "usage":
				if m, ok := raw.(map[string]any); ok {
					acc.inputTokens = maxInt(acc.inputTokens, tokenField(m, "input", "input_tokens", "prompt_tokens"))
					acc.outputTokens = maxInt(acc.outputTokens, tokenField(m, "output", "output_tokens", "completion_tokens"))
				}
			case "text":
				if s, ok := raw.(string); ok && s != "" && isTextPart(typed) {
					acc.texts = appendUnique(acc.texts, s)
				}
			}
			walkOpencode(raw, acc)
		}
	case []any:
		for _, item := range typed {
			walkOpencode(item, acc)
		}
	}
}

func isTextPart(m map[string]any) bool {
	if t, ok := m["type"].(string); ok && strings.Contains(strings.ToLower(t), "text") {
		return true
	}
	if part, ok := m["part"].(map[string]any); ok {
		if t, ok := part["type"].(string); ok && strings.Contains(strings.ToLower(t), "text") {
			return true
		}
	}
	return false
}

func tokenField(m map[string]any, keys ...string) int {
	best := 0
	for _, key := range keys {
		if f, ok := toFloat(m[key]); ok && int(f) > best {
			best = int(f)
		}
	}
	return best
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func maxInt(a, b int) int {
	if b > a {
		return b
	}
	return a
}

func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}
