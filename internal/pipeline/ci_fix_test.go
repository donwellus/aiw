package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/donwellus/aiw/internal/config"
)

func TestParseCIVerdict(t *testing.T) {
	verdict := parseCIVerdict("DECISION: FIX_TEST\nDETAILS:\nO golden usa markup antigo.\n- arquivo: golden.spec.ts\n")
	if verdict.Decision != "FIX_TEST" {
		t.Fatalf("decision = %q", verdict.Decision)
	}
	if !strings.Contains(verdict.Details, "golden.spec.ts") {
		t.Fatalf("details = %q", verdict.Details)
	}
}

func TestValidateCIFixFiles(t *testing.T) {
	if err := validateCIFixFiles("FIX_TEST", []string{"apps/e2e/e2e/golden.spec.ts", "apps/reader/e2e/offline.spec.ts"}); err != nil {
		t.Fatalf("testes deveriam ser permitidos: %v", err)
	}
	if err := validateCIFixFiles("FIX_TEST", []string{"apps/reader/src/features/sheet/index.tsx"}); err == nil {
		t.Fatal("FIX_TEST não deveria alterar source")
	}
	if err := validateCIFixFiles("FIX_CODE", []string{".github/workflows/ci.yml"}); err == nil {
		t.Fatal("workflow deveria ser proibido")
	}
}

func TestFailedCIFixChecksOnlyConfiguredChecks(t *testing.T) {
	yes := true
	cfg := &config.Config{Delivery: config.DeliveryConfig{
		CIFix:       &yes,
		CIFixChecks: []string{"verify"},
	}}
	p := &Pipeline{Cfg: cfg}
	status := prStatus{StatusChecks: []prCheck{
		{Name: "verify", Status: "COMPLETED", Conclusion: "FAILURE", DetailsURL: "https://github.com/o/r/actions/runs/1/job/2"},
		{Name: "security", Status: "COMPLETED", Conclusion: "FAILURE", DetailsURL: "https://github.com/o/r/actions/runs/3/job/4"},
	}}
	failed := p.failedCIFixChecks(status)
	if len(failed) != 1 || failed[0].Name != "verify" {
		t.Fatalf("failed = %+v", failed)
	}
	cfg.Delivery.CIFix = nil
	if len(p.failedCIFixChecks(status)) != 0 {
		t.Fatal("ci_fix desabilitado não deveria retornar checks")
	}
}

func TestCIFixURLParse(t *testing.T) {
	match := ciJobURL.FindStringSubmatch("https://github.com/o/r/actions/runs/123/job/456")
	if len(match) < 3 || match[1] != "123" || match[2] != "456" {
		t.Fatalf("match = %#v", match)
	}
}

func TestValidateCompletionRequiresDeliveredPR(t *testing.T) {
	yes := true
	cfg := &config.Config{Delivery: config.DeliveryConfig{Merge: "auto", RequireCI: &yes}}
	p := &Pipeline{Cfg: cfg}
	run := &RunState{CreatePR: true}
	if err := p.validateCompletion(run, Options{CreatePR: true}); err == nil {
		t.Fatal("run sem PR não deveria concluir")
	}
	run.PRURL = "https://github.com/o/r/pull/1"
	if err := p.validateCompletion(run, Options{CreatePR: true}); err == nil {
		t.Fatal("PR não mergeado deveria bloquear auto merge")
	}
	run.PRMerged = true
	if err := p.validateCompletion(run, Options{CreatePR: true}); err != nil {
		t.Fatal(err)
	}
	if err := p.validateCompletion(&RunState{}, Options{}); err != nil {
		t.Fatalf("run local deveria concluir: %v", err)
	}
}

func TestAdoptPRRequiresURLAndBranch(t *testing.T) {
	p := &Pipeline{}
	if _, err := p.AdoptPR(context.Background(), "missing", ""); err == nil {
		t.Fatal("URL vazia deveria falhar")
	}
}
