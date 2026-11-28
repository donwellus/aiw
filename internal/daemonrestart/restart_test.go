package daemonrestart

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/control"
)

func TestSpecFromInfo(t *testing.T) {
	spec, err := specFromInfo(control.DaemonInfo{
		PID:     123,
		Binary:  "/usr/local/bin/aiw",
		Args:    []string{"daemon", "--workers", "3"},
		WorkDir: "/tmp/project",
		LogPath: "/tmp/aiw.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Binary != "/usr/local/bin/aiw" || len(spec.Args) != 3 || spec.WorkDir != "/tmp/project" || spec.LogPath != "/tmp/aiw.log" {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestRunFailsWithoutDaemonAndReleasesLock(t *testing.T) {
	cfg := &config.Config{Root: t.TempDir()}
	err := Run(cfg, "restart-test", time.Second)
	if err == nil {
		t.Fatal("restart sem daemon deveria falhar")
	}
	state, ok := Latest(cfg)
	if !ok || state.Status != "failed" {
		t.Fatalf("state = %+v ok=%v", state, ok)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir(), "control", "restart.lock")); !os.IsNotExist(err) {
		t.Fatalf("lock deveria ter sido liberado: %v", err)
	}
}
