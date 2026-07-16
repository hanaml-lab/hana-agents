package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"time"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/worker"
)

// Client starts jobs and waits for them, with a pool of workers running them in-process.
type Client struct {
	Engine *Engine
	Queue  worker.Queue
}

// NewClient starts concurrency workers that run jobs from an in-process queue until ctx ends.
func NewClient(ctx context.Context, e *Engine, concurrency int) *Client {
	q := worker.NewMem()
	pool := &worker.Pool{Queue: q, Concurrency: concurrency, LeaseTTL: 90 * time.Second, Heartbeat: 15 * time.Second,
		Run: func(ctx context.Context, t worker.Task) error {
			_, err := e.Run(ctx, t.JobID)
			if errors.Is(err, ErrAwaitingApproval) || errors.Is(err, ErrJobFailed) {
				return nil // the journal holds the outcome; nothing to retry
			}
			return err
		}}
	go pool.Serve(ctx)
	return &Client{Engine: e, Queue: q}
}

func (c *Client) Start(ctx context.Context, job Job) (string, error) {
	id, err := c.Engine.Start(ctx, job)
	if err != nil {
		return "", err
	}
	return id, c.Queue.Put(ctx, worker.Task{JobID: id})
}

// Resume queues a job again, e.g. after an approval or a restart.
func (c *Client) Resume(ctx context.Context, id string) error {
	return c.Queue.Put(ctx, worker.Task{JobID: id})
}

// Approve records the approval and resumes the job.
func (c *Client) Approve(ctx context.Context, id, approver string) error {
	if err := c.Engine.Approve(ctx, id, approver); err != nil {
		return err
	}
	return c.Resume(ctx, id)
}

// Wait blocks until the job is done, failed or waiting for an approval.
func (c *Client) Wait(ctx context.Context, id string) (json.RawMessage, error) {
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		st, err := c.Engine.State(ctx, id)
		if err != nil {
			return nil, err
		}
		switch st.Status {
		case StatusDone:
			return st.Results[st.Plan[len(st.Plan)-1].ID], nil
		case StatusFailed:
			return nil, errors.Join(ErrJobFailed, errors.New(st.Error))
		case StatusWaiting:
			return nil, ErrAwaitingApproval
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewID returns a ULID: 48 bits of milliseconds and 80 random bits, so ids sort by start time.
func NewID() string {
	var b [16]byte
	ms := uint64(time.Now().UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	_, _ = rand.Read(b[6:])
	out := make([]byte, 26)
	// 128 bits as 26 base32 digits, most significant first
	var acc uint64
	bits, j := 0, 0
	acc, bits = 0, 2 // the leading digit carries the top 3 bits, padded to 5
	for _, x := range b {
		acc = acc<<8 | uint64(x)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out[j] = crockford[(acc>>uint(bits))&31]
			j++
		}
	}
	return string(out[:j])
}
