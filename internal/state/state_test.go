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

func TestFindByIDAcrossSources(t *testing.T) {
	store, _ := openStore(t)
	if err := store.Put(TaskRecord{Source: "folder", ID: "001", Status: "todo"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := store.Put(TaskRecord{Source: "wave", ID: "001", Status: "succeeded", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	record, ok := store.FindByID("001")
	if !ok || record.Status != "succeeded" || record.Source != "wave" {
		t.Fatalf("FindByID = %+v ok=%v", record, ok)
	}
}

func TestLatestByID(t *testing.T) {
	now := time.Now()
	records := []TaskRecord{
		{Source: "wave", ID: "015", Status: "needs_input", UpdatedAt: now.Add(-time.Hour)},
		{Source: "folder", ID: "015", Status: "succeeded", UpdatedAt: now},
		{Source: "wave", ID: "035", Status: "needs_input", UpdatedAt: now},
	}
	latest := LatestByID(records)
	if len(latest) != 2 {
		t.Fatalf("latest = %+v", latest)
	}
	for _, record := range latest {
		if record.ID == "015" && record.Status != "succeeded" {
			t.Fatalf("015 = %+v", record)
		}
		if record.ID == "035" && record.Status != "needs_input" {
			t.Fatalf("035 = %+v", record)
		}
	}
}

func TestClaimByKeyAcrossAliases(t *testing.T) {
	store, _ := openStore(t)
	key := "folder:/tmp/tasks:001"
	claim, ok, err := store.ClaimByKey(key, "folder", "001", "run-1", time.Hour)
	if err != nil || !ok {
		t.Fatalf("ClaimByKey: %v %v", ok, err)
	}
	if claim.Info().Key != key {
		t.Fatalf("key = %q", claim.Info().Key)
	}
	if _, ok, _ := store.ClaimByKey(key, "wave-a", "001", "run-2", time.Hour); ok {
		t.Fatal("alias não deveria conseguir o mesmo claim")
	}
	_ = claim.Release()
	if claim2, ok, _ := store.ClaimByKey(key, "wave-a", "001", "run-2", time.Hour); ok {
		_ = claim2.Release()
	} else {
		t.Fatal("claim deveria estar livre após release")
	}
}

func TestClaimByKeySeesLegacyClaim(t *testing.T) {
	store, _ := openStore(t)
	legacy := filepath.Join(store.claimsDir, legacyClaimFileName("folder", "001"))
	if err := os.WriteFile(legacy, []byte(`{"source":"folder","id":"001","run_id":"old","pid":1,"created_at":"2026-01-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimByKey("folder:/tasks:001", "folder", "001", "new", time.Hour); err != nil || ok {
		t.Fatalf("claim deveria ver o lock legado: ok=%v err=%v", ok, err)
	}
}
