package telemetry

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/donwellus/aiw/internal/config"
)

type capturedSpan struct {
	Name    string
	TraceID string
	Attrs   map[string]any
}

type collector struct {
	mu               sync.Mutex
	spans            []capturedSpan
	scores           []map[string]any
	status           int
	authHeader       string
	ingestionVersion string
	tracePath        string
}

func (c *collector) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/public/otel/v1/traces":
		body, _ := io.ReadAll(r.Body)
		var request collectortracepb.ExportTraceServiceRequest
		_ = proto.Unmarshal(body, &request)
		c.mu.Lock()
		c.authHeader = r.Header.Get("Authorization")
		c.ingestionVersion = r.Header.Get("x-langfuse-ingestion-version")
		c.tracePath = r.URL.Path
		for _, resourceSpans := range request.ResourceSpans {
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				for _, span := range scopeSpans.Spans {
					c.spans = append(c.spans, capturedSpan{
						Name:    span.Name,
						TraceID: hex.EncodeToString(span.TraceId),
						Attrs:   attrsToMap(span.Attributes),
					})
				}
			}
		}
		status := c.status
		c.mu.Unlock()
		if status >= 300 {
			w.WriteHeader(status)
			return
		}
		w.WriteHeader(http.StatusOK)
	case "/api/public/scores":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		c.mu.Lock()
		c.scores = append(c.scores, body)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func attrsToMap(attrs []*commonpb.KeyValue) map[string]any {
	out := make(map[string]any, len(attrs))
	for _, kv := range attrs {
		out[kv.Key] = valueToAny(kv.Value)
	}
	return out
}

func valueToAny(value *commonpb.AnyValue) any {
	switch typed := value.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return typed.StringValue
	case *commonpb.AnyValue_IntValue:
		return typed.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return typed.DoubleValue
	case *commonpb.AnyValue_BoolValue:
		return typed.BoolValue
	case *commonpb.AnyValue_ArrayValue:
		items := make([]any, 0, len(typed.ArrayValue.Values))
		for _, item := range typed.ArrayValue.Values {
			items = append(items, valueToAny(item))
		}
		return items
	default:
		return nil
	}
}

func (c *collector) snapshot() ([]capturedSpan, []map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	spans := make([]capturedSpan, len(c.spans))
	copy(spans, c.spans)
	scores := make([]map[string]any, len(c.scores))
	copy(scores, c.scores)
	return spans, scores
}

func boolPtr(value bool) *bool {
	return &value
}

func testConfig(host string) config.TelemetryConfig {
	return config.TelemetryConfig{
		Provider:      "langfuse",
		Host:          host,
		PublicKey:     "pk-lf-test",
		SecretKey:     "sk-lf-test",
		Capture:       "full",
		RedactSecrets: boolPtr(true),
		FailOpen:      boolPtr(true),
	}
}

func TestLangfuseSendsOTLPTrace(t *testing.T) {
	col := &collector{}
	server := httptest.NewServer(http.HandlerFunc(col.handler))
	defer server.Close()

	client, err := newLangfuse(testConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fakeToken := "sk-" + strings.Repeat("a", 24)
	trace := client.Start(ctx, TraceInfo{
		ID:        "run-1",
		Name:      "aiw.run",
		Input:     map[string]any{"prompt": "token " + fakeToken},
		Metadata:  map[string]any{"engine": "fake"},
		Tags:      []string{"aiw"},
		SessionID: "run-1",
	})
	span := trace.Span("stage.requirements", "input do estágio")
	now := time.Now()
	span.Generation(Generation{
		Name:     "requirements",
		Model:    "model-x",
		Input:    "prompt",
		Output:   "output",
		Usage:    Usage{InputTokens: 10, OutputTokens: 5, CostUSD: 0.01},
		Metadata: map[string]any{"engine": "fake"},
		Start:    now,
		End:      now.Add(time.Second),
	})
	span.Event("gate.approved", map[string]any{"gate": "spec"})
	span.End("saída", nil)
	trace.Score("success", 1, "ok")
	trace.Event("run.event", map[string]any{"a": 1})
	trace.End(map[string]any{"status": "succeeded", "cost_usd": 0.25, "input_tokens": 100, "output_tokens": 50}, nil)

	if err := client.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	spans, scores := col.snapshot()
	if len(spans) == 0 {
		t.Fatal("nenhum span enviado")
	}
	col.mu.Lock()
	auth := col.authHeader
	version := col.ingestionVersion
	path := col.tracePath
	col.mu.Unlock()
	if !strings.HasPrefix(auth, "Basic ") {
		t.Fatalf("Authorization = %q", auth)
	}
	if version != "4" {
		t.Fatalf("x-langfuse-ingestion-version = %q", version)
	}
	if path != "/api/public/otel/v1/traces" {
		t.Fatalf("path = %q", path)
	}

	var root, generation, event *capturedSpan
	for i := range spans {
		span := &spans[i]
		if span.Name == "aiw.run" {
			root = span
		}
		if span.Name == "requirements" {
			generation = span
		}
		if span.Name == "gate.approved" {
			event = span
		}
	}
	if root == nil {
		t.Fatalf("span raiz ausente: %v", spanNames(spans))
	}
	if generation == nil {
		t.Fatalf("generation ausente: %v", spanNames(spans))
	}
	if event == nil {
		t.Fatalf("event span ausente: %v", spanNames(spans))
	}

	if root.Attrs["langfuse.trace.name"] != "aiw.run" {
		t.Fatalf("trace name = %v", root.Attrs["langfuse.trace.name"])
	}
	if root.Attrs["langfuse.session.id"] != "run-1" {
		t.Fatalf("session id = %v", root.Attrs["langfuse.session.id"])
	}
	if root.Attrs["langfuse.trace.metadata.engine"] != "fake" {
		t.Fatalf("metadata engine = %v", root.Attrs["langfuse.trace.metadata.engine"])
	}
	if root.Attrs["gen_ai.usage.cost"] != 0.25 {
		t.Fatalf("custo da raiz = %v", root.Attrs["gen_ai.usage.cost"])
	}
	if root.Attrs["gen_ai.usage.input_tokens"] != int64(100) {
		t.Fatalf("tokens da raiz = %v", root.Attrs["gen_ai.usage.input_tokens"])
	}
	input, _ := root.Attrs["langfuse.observation.input"].(string)
	if strings.Contains(input, fakeToken) {
		t.Fatalf("segredo não redigido: %q", input)
	}

	if generation.Attrs["langfuse.observation.type"] != "generation" {
		t.Fatalf("tipo = %v", generation.Attrs["langfuse.observation.type"])
	}
	if generation.Attrs["langfuse.observation.model.name"] != "model-x" {
		t.Fatalf("modelo = %v", generation.Attrs["langfuse.observation.model.name"])
	}
	if generation.Attrs["gen_ai.usage.input_tokens"] != int64(10) {
		t.Fatalf("input tokens = %v", generation.Attrs["gen_ai.usage.input_tokens"])
	}
	if generation.Attrs["gen_ai.usage.output_tokens"] != int64(5) {
		t.Fatalf("output tokens = %v", generation.Attrs["gen_ai.usage.output_tokens"])
	}
	if generation.Attrs["gen_ai.usage.cost"] != 0.01 {
		t.Fatalf("cost = %v", generation.Attrs["gen_ai.usage.cost"])
	}
	if event.Attrs["langfuse.observation.type"] != "event" {
		t.Fatalf("evento tipo = %v", event.Attrs["langfuse.observation.type"])
	}

	if len(scores) < 1 {
		t.Fatalf("scores = %d", len(scores))
	}
	if scores[0]["name"] != "success" || scores[0]["value"] != float64(1) {
		t.Fatalf("score inesperado: %+v", scores[0])
	}
	if scores[0]["traceId"] == "" {
		t.Fatalf("score sem traceId: %+v", scores[0])
	}
}

func spanNames(spans []capturedSpan) []string {
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name)
	}
	return names
}

func TestLangfuseMetadataOnly(t *testing.T) {
	col := &collector{}
	server := httptest.NewServer(http.HandlerFunc(col.handler))
	defer server.Close()

	cfg := testConfig(server.URL)
	cfg.Capture = "metadata"
	client, err := newLangfuse(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	trace := client.Start(ctx, TraceInfo{ID: "run-2", Name: "aiw.run", Input: "conteúdo sensível"})
	span := trace.Span("stage.requirements", "input sensível")
	span.Generation(Generation{Name: "requirements", Input: "prompt sensível", Output: "saída sensível"})
	span.End("saída", nil)
	trace.End(map[string]any{"status": "succeeded", "cost_usd": 0.25, "input_tokens": 100, "output_tokens": 50}, nil)
	if err := client.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	spans, _ := col.snapshot()
	for _, span := range spans {
		for _, key := range []string{"langfuse.observation.input", "langfuse.observation.output"} {
			if _, ok := span.Attrs[key]; ok {
				t.Fatalf("capture metadata não deveria ter %s em %s", key, span.Name)
			}
		}
	}
}

func TestLangfuseFailOpen(t *testing.T) {
	col := &collector{status: http.StatusInternalServerError}
	server := httptest.NewServer(http.HandlerFunc(col.handler))
	defer server.Close()

	client, err := newLangfuse(testConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	trace := client.Start(context.Background(), TraceInfo{ID: "run-3", Name: "aiw.run"})
	trace.End(nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Flush(ctx); err != nil {
		t.Fatalf("fail-open deveria engolir erro: %v", err)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer closeCancel()
	_ = client.Close(closeCtx)
}
