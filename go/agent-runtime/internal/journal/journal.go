// Package journal is the append-only record of everything a job did. Replaying it rebuilds
// the job's state without calling any tool, which is what makes a resume after a crash safe.
package journal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind names an event type.
type Kind string

const (
	JobStarted     Kind = "job_started"
	Planned        Kind = "planned"
	StepStarted    Kind = "step_started"
	PolicyDecided  Kind = "policy_decided"
	ApprovalNeeded Kind = "approval_needed"
	Approved       Kind = "approved"
	Rejected       Kind = "rejected"
	ToolCalled     Kind = "tool_called"
	ToolResult     Kind = "tool_result"
	ToolFailed     Kind = "tool_failed"
	StepDone       Kind = "step_done"
	StepReturned   Kind = "step_returned"
	JobDone        Kind = "job_done"
	JobFailed      Kind = "job_failed"
)

// Event is one journal row. Seq is assigned by the journal and is dense per job.
type Event struct {
	JobID   string          `json:"job_id"`
	Seq     int64           `json:"seq"`
	Kind    Kind            `json:"kind"`
	StepID  string          `json:"step_id,omitempty"`
	Attempt int             `json:"attempt,omitempty"`
	Key     string          `json:"key,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	At      time.Time       `json:"at"`
}

// ErrConflict is returned when two writers race to append the same sequence number.
var ErrConflict = errors.New("journal: sequence conflict")

// Journal stores events. Append must be atomic: a crash leaves either the whole event or none.
type Journal interface {
	Append(ctx context.Context, ev Event) (Event, error)
	Load(ctx context.Context, jobID string) ([]Event, error)
	Jobs(ctx context.Context) ([]string, error)
}

// Payload marshals v for an event, panicking only on values that cannot be JSON.
func Payload(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("journal payload: %v", err))
	}
	return b
}

// Mem is an in-process journal, for tests and single-process runs.
type Mem struct {
	mu   sync.Mutex
	jobs map[string][]Event
}

func NewMem() *Mem { return &Mem{jobs: map[string][]Event{}} }

func (m *Mem) Append(_ context.Context, ev Event) (Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ev.Seq = int64(len(m.jobs[ev.JobID]) + 1)
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	m.jobs[ev.JobID] = append(m.jobs[ev.JobID], ev)
	return ev, nil
}

func (m *Mem) Load(_ context.Context, jobID string) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Event(nil), m.jobs[jobID]...), nil
}

func (m *Mem) Jobs(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.jobs))
	for id := range m.jobs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// File keeps one JSON Lines file per job and fsyncs every append.
type File struct {
	dir string
	mu  sync.Mutex
	seq map[string]int64
}

func NewFile(dir string) (*File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &File{dir: dir, seq: map[string]int64{}}, nil
}

func (f *File) path(jobID string) string { return filepath.Join(f.dir, jobID+".jsonl") }

func (f *File) Append(ctx context.Context, ev Event) (Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.seq[ev.JobID]; !ok {
		evs, err := f.load(ev.JobID)
		if err != nil {
			return ev, err
		}
		f.seq[ev.JobID] = int64(len(evs))
	}
	f.seq[ev.JobID]++
	ev.Seq = f.seq[ev.JobID]
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return ev, err
	}
	fh, err := os.OpenFile(f.path(ev.JobID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return ev, err
	}
	defer fh.Close()
	if _, err := fh.Write(append(b, '\n')); err != nil {
		return ev, err
	}
	return ev, fh.Sync()
}

func (f *File) load(jobID string) ([]Event, error) {
	fh, err := os.Open(f.path(jobID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var out []Event
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			// a torn final line from a crash mid-write is ignored; anything else is corruption
			if len(out) > 0 && !sc.Scan() {
				break
			}
			return nil, fmt.Errorf("journal %s: %w", jobID, err)
		}
		out = append(out, ev)
	}
	return out, sc.Err()
}

func (f *File) Load(_ context.Context, jobID string) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load(jobID)
}

func (f *File) Jobs(context.Context) ([]string, error) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".jsonl"); ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}
