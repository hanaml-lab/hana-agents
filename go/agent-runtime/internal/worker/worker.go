// Package worker leases steps from a queue and runs them. A lease must be renewed by
// heartbeats; a worker that dies stops renewing, its lease expires, and another worker picks
// the step up and resumes it from the journal.
package worker

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Task is a unit of work on the queue: run job JobID forward.
type Task struct {
	JobID string
}

// Lease is a worker's claim on a task until Expires.
type Lease struct {
	Task    Task
	ID      uint64
	Expires time.Time
}

// Queue hands out leases. Implementations: Mem (in process). A distributed queue only needs
// the same three operations.
type Queue interface {
	Put(ctx context.Context, t Task) error
	Lease(ctx context.Context, ttl time.Duration) (Lease, error)
	Heartbeat(ctx context.Context, l Lease, ttl time.Duration) (Lease, error)
	Ack(ctx context.Context, l Lease) error
}

var ErrLeaseLost = errors.New("worker: lease expired or taken")

type entry struct {
	task    Task
	lease   uint64
	expires time.Time
}

// Mem is an in-process queue with lease expiry.
type Mem struct {
	mu     sync.Mutex
	items  []*entry
	nextID uint64
	now    func() time.Time
	wake   chan struct{}
}

func NewMem() *Mem { return &Mem{now: time.Now, wake: make(chan struct{}, 1)} }

func (q *Mem) Put(_ context.Context, t Task) error {
	q.mu.Lock()
	q.items = append(q.items, &entry{task: t})
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return nil
}

func (q *Mem) tryLease(ttl time.Duration) (Lease, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
	for _, e := range q.items {
		if e.lease == 0 || now.After(e.expires) {
			q.nextID++
			e.lease, e.expires = q.nextID, now.Add(ttl)
			return Lease{e.task, e.lease, e.expires}, true
		}
	}
	return Lease{}, false
}

// Lease blocks until a task is free or ctx ends.
func (q *Mem) Lease(ctx context.Context, ttl time.Duration) (Lease, error) {
	for {
		if l, ok := q.tryLease(ttl); ok {
			return l, nil
		}
		select {
		case <-ctx.Done():
			return Lease{}, ctx.Err()
		case <-q.wake:
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (q *Mem) find(l Lease) *entry {
	for _, e := range q.items {
		if e.lease == l.ID && e.task == l.Task {
			return e
		}
	}
	return nil
}

func (q *Mem) Heartbeat(_ context.Context, l Lease, ttl time.Duration) (Lease, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e := q.find(l)
	if e == nil || q.now().After(e.expires) {
		return l, ErrLeaseLost
	}
	e.expires = q.now().Add(ttl)
	l.Expires = e.expires
	return l, nil
}

func (q *Mem) Ack(_ context.Context, l Lease) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, e := range q.items {
		if e.lease == l.ID && e.task == l.Task {
			q.items = append(q.items[:i], q.items[i+1:]...)
			return nil
		}
	}
	return ErrLeaseLost
}

// Pool runs tasks from a queue with a fixed number of workers.
type Pool struct {
	Queue       Queue
	Concurrency int
	LeaseTTL    time.Duration
	Heartbeat   time.Duration
	Run         func(ctx context.Context, t Task) error
	OnError     func(t Task, err error)
}

// Serve runs until ctx ends. A task whose Run returns an error is left unacknowledged, so its
// lease expires and the task is retried.
func (p *Pool) Serve(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < max(1, p.Concurrency); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				l, err := p.Queue.Lease(ctx, p.LeaseTTL)
				if err != nil {
					continue
				}
				p.runOne(ctx, l)
			}
		}()
	}
	wg.Wait()
}

func (p *Pool) runOne(ctx context.Context, l Lease) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(runCtx, l.Task) }()
	tick := time.NewTicker(p.Heartbeat)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				if p.OnError != nil {
					p.OnError(l.Task, err)
				}
				return
			}
			_ = p.Queue.Ack(ctx, l)
			return
		case <-tick.C:
			var err error
			if l, err = p.Queue.Heartbeat(ctx, l, p.LeaseTTL); err != nil {
				cancel() // someone else owns the task now; stop before writing anything more
				<-done
				return
			}
		}
	}
}
