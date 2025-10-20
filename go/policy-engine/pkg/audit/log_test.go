package audit

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestChainDetectsEdits(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	for _, o := range []string{"allow", "hold", "forbid"} {
		if err := l.Write(Entry{At: time.Unix(0, 0).UTC(), Action: "payments.send", Outcome: o, Rule: "r"}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := Verify(bytes.NewReader(buf.Bytes())); err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	edited := strings.Replace(buf.String(), `"outcome":"hold"`, `"outcome":"allow"`, 1)
	if _, err := Verify(strings.NewReader(edited)); err == nil {
		t.Fatal("edited log verified")
	}
}
