package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxBody = 2 << 20

type GitHubEvent struct {
	Delivery string
	Action   string
	Number   int
	Repo     string
}

type githubHandler struct {
	repo    string
	secret  []byte
	onEvent func(GitHubEvent)

	mu   sync.Mutex
	seen map[string]time.Time
}

func NewGitHub(repo, secret string, onEvent func(GitHubEvent)) http.Handler {
	return &githubHandler{repo: repo, secret: []byte(secret), onEvent: onEvent, seen: map[string]time.Time{}}
}

func (h *githubHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		http.Error(w, "corpo inválido", http.StatusBadRequest)
		return
	}
	if len(body) > maxBody {
		http.Error(w, "payload muito grande", http.StatusRequestEntityTooLarge)
		return
	}
	if !h.validSignature(body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "assinatura inválida", http.StatusUnauthorized)
		return
	}
	delivery := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	if delivery != "" {
		if !h.markDelivery(delivery) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("duplicado\n"))
			return
		}
	}
	if r.Header.Get("X-GitHub-Event") == "ping" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong\n"))
		return
	}
	if r.Header.Get("X-GitHub-Event") != "issues" {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ignorado: evento\n"))
		return
	}
	var payload struct {
		Action string `json:"action"`
		Issue  struct {
			Number int `json:"number"`
		} `json:"issue"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "json inválido", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(payload.Repository.FullName, h.repo) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ignorado: repositório\n"))
		return
	}
	switch payload.Action {
	case "opened", "edited", "reopened", "labeled", "unlabeled":
	default:
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ignorado: ação\n"))
		return
	}
	if h.onEvent != nil {
		h.onEvent(GitHubEvent{
			Delivery: delivery,
			Action:   payload.Action,
			Number:   payload.Issue.Number,
			Repo:     payload.Repository.FullName,
		})
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("aceito\n"))
}

func (h *githubHandler) validSignature(body []byte, header string) bool {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, h.secret)
	mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

func (h *githubHandler) markDelivery(delivery string) bool {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, seenAt := range h.seen {
		if now.Sub(seenAt) > 24*time.Hour {
			delete(h.seen, key)
		}
	}
	if _, exists := h.seen[delivery]; exists {
		return false
	}
	if len(h.seen) > 10000 {
		h.seen = map[string]time.Time{}
	}
	h.seen[delivery] = now
	return true
}
