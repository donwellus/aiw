package cli

import "testing"

func TestStatusTitle(t *testing.T) {
	if got := statusTitle("  Uma   tarefa\ncom quebra  "); got != "Uma tarefa com quebra" {
		t.Fatalf("statusTitle = %q", got)
	}
	if got := statusTitle(""); got != "-" {
		t.Fatalf("statusTitle vazio = %q", got)
	}
	long := "12345678901234567890123456789012345678901234567890123456789012345"
	if got := statusTitle(long); len([]rune(got)) != 60 || got[len(got)-3:] != "..." {
		t.Fatalf("statusTitle longo = %q", got)
	}
}
