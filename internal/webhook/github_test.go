package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func request(t *testing.T, body, event, delivery, signature string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-Hub-Signature-256", signature)
	return req
}

func TestGitHubWebhook(t *testing.T) {
	body := `{"action":"opened","issue":{"number":42},"repository":{"full_name":"owner/repo"}}`
	var calls int32
	handler := NewGitHub("owner/repo", "secret", func(event GitHubEvent) {
		atomic.AddInt32(&calls, 1)
		if event.Number != 42 || event.Action != "opened" {
			t.Errorf("evento inesperado: %+v", event)
		}
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request(t, body, "issues", "delivery-1", sign("secret", body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(t, body, "issues", "delivery-1", sign("secret", body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("duplicado status = %d", rec.Code)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, esperado 1 (dedupe)", got)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(t, body, "issues", "delivery-2", "sha256=deadbeef"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("assinatura inválida status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(t, `{}`, "ping", "delivery-3", sign("secret", `{}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("ping = %d %q", rec.Code, rec.Body.String())
	}

	other := `{"action":"opened","issue":{"number":42},"repository":{"full_name":"other/repo"}}`
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(t, other, "issues", "delivery-4", sign("secret", other)))
	if rec.Code != http.StatusAccepted || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("repo errado status = %d calls=%d", rec.Code, calls)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(t, body, "push", "delivery-5", sign("secret", body)))
	if rec.Code != http.StatusAccepted || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("evento errado status = %d calls=%d", rec.Code, calls)
	}
}

func TestGitHubWebhookRejectsGet(t *testing.T) {
	handler := NewGitHub("owner/repo", "secret", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/webhooks/github", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
}
