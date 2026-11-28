package control

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/pipeline"
)

func SetDeliveryFeedback(cfg *config.Config, runID, decision, feedback string) (string, error) {
	if err := ValidateRunID(runID); err != nil {
		return "", err
	}
	decision = strings.ToUpper(strings.TrimSpace(decision))
	switch decision {
	case "FIX_CODE", "FIX_TEST", "RETRY_CI":
	default:
		return "", fmt.Errorf("decisão inválida %q (use FIX_CODE, FIX_TEST ou RETRY_CI)", decision)
	}
	run, err := pipeline.LoadRun(cfg, runID)
	if err != nil {
		return "", err
	}
	if run.Status != "paused" {
		return "", fmt.Errorf("run %s está %s; decisão de CI exige run pausada", runID, run.Status)
	}
	dir := filepath.Join(cfg.RunsDir(), runID, "feedback")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "delivery.md")
	content := fmt.Sprintf("# Decisão humana — correção de CI\n\nDECISION: %s\n\n%s\n", decision, strings.TrimSpace(feedback))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	if run.CIFix == nil {
		run.CIFix = &pipeline.CIFixState{}
	}
	run.CIFix.Rounds = 0
	run.CIFix.LastError = ""
	if err := pipeline.SaveRun(cfg, run); err != nil {
		return "", err
	}
	return path, nil
}
