package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/config"
)

type Exec struct {
	name string
	cfg  config.EngineConfig
}

func NewExec(name string, cfg config.EngineConfig) (*Exec, error) {
	if len(cfg.Command) == 0 {
		return nil, fmt.Errorf("engine %s com driver exec precisa de command configurado", name)
	}
	if _, err := exec.LookPath(cfg.Command[0]); err != nil {
		return nil, fmt.Errorf("binário %q do engine %s não encontrado: %w", cfg.Command[0], name, err)
	}
	return &Exec{name: name, cfg: cfg}, nil
}

func (e *Exec) Name() string {
	return e.name
}

func (e *Exec) Run(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	cctx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	model := req.Model
	if model == "" {
		model = e.cfg.Model
	}
	promptVia := strings.ToLower(strings.TrimSpace(e.cfg.PromptVia))
	if promptVia == "" {
		promptVia = "arg"
	}

	args := make([]string, 0, len(e.cfg.Command)+1)
	for _, item := range e.cfg.Command[1:] {
		replaced := strings.NewReplacer(
			"{{prompt}}", req.Prompt,
			"{{workdir}}", req.Workdir,
			"{{model}}", model,
			"{{stage}}", req.Stage,
		).Replace(item)
		args = append(args, replaced)
	}
	if promptVia == "arg" && !containsPlaceholder(e.cfg.Command, "{{prompt}}") {
		args = append(args, req.Prompt)
	}

	cmd := exec.CommandContext(cctx, e.cfg.Command[0], args...)
	cmd.Dir = req.Workdir
	cmd.Env = append(os.Environ(), req.Env...)
	if promptVia == "stdin" {
		cmd.Stdin = strings.NewReader(req.Prompt)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := Result{
		Output:   strings.TrimSpace(stdout.String()),
		Raw:      stdout.Bytes(),
		Command:  displayCommand(e.cfg.Command[0], args),
		Duration: time.Since(start),
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return result, fmt.Errorf("engine %s falhou no estágio %s: %w: %s", e.name, req.Stage, err, truncate(msg, 2000))
	}
	return result, nil
}

func containsPlaceholder(items []string, placeholder string) bool {
	for _, item := range items {
		if strings.Contains(item, placeholder) {
			return true
		}
	}
	return false
}
