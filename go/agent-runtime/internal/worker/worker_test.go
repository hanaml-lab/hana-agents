package worker

import (
	"context"
	"testing"
	"time"
)

func TestExpiredLeaseIsTakenOver(t *testing.T) {
	ctx := context.Background()
	q := NewMem()
	now := time.Unix(0, 0)
	q.now = func() time.Time { return now }
	_ = q.Put(ctx, Task{JobID: "j"})
	l1, _ := q.Lease(ctx, 90*time.Second)
	if _, ok := q.tryLease(90 * time.Second); ok {
		t.Fatal("task leased twice")
	}
	now = now.Add(60 * time.Second)
	if _, err := q.Heartbeat(ctx, l1, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	now = now.Add(100 * time.Second) // the first worker stopped heartbeating
	l2, ok := q.tryLease(90 * time.Second)
	if !ok || l2.ID == l1.ID {
		t.Fatal("expired lease not taken over")
	}
	if _, err := q.Heartbeat(ctx, l1, time.Second); err != ErrLeaseLost {
		t.Fatalf("old worker kept its lease: %v", err)
	}
	if err := q.Ack(ctx, l2); err != nil {
		t.Fatal(err)
	}
}
