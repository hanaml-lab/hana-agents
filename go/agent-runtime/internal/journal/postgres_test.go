package journal

import (
	"context"
	"os"
	"testing"
)

// TestPostgres runs against a database with migrations/0001_journal.sql applied. It needs
// JOURNAL_TEST_DSN; CI provides one from a Postgres service container.
func TestPostgres(t *testing.T) {
	dsn := os.Getenv("JOURNAL_TEST_DSN")
	if dsn == "" {
		t.Skip("JOURNAL_TEST_DSN not set")
	}
	ctx := context.Background()
	p, err := OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	job := "pg-" + t.Name()
	for _, k := range []Kind{JobStarted, Planned, ToolResult} {
		if _, err := p.Append(ctx, Event{JobID: job, Kind: k, Key: "k", Payload: Payload(map[string]int{"n": 1})}); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := p.Load(ctx, job)
	if err != nil || len(evs) != 3 || evs[2].Seq != 3 || string(evs[2].Payload) != `{"n": 1}` {
		t.Fatalf("evs=%+v err=%v", evs, err)
	}
}
