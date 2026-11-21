package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func sampleReport() *Report {
	return &Report{
		GeneratedAt: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC),
		Root:        "/tmp/<projeto>",
		Paused:      true,
		Source:      "folder",
		Summary: Summary{
			Tasks:        1,
			ByStatus:     map[string]int{"failed": 1},
			Runs:         1,
			CostUSD:      1.2345,
			TaskCostUSD:  1.2345,
			InputTokens:  10,
			OutputTokens: 5,
			ActiveClaims: 1,
			StaleClaims:  1,
		},
		Tasks: []Task{{
			ID:        "a|b",
			Title:     "<script>alert(1)</script>",
			Source:    "folder",
			Status:    "failed",
			Stage:     "test",
			RunID:     "run-1",
			CostUSD:   1.2345,
			UpdatedAt: time.Date(2026, 11, 21, 11, 0, 0, 0, time.UTC),
			Error:     "falhou",
		}},
		Claims: []Claim{{TaskID: "a|b", RunID: "run-1", PID: 42, AgeSeconds: 7200, Stale: true}},
		Alerts: []Alert{{Level: "error", Kind: "failed", TaskID: "a|b", RunID: "run-1", Message: "falhou"}},
	}
}

func TestRenderFormats(t *testing.T) {
	report := sampleReport()
	tests := []struct {
		name   string
		format Format
		check  func(t *testing.T, out string)
	}{
		{
			name:   "table",
			format: FormatTable,
			check: func(t *testing.T, out string) {
				for _, want := range []string{"RESUMO", "TAREFAS", "US$ 1.2345", "CLAIMS", "[error] failed"} {
					if !strings.Contains(out, want) {
						t.Fatalf("tabela sem %q:\n%s", want, out)
					}
				}
			},
		},
		{
			name:   "json",
			format: FormatJSON,
			check: func(t *testing.T, out string) {
				var decoded Report
				if err := json.Unmarshal([]byte(out), &decoded); err != nil {
					t.Fatal(err)
				}
				if decoded.Tasks[0].Title != "<script>alert(1)</script>" || decoded.Summary.CostUSD != 1.2345 {
					t.Fatalf("json inesperado: %+v", decoded)
				}
			},
		},
		{
			name:   "yaml",
			format: FormatYAML,
			check: func(t *testing.T, out string) {
				var decoded Report
				if err := yaml.Unmarshal([]byte(out), &decoded); err != nil {
					t.Fatal(err)
				}
				if decoded.Tasks[0].Title != "<script>alert(1)</script>" || decoded.Summary.CostUSD != 1.2345 {
					t.Fatalf("yaml inesperado: %+v", decoded)
				}
			},
		},
		{
			name:   "markdown",
			format: FormatMD,
			check: func(t *testing.T, out string) {
				if strings.Contains(out, "<script>") {
					t.Fatalf("markdown contém HTML bruto:\n%s", out)
				}
				for _, want := range []string{"# Relatório aiw", "&lt;script&gt;", `a\|b`, "## Alertas"} {
					if !strings.Contains(out, want) {
						t.Fatalf("markdown sem %q:\n%s", want, out)
					}
				}
			},
		},
		{
			name:   "html",
			format: FormatHTML,
			check: func(t *testing.T, out string) {
				if strings.Contains(out, "<script>alert") {
					t.Fatalf("html contém script bruto:\n%s", out)
				}
				for _, want := range []string{"<!doctype html>", "&lt;script&gt;", "class=\"status failed\"", "a|b"} {
					if !strings.Contains(out, want) {
						t.Fatalf("html sem %q:\n%s", want, out)
					}
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := Render(&out, test.format, report); err != nil {
				t.Fatal(err)
			}
			test.check(t, out.String())
		})
	}
}

func TestRenderUnknownFormat(t *testing.T) {
	var out bytes.Buffer
	if err := Render(&out, Format("xml"), sampleReport()); err == nil {
		t.Fatal("formato desconhecido deveria falhar")
	}
}
