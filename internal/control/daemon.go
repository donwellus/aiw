package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/donwellus/aiw/internal/config"
)

type DaemonInfo struct {
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	Workers     int       `json:"workers"`
	Interval    string    `json:"interval"`
	Source      string    `json:"source,omitempty"`
	StatusAddr  string    `json:"status_addr,omitempty"`
	Version     string    `json:"version,omitempty"`
}

func DaemonPath(cfg *config.Config) string {
	return filepath.Join(cfg.DataDir(), "daemon.json")
}

func WriteDaemonInfo(cfg *config.Config, info DaemonInfo) error {
	info.HeartbeatAt = time.Now().UTC()
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(DaemonPath(cfg), append(data, '\n'))
}

func ReadDaemonInfo(cfg *config.Config) (DaemonInfo, bool) {
	data, err := os.ReadFile(DaemonPath(cfg))
	if err != nil {
		return DaemonInfo{}, false
	}
	var info DaemonInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return DaemonInfo{}, false
	}
	if info.PID == 0 {
		return DaemonInfo{}, false
	}
	return info, true
}

func RemoveDaemonInfo(cfg *config.Config, pid int) {
	info, ok := ReadDaemonInfo(cfg)
	if !ok || info.PID != pid {
		return
	}
	_ = os.Remove(DaemonPath(cfg))
}

func DaemonActive(cfg *config.Config, maxAge time.Duration) (DaemonInfo, bool) {
	info, ok := ReadDaemonInfo(cfg)
	if !ok {
		return DaemonInfo{}, false
	}
	if maxAge <= 0 {
		maxAge = 15 * time.Second
	}
	if info.HeartbeatAt.IsZero() || time.Since(info.HeartbeatAt) > maxAge {
		return info, false
	}
	return info, true
}
