package cli

import (
	"reflect"
	"testing"

	"github.com/donwellus/aiw/internal/state"
)

func TestResumeCandidates(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(id, status, runID string) {
		t.Helper()
		if err := store.Put(state.TaskRecord{Source: "folder", ID: id, Status: status, RunID: runID}); err != nil {
			t.Fatal(err)
		}
	}
	put("a", "paused", "run-a")
	put("b", "todo", "run-b")
	put("c", "running", "run-c")
	put("d", "todo", "")
	put("e", "needs_input", "run-e")
	put("f", "todo", "run-a")

	got := resumeCandidates(store)
	want := []string{"run-a", "run-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidatos = %v, esperado %v", got, want)
	}
}

func TestResumeCandidatesUsesLatestStatus(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "a", Status: "paused", RunID: "run-a"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(state.TaskRecord{Source: "folder", ID: "a", Status: "running", RunID: "run-a"}); err != nil {
		t.Fatal(err)
	}
	if got := resumeCandidates(store); len(got) != 0 {
		t.Fatalf("candidatos = %v, esperado vazio", got)
	}
}
