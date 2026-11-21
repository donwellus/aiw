package pause

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const Marker = "PAUSE"

func Path(dataDir string) string {
	return filepath.Join(dataDir, Marker)
}

func Active(dataDir string) bool {
	if dataDir == "" {
		return false
	}
	_, err := os.Stat(Path(dataDir))
	return err == nil
}

func Set(dataDir, reason string) error {
	if dataDir == "" {
		return errors.New("data dir vazio")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	content := time.Now().UTC().Format(time.RFC3339Nano)
	if reason != "" {
		content += " " + reason
	}
	return os.WriteFile(Path(dataDir), []byte(content+"\n"), 0o644)
}

func Clear(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	err := os.Remove(Path(dataDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func Watch(ctx context.Context, dataDir string, interval time.Duration, onPause func()) func() {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				if Active(dataDir) {
					if onPause != nil {
						onPause()
					}
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
	}
}
