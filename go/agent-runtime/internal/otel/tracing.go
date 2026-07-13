// Package otel records one span per step, linked to the job's root trace, in the
// OpenTelemetry data model (W3C trace and span ids). Spans are written as JSON lines that an
// OpenTelemetry Collector filelog receiver can ingest, so the runtime needs no SDK at build time.
package otel

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sync"
	"time"
)

type Span struct {
	TraceID    string            `json:"trace_id"`
	SpanID     string            `json:"span_id"`
	ParentID   string            `json:"parent_span_id,omitempty"`
	Name       string            `json:"name"`
	Start      time.Time         `json:"start_time"`
	End        time.Time         `json:"end_time"`
	Status     string            `json:"status"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// TraceID is derived from the job id, so every process that works on a job, before and after
// a crash, reports into the same trace.
func TraceID(jobID string) string {
	sum := sha256.Sum256([]byte("trace:" + jobID))
	return hex.EncodeToString(sum[:16])
}

func RootSpanID(jobID string) string {
	sum := sha256.Sum256([]byte("root:" + jobID))
	return hex.EncodeToString(sum[:8])
}

func newSpanID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Tracer writes finished spans. A nil writer discards them.
type Tracer struct {
	mu sync.Mutex
	w  io.Writer
}

func New(w io.Writer) *Tracer { return &Tracer{w: w} }

// StepSpan starts a span for one step of a job; call the returned function to end it.
func (t *Tracer) StepSpan(jobID, name string, attrs map[string]string) func(status string) {
	s := Span{TraceID: TraceID(jobID), SpanID: newSpanID(), ParentID: RootSpanID(jobID), Name: name,
		Start: time.Now().UTC(), Attributes: attrs}
	return func(status string) {
		s.End, s.Status = time.Now().UTC(), status
		t.emit(s)
	}
}

func (t *Tracer) emit(s Span) {
	if t == nil || t.w == nil {
		return
	}
	b, _ := json.Marshal(s)
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = t.w.Write(append(b, '\n'))
}
