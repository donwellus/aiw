package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store, dir
}

func TestPutGetAll(t *testing.T) {
	store, _ := openStore(t)
	if err := store.Put(TaskRecord{Source: "folder", ID: "002", Status: "todo"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(TaskRecord{Source: "folder", ID: "001", Status: "succeeded", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	record, ok := store.Get("folder", "001")
	if !ok || record.Status != "succeeded" || record.RunID != "run-1" {
		t.Fatalf("Get inesperado: %+v ok=%v", record, ok)
	}
	if _, ok := store.Get("folder", "nao-existe"); ok {
		t.Fatal("Get deveria retornar false")
	}
	all := store.All()
	if len(all) != 2 || all[0].ID != "001" || all[1].ID != "002" {
		t.Fatalf("All inesperado: %+v", all)
	}
	if _, ok := store.FindByRunID("run-1"); !ok {
		t.Fatal("FindByRunID deveria achar run-1")
	}
}

func TestMigrationFromLegacyTasksJSON(t *testing.T) {
	stateDir := t.TempDir()
	legacy := map[string]TaskRecord{
		"folder:001": {Status: "succeeded", RunID: "run-1", ContentHash: "abc"},
		"folder:002": {Source: "folder", ID: "002", Status: "failed"},
	}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(stateDir, "tasks.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	record, ok := store.Get("folder", "001")
	if !ok || record.Status != "succeeded" || record.ID != "001" {
		t.Fatalf("registro migrado inesperado: %+v", record)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "tasks.json.migrated")); err != nil {
		t.Fatalf("backup da migração ausente: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "tasks.json")); !os.IsNotExist(err) {
		t.Fatal("tasks.json antigo deveria ter sido renomeado")
	}
}

func TestClaimExclusiveAndRelease(t *testing.T) {
	store, _ := openStore(t)
	claim, ok, err := store.Claim("folder", "001", "run-1", time.Hour)
	if err != nil || !ok {
		t.Fatalf("Claim inicial: ok=%v err=%v", ok, err)
	}
	if claim.Info().RunID != "run-1" {
		t.Fatalf("info inesperada: %+v", claim.Info())
	}
	if _, ok, _ := store.Claim("folder", "001", "run-2", time.Hour); ok {
		t.Fatal("segundo Claim deveria falhar")
	}
	if _, ok := store.ClaimInfo("folder", "001"); !ok {
		t.Fatal("ClaimInfo deveria existir")
	}
	if err := claim.Release(); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.ClaimInfo("folder", "001"); ok {
		t.Fatal("claim deveria ter sido removido")
	}
	claim2, ok, err := store.Claim("folder", "001", "run-2", time.Hour)
	if err != nil || !ok {
		t.Fatalf("Claim após release: ok=%v err=%v", ok, err)
	}
	_ = claim2.Release()
}

func TestClaimStale(t *testing.T) {
	store, _ := openStore(t)
	claim, ok, err := store.Claim("folder", "001", "run-1", time.Hour)
	if err != nil || !ok {
		t.Fatalf("Claim: %v %v", ok, err)
	}
	info := claim.Info()
	info.CreatedAt = time.Now().Add(-3 * time.Hour)
	data, _ := json.Marshal(info)
	claimPath := filepath.Join(store.claimsDir, claimFileName("folder", "001"))
	if err := os.WriteFile(claimPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(claimPath, old, old); err != nil {
		t.Fatal(err)
	}
	claim2, ok, err := store.Claim("folder", "001", "run-2", time.Hour)
	if err != nil || !ok {
		t.Fatalf("Claim stale deveria ser assumido: ok=%v err=%v", ok, err)
	}
	if claim2.Info().RunID != "run-2" {
		t.Fatalf("runID = %q", claim2.Info().RunID)
	}
	_ = claim2.Release()
}

func TestClaimConcurrent(t *testing.T) {
	store, _ := openStore(t)
	var successes int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			claim, ok, err := store.Claim("folder", "001", "run", time.Hour)
			if err != nil {
				t.Errorf("Claim: %v", err)
				return
			}
			if ok {
				atomic.AddInt32(&successes, 1)
				time.Sleep(5 * time.Millisecond)
				_ = claim.Release()
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("claims bem-sucedidos = %d, esperado 1", successes)
	}
}

func TestFindAndReleaseClaimByRunID(t *testing.T) {
	store, _ := openStore(t)
	claim, ok, err := store.Claim("folder", "001", "run-x", time.Hour)
	if err != nil || !ok {
		t.Fatalf("Claim: %v %v", ok, err)
	}
	info, ok := store.FindClaimByRunID("run-x")
	if !ok || info.Source != "folder" || info.ID != "001" {
		t.Fatalf("FindClaimByRunID = %+v ok=%v", info, ok)
	}
	if err := claim.Release(); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseClaim("folder", "001"); err != nil {
		t.Fatal(err)
	}
}

func TestClaimStaleConcurrent(t *testing.T) {
	store, _ := openStore(t)
	initial, ok, err := store.Claim("folder", "001", "run-old", time.Hour)
	if err != nil || !ok {
		t.Fatalf("Claim inicial: %v %v", ok, err)
	}
	old := time.Now().Add(-3 * time.Hour)
	claimPath := filepath.Join(store.claimsDir, claimFileName("folder", "001"))
	if err := os.Chtimes(claimPath, old, old); err != nil {
		t.Fatal(err)
	}
	_ = initial

	var successes int32
	var wg sync.WaitGroup
	var mu sync.Mutex
	var held []*Claim
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim, ok, err := store.Claim("folder", "001", "run-new", time.Hour)
			if err != nil {
				t.Errorf("Claim: %v", err)
				return
			}
			if ok {
				atomic.AddInt32(&successes, 1)
				mu.Lock()
				held = append(held, claim)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	for _, claim := range held {
		_ = claim.Release()
	}
	if successes != 1 {
		t.Fatalf("sucessos no takeover = %d, esperado 1", successes)
	}
}
