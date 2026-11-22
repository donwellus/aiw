package taskkey

import (
	"path/filepath"
	"testing"

	"github.com/donwellus/aiw/internal/config"
)

func TestKeyerCanonicalizesFolderAliases(t *testing.T) {
	cfg := &config.Config{
		Root:          "/tmp/project",
		DefaultSource: "folder",
		Sources: map[string]config.SourceConfig{
			"folder": {Driver: "folder", Path: "./tasks"},
			"alias":  {Driver: "folder", Path: "./tasks"},
		},
	}
	keys := New(cfg)
	want := "folder:" + filepath.Join("/tmp/project", "tasks") + ":001"
	if got := keys.Key("folder", "001", ""); got != want {
		t.Fatalf("folder = %q, esperado %q", got, want)
	}
	if got := keys.Key("alias", "001", ""); got != want {
		t.Fatalf("alias = %q, esperado %q", got, want)
	}
	if got := keys.Key("linear", "ABC-1", ""); got != "linear:ABC-1" {
		t.Fatalf("remoto = %q", got)
	}
	if got := keys.Key("folder", "001", "custom"); got != "custom" {
		t.Fatalf("claim key = %q", got)
	}
}
