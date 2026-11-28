package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestStatusTitle(t *testing.T) {
	if got := statusTitle("  Uma   tarefa\ncom quebra  "); got != "Uma tarefa com quebra" {
		t.Fatalf("statusTitle = %q", got)
	}
	if got := statusTitle(""); got != "-" {
		t.Fatalf("statusTitle vazio = %q", got)
	}
	long := "12345678901234567890123456789012345678901234567890123456789012345"
	if got := statusTitle(long); got != long {
		t.Fatalf("statusTitle não deveria truncar: %q", got)
	}
}

func TestStatusTableOneLinePerTask(t *testing.T) {
	row := statusRow{
		ID:      "67",
		Status:  "running",
		Stage:   "requirements",
		RunID:   "20261128-163503-67-ba26f3",
		CostUSD: 0.0018,
		Source:  "github",
		PR:      "#62",
		Title:   "Uma tarefa com título longo",
	}
	var out strings.Builder
	renderStatusTable(&out, []statusRow{row}, 200)
	text := out.String()
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("esperava header + uma linha por task, obtive %d:\n%s", len(lines), text)
	}
	if !strings.Contains(text, "TÍTULO") || strings.Contains(text, "TÍTULO / DESCRIÇÃO") || strings.Contains(text, "Descrição") {
		t.Fatalf("tabela não deveria mostrar descrição:\n%s", text)
	}
	if !strings.Contains(text, "RUN") || !strings.Contains(text, "ATUALIZADO") || !strings.Contains(text, "DELIVERY") {
		t.Fatalf("tabela larga sem colunas opcionais:\n%s", text)
	}
	for _, line := range lines {
		if runeLen(line) > 200 {
			t.Fatalf("linha excede largura (%d): %q", runeLen(line), line)
		}
	}

	var narrow strings.Builder
	renderStatusTable(&narrow, []statusRow{row}, 80)
	narrowLines := strings.Split(strings.TrimSuffix(narrow.String(), "\n"), "\n")
	if len(narrowLines) != 2 {
		t.Fatalf("tabela estreita deveria manter uma linha por task:\n%s", narrow.String())
	}
	if strings.Contains(narrow.String(), "ATUALIZADO") {
		t.Fatalf("tabela estreita não deveria ter ATUALIZADO:\n%s", narrow.String())
	}
	if !strings.Contains(narrow.String(), "TÍTULO") || strings.Contains(narrow.String(), "TÍTULO / DESCRIÇÃO") || strings.Contains(narrow.String(), " — ") {
		t.Fatalf("tabela estreita deveria mostrar só título:\n%s", narrow.String())
	}
}

func TestStatusTableTruncatesLongText(t *testing.T) {
	row := statusRow{ID: "1", Status: "running", Stage: "test", Title: strings.Repeat("x", 200)}
	var out strings.Builder
	renderStatusTable(&out, []statusRow{row}, 80)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("linha não deveria quebrar:\n%s", out.String())
	}
	if !strings.Contains(lines[1], "…") {
		t.Fatalf("texto longo deveria terminar em elipse: %q", lines[1])
	}
	if runeLen(lines[1]) > 80 {
		t.Fatalf("linha excede 80: %q", lines[1])
	}
}

func TestTruncateStatusText(t *testing.T) {
	if got := truncateStatusText("uma descrição longa", 10); runeLen(got) != 10 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncate = %q", got)
	}
	if got := truncateStatusText("curta", 10); got != "curta" {
		t.Fatalf("texto curto alterado: %q", got)
	}
}

func TestTerminalWidthFromColumns(t *testing.T) {
	t.Setenv("COLUMNS", "137")
	var out bytes.Buffer
	if got := terminalWidth(&out); got != 137 {
		t.Fatalf("terminalWidth = %d", got)
	}
}
