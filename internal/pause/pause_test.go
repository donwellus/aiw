package pause

import (
	"context"
	"testing"
	"time"
)

func TestMarkerLifecycle(t *testing.T) {
	dir := t.TempDir()
	if Active(dir) {
		t.Fatal("pausa ativa sem marker")
	}
	if err := Set(dir, "manual"); err != nil {
		t.Fatal(err)
	}
	if !Active(dir) {
		t.Fatal("marker criado mas pausa inativa")
	}
	if err := Clear(dir); err != nil {
		t.Fatal(err)
	}
	if Active(dir) {
		t.Fatal("pausa ativa após clear")
	}
	if err := Clear(dir); err != nil {
		t.Fatalf("clear idempotente: %v", err)
	}
}

func TestWatch(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fired := make(chan string, 1)
	stop := Watch(ctx, dir, 5*time.Millisecond, func() { fired <- "paused" })
	defer stop()

	if err := Set(dir, "watch"); err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-fired:
		if reason != "paused" {
			t.Fatalf("motivo inesperado: %s", reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch não disparou após criar o marker")
	}
	if Active(dir) == false {
		t.Fatal("marker deveria continuar ativo")
	}
}

func TestWatchStopsOnCancel(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	stop := Watch(ctx, dir, 5*time.Millisecond, func() { t.Error("watch disparou após cancelamento") })
	cancel()
	stop()
	time.Sleep(20 * time.Millisecond)
}
