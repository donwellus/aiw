package daemonrestart

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/control"
)

const DefaultTimeout = 15 * time.Minute

type State struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	OldPID     int       `json:"old_pid,omitempty"`
	NewPID     int       `json:"new_pid,omitempty"`
	Message    string    `json:"message,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

type Spec struct {
	Binary  string   `json:"binary"`
	Args    []string `json:"args"`
	WorkDir string   `json:"work_dir"`
	LogPath string   `json:"log_path"`
}

func Run(cfg *config.Config, id string, timeout time.Duration) error {
	if strings.TrimSpace(id) == "" {
		id = NewID()
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	state := State{ID: id, Status: "running", StartedAt: time.Now().UTC()}
	if err := writeState(cfg, state); err != nil {
		return err
	}
	lock, err := acquireLock(cfg)
	if err != nil {
		state.Status = "failed"
		state.Message = err.Error()
		state.FinishedAt = time.Now().UTC()
		_ = writeState(cfg, state)
		return err
	}
	defer releaseLock(lock)

	info, active := control.DaemonActive(cfg, 0)
	if !active {
		return failState(cfg, state, errors.New("daemon não está ativo"))
	}
	spec, err := specFromInfo(info)
	if err != nil {
		return failState(cfg, state, err)
	}
	state.OldPID = info.PID
	if err := writeState(cfg, state); err != nil {
		return err
	}
	if err := syscall.Kill(info.PID, syscall.SIGINT); err != nil && !errors.Is(err, syscall.ESRCH) {
		return failState(cfg, state, fmt.Errorf("enviando SIGINT ao daemon: %w", err))
	}
	deadline := time.Now().Add(timeout)
	for processAlive(info.PID) {
		if time.Now().After(deadline) {
			return failState(cfg, state, fmt.Errorf("daemon %d não encerrou após %s; novo daemon não foi iniciado", info.PID, timeout))
		}
		time.Sleep(250 * time.Millisecond)
	}
	logFile, err := os.OpenFile(logPath(cfg, spec), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return failState(cfg, state, err)
	}
	defer logFile.Close()
	cmd := exec.Command(spec.Binary, spec.Args...)
	cmd.Dir = spec.WorkDir
	cmd.Env = os.Environ()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return failState(cfg, state, fmt.Errorf("iniciando novo daemon: %w", err))
	}
	newPID := cmd.Process.Pid
	_ = cmd.Process.Release()
	state.NewPID = newPID
	if err := writeState(cfg, state); err != nil {
		return err
	}
	for time.Now().Before(deadline) {
		next, nextActive := control.DaemonActive(cfg, 5*time.Second)
		if nextActive && next.PID != info.PID {
			state.Status = "succeeded"
			state.Message = "daemon reiniciado"
			state.FinishedAt = time.Now().UTC()
			_ = writeState(cfg, state)
			return nil
		}
		if !processAlive(newPID) {
			return failState(cfg, state, errors.New("novo daemon encerrou antes de publicar heartbeat"))
		}
		time.Sleep(250 * time.Millisecond)
	}
	return failState(cfg, state, errors.New("novo daemon não publicou heartbeat dentro do timeout"))
}

func Latest(cfg *config.Config) (State, bool) {
	data, err := os.ReadFile(filepath.Join(restartsDir(cfg), "latest.json"))
	if err != nil {
		return State{}, false
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, false
	}
	return state, true
}

func StatePath(cfg *config.Config, id string) string {
	return filepath.Join(restartsDir(cfg), id+".json")
}

func LogPath(cfg *config.Config, id string) string {
	return filepath.Join(restartsDir(cfg), id+".log")
}

func NewID() string {
	return fmt.Sprintf("rs-%s-%d", time.Now().UTC().Format("20060102-150405"), time.Now().UnixNano()%1_000_000)
}

func specFromInfo(info control.DaemonInfo) (Spec, error) {
	spec := Spec{Binary: info.Binary, Args: append([]string(nil), info.Args...), WorkDir: info.WorkDir, LogPath: info.LogPath}
	if spec.Binary == "" {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(info.PID), "cmdline"))
		if err != nil {
			return Spec{}, fmt.Errorf("lendo cmdline do daemon: %w", err)
		}
		parts := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		if len(parts) == 0 || parts[0] == "" {
			return Spec{}, errors.New("cmdline do daemon vazia")
		}
		spec.Binary = parts[0]
		spec.Args = parts[1:]
	}
	if spec.WorkDir == "" {
		if target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(info.PID), "cwd")); err == nil {
			spec.WorkDir = target
		}
	}
	if spec.LogPath == "" {
		if target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(info.PID), "fd", "1")); err == nil && filepath.IsAbs(target) && !strings.HasPrefix(target, "/dev/") && !strings.HasPrefix(target, "pipe:") {
			spec.LogPath = target
		}
	}
	if spec.WorkDir == "" {
		spec.WorkDir = "."
	}
	if spec.Binary == "" || len(spec.Args) == 0 {
		return Spec{}, errors.New("launch spec do daemon incompleta")
	}
	return spec, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data))
	return len(fields) < 3 || fields[2] != "Z"
}

func writeState(cfg *config.Config, state State) error {
	if err := os.MkdirAll(restartsDir(cfg), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	statePath := StatePath(cfg, state.ID)
	if err := os.WriteFile(statePath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(restartsDir(cfg), "latest.json"), append(data, '\n'), 0o644)
}

func failState(cfg *config.Config, state State, err error) error {
	state.Status = "failed"
	state.Message = err.Error()
	state.FinishedAt = time.Now().UTC()
	_ = writeState(cfg, state)
	return err
}

func restartsDir(cfg *config.Config) string {
	return filepath.Join(cfg.DataDir(), "control", "restarts")
}

func logPath(cfg *config.Config, spec Spec) string {
	if spec.LogPath != "" {
		return spec.LogPath
	}
	return filepath.Join(cfg.DataDir(), "daemon.log")
}

func acquireLock(cfg *config.Config) (*os.File, error) {
	dir := filepath.Join(cfg.DataDir(), "control")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "restart.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, errors.New("já existe um restart em andamento")
		}
		return nil, err
	}
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	return file, nil
}

func releaseLock(file *os.File) {
	if file == nil {
		return
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
}
