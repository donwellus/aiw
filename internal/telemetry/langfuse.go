package telemetry

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/donwellus/aiw/internal/config"
)

const maxFieldLength = 256 * 1024

type langfuse struct {
	capture  string
	redact   bool
	strict   bool
	provider *sdktrace.TracerProvider
	tracer   oteltrace.Tracer
	http     *http.Client
	scoreURL string
	user     string
	pass     string
	once     sync.Once
}

func newLangfuse(cfg config.TelemetryConfig) (*langfuse, error) {
	if strings.TrimSpace(cfg.PublicKey) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, fmt.Errorf("LANGFUSE_PUBLIC_KEY e LANGFUSE_SECRET_KEY são obrigatórios")
	}
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return nil, fmt.Errorf("host do Langfuse vazio")
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	parsed, err := url.Parse(host)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("host do Langfuse inválido: %q", cfg.Host)
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	auth := base64.StdEncoding.EncodeToString([]byte(cfg.PublicKey + ":" + cfg.SecretKey))

	options := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(parsed.Host),
		otlptracehttp.WithURLPath(basePath + "/api/public/otel/v1/traces"),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization":                "Basic " + auth,
			"x-langfuse-ingestion-version": "4",
		}),
		otlptracehttp.WithTimeout(15 * time.Second),
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{
			Enabled:         true,
			InitialInterval: 500 * time.Millisecond,
			MaxInterval:     5 * time.Second,
			MaxElapsedTime:  10 * time.Second,
		}),
	}
	if parsed.Scheme == "http" {
		options = append(options, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(context.Background(), options...)
	if err != nil {
		return nil, err
	}

	res := resource.NewWithAttributes(semconv.SchemaURL, attribute.String("service.name", "aiw"))
	if merged, mergeErr := resource.Merge(resource.Default(), res); mergeErr == nil {
		res = merged
	}
	processor := sdktrace.NewBatchSpanProcessor(exporter,
		sdktrace.WithBatchTimeout(2*time.Second),
		sdktrace.WithMaxExportBatchSize(512),
	)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(processor),
		sdktrace.WithResource(res),
	)
	return &langfuse{
		capture:  cfg.Capture,
		redact:   cfg.RedactEnabled(),
		strict:   !cfg.FailOpenEnabled(),
		provider: provider,
		tracer:   provider.Tracer("aiw"),
		http:     &http.Client{Timeout: 10 * time.Second},
		scoreURL: strings.TrimRight(host, "/") + "/api/public/scores",
		user:     cfg.PublicKey,
		pass:     cfg.SecretKey,
	}, nil
}

func (l *langfuse) Start(ctx context.Context, info TraceInfo) Trace {
	name := info.Name
	if name == "" {
		name = "aiw.run"
	}
	traceAttrs := []attribute.KeyValue{
		attribute.String("langfuse.trace.name", name),
	}
	if info.SessionID != "" {
		traceAttrs = append(traceAttrs, attribute.String("langfuse.session.id", info.SessionID))
	}
	if len(info.Tags) > 0 {
		traceAttrs = append(traceAttrs, attribute.StringSlice("langfuse.trace.tags", info.Tags))
	}
	for key, value := range info.Metadata {
		traceAttrs = append(traceAttrs, attribute.String("langfuse.trace.metadata."+sanitizeKey(key), l.metadataValue(value)))
	}

	ctx, span := l.tracer.Start(ctx, name)
	attrs := append([]attribute.KeyValue{}, traceAttrs...)
	attrs = append(attrs, attribute.String("langfuse.observation.type", "span"))
	if input := l.contentJSON(info.Input); input != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.input", input))
	}
	span.SetAttributes(attrs...)

	return &lfTrace{
		client:     l,
		ctx:        ctx,
		span:       span,
		id:         span.SpanContext().TraceID().String(),
		traceAttrs: traceAttrs,
	}
}

func (l *langfuse) Flush(ctx context.Context) error {
	if err := l.provider.ForceFlush(ctx); err != nil {
		if l.strict {
			return err
		}
		log.Printf("langfuse: falha ao enviar traces: %v", err)
	}
	return nil
}

func (l *langfuse) Close(ctx context.Context) error {
	var err error
	l.once.Do(func() {
		err = l.provider.Shutdown(ctx)
		if err != nil && !l.strict {
			log.Printf("langfuse: falha ao encerrar exporter: %v", err)
			err = nil
		}
	})
	return err
}

type lfTrace struct {
	client     *langfuse
	ctx        context.Context
	span       oteltrace.Span
	id         string
	traceAttrs []attribute.KeyValue
}

func (t *lfTrace) ID() string { return t.id }

func (t *lfTrace) Span(name string, input any) Span {
	ctx, span := t.client.tracer.Start(t.ctx, name)
	attrs := append([]attribute.KeyValue{}, t.traceAttrs...)
	attrs = append(attrs, attribute.String("langfuse.observation.type", "span"))
	if content := t.client.contentJSON(input); content != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.input", content))
	}
	span.SetAttributes(attrs...)
	return &lfSpan{client: t.client, ctx: ctx, span: span, traceAttrs: t.traceAttrs}
}

func (t *lfTrace) Event(name string, meta map[string]any) {
	_, span := t.client.tracer.Start(t.ctx, name)
	attrs := append([]attribute.KeyValue{}, t.traceAttrs...)
	attrs = append(attrs, attribute.String("langfuse.observation.type", "event"))
	for key, value := range meta {
		attrs = append(attrs, attribute.String("langfuse.observation.metadata."+sanitizeKey(key), t.client.metadataValue(value)))
	}
	span.SetAttributes(attrs...)
	span.End()
}

func (t *lfTrace) Score(name string, value float64, comment string) {
	body := map[string]any{
		"traceId":  t.id,
		"name":     name,
		"value":    value,
		"dataType": "NUMERIC",
	}
	if comment != "" {
		body["comment"] = t.client.sanitizeString(comment)
	}
	data, err := json.Marshal(body)
	if err != nil {
		log.Printf("langfuse: score %s inválido: %v", name, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.client.scoreURL, bytes.NewReader(data))
	if err != nil {
		log.Printf("langfuse: score %s: %v", name, err)
		return
	}
	req.SetBasicAuth(t.client.user, t.client.pass)
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.http.Do(req)
	if err != nil {
		log.Printf("langfuse: score %s: %v", name, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("langfuse: score %s rejeitado com status %d", name, resp.StatusCode)
	}
}

func (t *lfTrace) End(output any, err error) {
	if content := t.client.contentJSON(output); content != "" {
		t.span.SetAttributes(attribute.String("langfuse.observation.output", content))
	}
	if values, ok := output.(map[string]any); ok {
		var attrs []attribute.KeyValue
		if cost, ok := anyFloat(values["cost_usd"]); ok && cost != 0 {
			attrs = append(attrs, attribute.Float64("gen_ai.usage.cost", cost))
		}
		if tokens, ok := anyInt(values["input_tokens"]); ok && tokens != 0 {
			attrs = append(attrs, attribute.Int("gen_ai.usage.input_tokens", tokens))
		}
		if tokens, ok := anyInt(values["output_tokens"]); ok && tokens != 0 {
			attrs = append(attrs, attribute.Int("gen_ai.usage.output_tokens", tokens))
		}
		if len(attrs) > 0 {
			t.span.SetAttributes(attrs...)
		}
	}
	if err != nil {
		message := t.client.sanitizeString(err.Error())
		t.span.SetAttributes(
			attribute.String("langfuse.observation.level", "ERROR"),
			attribute.String("langfuse.observation.status_message", message),
		)
		t.span.SetStatus(codes.Error, message)
	}
	t.span.End()
}

type lfSpan struct {
	client     *langfuse
	ctx        context.Context
	span       oteltrace.Span
	traceAttrs []attribute.KeyValue
}

func (s *lfSpan) Generation(g Generation) {
	start := g.Start
	if start.IsZero() {
		start = time.Now()
	}
	end := g.End
	if end.IsZero() {
		end = time.Now()
	}
	_, span := s.client.tracer.Start(s.ctx, g.Name, oteltrace.WithTimestamp(start))
	attrs := append([]attribute.KeyValue{}, s.traceAttrs...)
	attrs = append(attrs, attribute.String("langfuse.observation.type", "generation"))
	if g.Model != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.model.name", g.Model))
	}
	if content := s.client.contentJSON(g.Input); content != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.input", content))
	}
	if content := s.client.contentJSON(g.Output); content != "" {
		attrs = append(attrs, attribute.String("langfuse.observation.output", content))
	}
	attrs = append(attrs,
		attribute.Int("gen_ai.usage.input_tokens", g.Usage.InputTokens),
		attribute.Int("gen_ai.usage.output_tokens", g.Usage.OutputTokens),
	)
	if g.Usage.CostUSD != 0 {
		attrs = append(attrs, attribute.Float64("gen_ai.usage.cost", g.Usage.CostUSD))
	}
	level := g.Level
	if level == "" {
		level = "DEFAULT"
	}
	attrs = append(attrs, attribute.String("langfuse.observation.level", level))
	for key, value := range g.Metadata {
		attrs = append(attrs, attribute.String("langfuse.observation.metadata."+sanitizeKey(key), s.client.metadataValue(value)))
	}
	span.SetAttributes(attrs...)
	if level == "ERROR" {
		span.SetStatus(codes.Error, "")
	}
	span.End(oteltrace.WithTimestamp(end))
}

func (s *lfSpan) Event(name string, meta map[string]any) {
	_, span := s.client.tracer.Start(s.ctx, name)
	attrs := append([]attribute.KeyValue{}, s.traceAttrs...)
	attrs = append(attrs, attribute.String("langfuse.observation.type", "event"))
	for key, value := range meta {
		attrs = append(attrs, attribute.String("langfuse.observation.metadata."+sanitizeKey(key), s.client.metadataValue(value)))
	}
	span.SetAttributes(attrs...)
	span.End()
}

func (s *lfSpan) End(output any, err error) {
	if content := s.client.contentJSON(output); content != "" {
		s.span.SetAttributes(attribute.String("langfuse.observation.output", content))
	}
	if err != nil {
		message := s.client.sanitizeString(err.Error())
		s.span.SetAttributes(
			attribute.String("langfuse.observation.level", "ERROR"),
			attribute.String("langfuse.observation.status_message", message),
		)
		s.span.SetStatus(codes.Error, message)
	}
	s.span.End()
}

func (l *langfuse) contentJSON(value any) string {
	if value == nil || l.capture == "metadata" {
		return ""
	}
	generic, err := toGeneric(value)
	if err != nil {
		return l.sanitizeString(fmt.Sprintf("%v", value))
	}
	generic = l.sanitizeValue(generic)
	if text, ok := generic.(string); ok {
		return text
	}
	data, err := json.Marshal(generic)
	if err != nil {
		return ""
	}
	return string(data)
}

func (l *langfuse) metadataValue(value any) string {
	generic, err := toGeneric(value)
	if err != nil {
		return l.sanitizeString(fmt.Sprintf("%v", value))
	}
	generic = l.sanitizeValue(generic)
	if text, ok := generic.(string); ok {
		return text
	}
	data, err := json.Marshal(generic)
	if err != nil {
		return ""
	}
	return string(data)
}

func (l *langfuse) sanitizeValue(value any) any {
	switch typed := value.(type) {
	case string:
		return l.sanitizeString(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = l.sanitizeValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = l.sanitizeValue(item)
		}
		return out
	default:
		return value
	}
}

func (l *langfuse) sanitizeString(value string) string {
	if l.redact {
		value = redactSecrets(value)
	}
	return truncateString(value, maxFieldLength)
}

func toGeneric(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	return generic, nil
}

func anyFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func anyInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	default:
		return 0, false
	}
}

func truncateString(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "...(truncado)"
}

func sanitizeKey(key string) string {
	var buf strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			buf.WriteRune(r)
		default:
			buf.WriteByte('_')
		}
	}
	out := buf.String()
	if out == "" {
		return "value"
	}
	return out
}

var (
	tokenPattern    = regexp.MustCompile(`(?i)\b(sk-[a-z0-9_-]{16,}|ghp_[a-z0-9]{20,}|github_pat_[a-z0-9_]{20,}|AKIA[0-9A-Z]{16})\b`)
	bearerPattern   = regexp.MustCompile(`(?i)(bearer\s+)([a-z0-9._\-]{16,})`)
	keyValuePattern = regexp.MustCompile(`(?i)((?:api[_-]?key|secret|token|password)\s*[:=]\s*)(["']?)([a-z0-9_\-\.]{8,})`)
)

func redactSecrets(value string) string {
	value = tokenPattern.ReplaceAllString(value, "[REDACTED]")
	value = bearerPattern.ReplaceAllString(value, "${1}[REDACTED]")
	value = keyValuePattern.ReplaceAllString(value, "${1}${2}[REDACTED]")
	return value
}
