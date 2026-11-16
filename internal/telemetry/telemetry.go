package telemetry

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/donwellus/aiw/internal/config"
)

type Usage struct {
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

type TraceInfo struct {
	ID        string
	Name      string
	Input     any
	Metadata  map[string]any
	Tags      []string
	SessionID string
}

type Generation struct {
	Name     string
	Model    string
	Input    any
	Output   any
	Usage    Usage
	Metadata map[string]any
	Level    string
	Start    time.Time
	End      time.Time
}

type Client interface {
	Start(ctx context.Context, info TraceInfo) Trace
	Flush(ctx context.Context) error
	Close(ctx context.Context) error
}

type Trace interface {
	ID() string
	Span(name string, input any) Span
	Event(name string, meta map[string]any)
	Score(name string, value float64, comment string)
	End(output any, err error)
}

type Span interface {
	Generation(g Generation)
	Event(name string, meta map[string]any)
	End(output any, err error)
}

func New(cfg config.TelemetryConfig) Client {
	switch cfg.Provider {
	case "", "noop":
		return Noop{}
	case "langfuse":
		client, err := newLangfuse(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "aviso: telemetria Langfuse desativada: %v\n", err)
			return Noop{}
		}
		return client
	default:
		fmt.Fprintf(os.Stderr, "aviso: provider de telemetria %q desconhecido, usando noop\n", cfg.Provider)
		return Noop{}
	}
}

type Noop struct{}

func (Noop) Start(ctx context.Context, info TraceInfo) Trace { return noopTrace{} }
func (Noop) Flush(ctx context.Context) error                 { return nil }
func (Noop) Close(ctx context.Context) error                 { return nil }

type noopTrace struct{}

func (noopTrace) ID() string                                       { return "" }
func (noopTrace) Span(name string, input any) Span                 { return noopSpan{} }
func (noopTrace) Event(name string, meta map[string]any)           {}
func (noopTrace) Score(name string, value float64, comment string) {}
func (noopTrace) End(output any, err error)                        {}

type noopSpan struct{}

func (noopSpan) Generation(g Generation)                {}
func (noopSpan) Event(name string, meta map[string]any) {}
func (noopSpan) End(output any, err error)              {}
