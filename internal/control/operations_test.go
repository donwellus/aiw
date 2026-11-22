package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/donwellus/aiw/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{Root: t.TempDir()}
}

func TestOperationLifecycle(t *testing.T) {
	store, err := OpenOperationStore(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.Enqueue(ActionResume, "run-1", map[string]any{"auto": true})
	if err != nil {
		t.Fatal(err)
	}
	if !ValidOperationID(operation.ID) {
		t.Fatalf("id inválido: %s", operation.ID)
	}
	listed, err := store.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Status != StatusQueued {
		t.Fatalf("lista inesperada: %+v", listed)
	}
	claimed, err := store.ClaimNext()
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != operation.ID || claimed.Status != StatusRunning {
		t.Fatalf("claim inesperado: %+v", claimed)
	}
	if next, err := store.ClaimNext(); err != nil || next != nil {
		t.Fatalf("segundo claim: %+v %v", next, err)
	}
	if err := store.Finish(operation.ID, StatusSucceeded, map[string]any{"run_id": "run-1"}, ""); err != nil {
		t.Fatal(err)
	}
	finished, err := store.Get(operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != StatusSucceeded || finished.FinishedAt == nil {
		t.Fatalf("finalização inesperada: %+v", finished)
	}
}

func TestCancelQueuedAndRunning(t *testing.T) {
	store, err := OpenOperationStore(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.Enqueue(ActionResume, "run-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := store.Cancel(queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != StatusCanceled {
		t.Fatalf("queued cancel = %+v", canceled)
	}
	if next, err := store.ClaimNext(); err != nil || next != nil {
		t.Fatalf("cancelada não deveria ser claimada: %+v %v", next, err)
	}

	running, err := store.Enqueue(ActionResume, "run-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimNext(); err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if _, err := store.Cancel(running.ID); err != nil {
		t.Fatal(err)
	}
	if !store.CancelRequested(running.ID) {
		t.Fatal("marcador de cancelamento ausente")
	}
	if err := store.Finish(running.ID, StatusCanceled, nil, ""); err != nil {
		t.Fatal(err)
	}
	if store.CancelRequested(running.ID) {
		t.Fatal("marcador de cancelamento deveria sumir")
	}
}

func TestConcurrentClaims(t *testing.T) {
	store, err := OpenOperationStore(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := store.Enqueue(ActionResume, "run", nil); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	claimed := map[string]bool{}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			operation, err := store.ClaimNext()
			if err != nil || operation == nil {
				return
			}
			mu.Lock()
			if claimed[operation.ID] {
				t.Errorf("operação claimada duas vezes: %s", operation.ID)
			}
			claimed[operation.ID] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(claimed) != 5 {
		t.Fatalf("claims = %d, esperado 5", len(claimed))
	}
}

func TestDaemonStatus(t *testing.T) {
	cfg := testConfig(t)
	info := DaemonInfo{PID: os.Getpid(), StartedAt: time.Now().UTC(), Workers: 2, Interval: "1m"}
	if err := WriteDaemonInfo(cfg, info); err != nil {
		t.Fatal(err)
	}
	if got, active := DaemonActive(cfg, 15*time.Second); !active || got.PID != info.PID {
		t.Fatalf("daemon deveria estar ativo: %+v %v", got, active)
	}
	stale := info
	stale.HeartbeatAt = time.Now().Add(-time.Hour)
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir(), "daemon.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, active := DaemonActive(cfg, 15*time.Second); active {
		t.Fatal("heartbeat antigo deveria estar inativo")
	}
	RemoveDaemonInfo(cfg, info.PID)
}
