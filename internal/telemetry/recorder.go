package telemetry

import (
	"context"
	"sync"
)

type RecordedEvent struct {
	Name string
	Meta map[string]any
}

type RecordedScore struct {
	Name    string
	Value   float64
	Comment string
}

type RecordedSpan struct {
	Name        string
	Input       any
	Generations []Generation
	Events      []RecordedEvent
	Output      any
	Err         error
	Ended       bool
}

type RecordedTrace struct {
	Info   TraceInfo
	Spans  []*RecordedSpan
	Events []RecordedEvent
	Scores []RecordedScore
	Output any
	Err    error
	Ended  bool
}

type Recorder struct {
	mu     sync.Mutex
	traces []*RecordedTrace
}

func NewRecorder() *Recorder {
	return &Recorder{}
}

func (r *Recorder) Start(ctx context.Context, info TraceInfo) Trace {
	recorded := &RecordedTrace{Info: info}
	r.mu.Lock()
	r.traces = append(r.traces, recorded)
	r.mu.Unlock()
	return &recordedTrace{recorder: r, trace: recorded}
}

func (r *Recorder) Flush(ctx context.Context) error { return nil }
func (r *Recorder) Close(ctx context.Context) error { return nil }

func (r *Recorder) Traces() []*RecordedTrace {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*RecordedTrace, len(r.traces))
	copy(out, r.traces)
	return out
}

type recordedTrace struct {
	recorder *Recorder
	trace    *RecordedTrace
}

func (t *recordedTrace) ID() string { return t.trace.Info.ID }

func (t *recordedTrace) Span(name string, input any) Span {
	span := &RecordedSpan{Name: name, Input: input}
	t.recorder.mu.Lock()
	t.trace.Spans = append(t.trace.Spans, span)
	t.recorder.mu.Unlock()
	return &recordedSpan{recorder: t.recorder, trace: t.trace, span: span}
}

func (t *recordedTrace) Event(name string, meta map[string]any) {
	t.recorder.mu.Lock()
	t.trace.Events = append(t.trace.Events, RecordedEvent{Name: name, Meta: meta})
	t.recorder.mu.Unlock()
}

func (t *recordedTrace) Score(name string, value float64, comment string) {
	t.recorder.mu.Lock()
	t.trace.Scores = append(t.trace.Scores, RecordedScore{Name: name, Value: value, Comment: comment})
	t.recorder.mu.Unlock()
}

func (t *recordedTrace) End(output any, err error) {
	t.recorder.mu.Lock()
	t.trace.Output = output
	t.trace.Err = err
	t.trace.Ended = true
	t.recorder.mu.Unlock()
}

type recordedSpan struct {
	recorder *Recorder
	trace    *RecordedTrace
	span     *RecordedSpan
}

func (s *recordedSpan) Generation(g Generation) {
	s.recorder.mu.Lock()
	s.span.Generations = append(s.span.Generations, g)
	s.recorder.mu.Unlock()
}

func (s *recordedSpan) Event(name string, meta map[string]any) {
	s.recorder.mu.Lock()
	s.span.Events = append(s.span.Events, RecordedEvent{Name: name, Meta: meta})
	s.recorder.mu.Unlock()
}

func (s *recordedSpan) End(output any, err error) {
	s.recorder.mu.Lock()
	s.span.Output = output
	s.span.Err = err
	s.span.Ended = true
	s.recorder.mu.Unlock()
}
