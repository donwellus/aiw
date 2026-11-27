package taskcreate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	return &config.Config{
		Root:          root,
		DefaultSource: "folder",
		Sources: map[string]config.SourceConfig{
			"folder": {Driver: "folder", Path: "./tasks"},
		},
		Workspace: config.WorkspaceConfig{DefaultRepo: "."},
	}
}

func TestPreviewAndCreateRequiresAcknowledgedMissing(t *testing.T) {
	cfg := testConfig(t)
	service := New(cfg)
	ctx := context.Background()
	draft := source.TaskDraft{Title: "Sem aceite", Description: "Contexto suficiente."}
	preview, err := service.Prepare(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Source != "folder" || len(preview.Missing) != 1 || preview.Missing[0] != "acceptance" {
		t.Fatalf("preview = %+v", preview)
	}
	if _, err := service.Create(ctx, Request{Draft: draft, PreviewID: preview.PreviewID}); err == nil {
		t.Fatal("criação incompleta sem allow deveria falhar")
	}
	if _, err := service.Create(ctx, Request{Draft: draft, PreviewID: preview.PreviewID, AllowIncomplete: true, AcknowledgedMissing: []string{"description"}}); err == nil {
		t.Fatal("ack divergente deveria falhar")
	}
	result, err := service.Create(ctx, Request{Draft: draft, PreviewID: preview.PreviewID, AllowIncomplete: true, AcknowledgedMissing: []string{"acceptance"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalID == "" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "tasks", result.ExternalID+".md")); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewInvalidatesOnDraftChange(t *testing.T) {
	cfg := testConfig(t)
	service := New(cfg)
	ctx := context.Background()
	draft := source.TaskDraft{Title: "Completa", Description: "Contexto.", Acceptance: []string{"Funciona."}}
	preview, err := service.Prepare(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	changed := draft
	changed.Title = "Outra"
	if _, err := service.Create(ctx, Request{Draft: changed, PreviewID: preview.PreviewID}); err == nil {
		t.Fatal("preview alterado deveria falhar")
	}
}

func TestAttachmentsRequireAuthorization(t *testing.T) {
	cfg := testConfig(t)
	service := New(cfg)
	ctx := context.Background()
	image := filepath.Join(cfg.Root, "wireframe.png")
	if err := os.WriteFile(image, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	draft := source.TaskDraft{
		Title:       "Com anexo",
		Description: "Compare com a imagem.",
		Acceptance:  []string{"Layout confere."},
		Attachments: []string{image},
	}
	if _, err := service.Prepare(ctx, draft); err == nil {
		t.Fatal("anexo sem autorização deveria falhar")
	}
	draft.AttachmentsAuthorized = true
	preview, err := service.Prepare(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Attachments) != 1 || preview.Attachments[0] != image {
		t.Fatalf("preview attachments = %+v", preview.Attachments)
	}
	result, err := service.Create(ctx, Request{Draft: draft, PreviewID: preview.PreviewID})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalID == "" {
		t.Fatalf("result = %+v", result)
	}
}
