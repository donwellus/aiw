package sourcetest

import (
	"context"
	"testing"

	"github.com/donwellus/aiw/internal/source"
)

type Options struct {
	ExpectedID string
	Filter     source.Filter
}

func Run(t *testing.T, src source.Source, opts Options) {
	t.Helper()

	t.Run("name", func(t *testing.T) {
		if src.Name() == "" {
			t.Fatal("Name() vazio")
		}
	})

	var refs []source.Ref
	t.Run("list", func(t *testing.T) {
		var err error
		refs, err = src.List(context.Background(), opts.Filter)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(refs) == 0 {
			t.Fatal("List retornou zero tarefas")
		}
		if opts.ExpectedID != "" {
			found := false
			for _, ref := range refs {
				if ref.ID == opts.ExpectedID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("ID %q não encontrado em List", opts.ExpectedID)
			}
		}
	})

	if opts.ExpectedID == "" {
		return
	}

	var ref source.Ref
	for _, candidate := range refs {
		if candidate.ID == opts.ExpectedID {
			ref = candidate
			break
		}
	}

	t.Run("fetch", func(t *testing.T) {
		task, err := src.Fetch(context.Background(), ref)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if task.ExternalID != opts.ExpectedID {
			t.Fatalf("ExternalID = %q, esperado %q", task.ExternalID, opts.ExpectedID)
		}
		if task.Title == "" {
			t.Fatal("Title vazio")
		}
		if task.ContentHash == "" {
			t.Fatal("ContentHash vazio")
		}
		again, err := src.Fetch(context.Background(), ref)
		if err != nil {
			t.Fatalf("Fetch repetido: %v", err)
		}
		if again.ContentHash != task.ContentHash {
			t.Fatal("ContentHash instável entre fetches")
		}
	})

	t.Run("fetch-missing", func(t *testing.T) {
		missing := source.Ref{Source: src.Name(), ID: "aiw-nao-existe", URL: "/tmp/aiw-nao-existe.md"}
		if _, err := src.Fetch(context.Background(), missing); err == nil {
			t.Fatal("Fetch de ref inexistente deveria falhar")
		}
	})
}
