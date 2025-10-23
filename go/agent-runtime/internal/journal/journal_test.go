package journal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileJournalSurvivesReopenAndTornLine(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	j, err := NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []Kind{JobStarted, Planned, StepStarted} {
		if _, err := j.Append(ctx, Event{JobID: "job1", Kind: k}); err != nil {
			t.Fatal(err)
		}
	}
	// a crash in the middle of a write leaves half a line behind
	f, _ := os.OpenFile(filepath.Join(dir, "job1.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"job_id":"job1","seq":4,"ki`)
	f.Close()

	j2, _ := NewFile(dir)
	evs, err := j2.Load(ctx, "job1")
	if err != nil || len(evs) != 3 || evs[2].Seq != 3 {
		t.Fatalf("evs=%d err=%v", len(evs), err)
	}
	jobs, _ := j2.Jobs(ctx)
	if len(jobs) != 1 || jobs[0] != "job1" {
		t.Fatalf("jobs=%v", jobs)
	}
}
