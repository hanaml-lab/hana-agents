// Package audit writes one JSON line per decision. Each line carries the hash of the previous
// line, so a deleted or edited entry breaks the chain and Verify finds it.
package audit

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

type Entry struct {
	At            time.Time      `json:"at"`
	Principal     string         `json:"principal"`
	Action        string         `json:"action"`
	Args          map[string]any `json:"args"`
	Outcome       string         `json:"outcome"`
	Rule          string         `json:"rule"`
	PolicyVersion string         `json:"policy_version"`
	Prev          string         `json:"prev"`
}

type Log struct {
	mu   sync.Mutex
	w    io.Writer
	prev string
}

func New(w io.Writer) *Log { return &Log{w: w, prev: "genesis"} }

func (l *Log) Write(e Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.Prev = l.prev
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := l.w.Write(append(b, '\n')); err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	l.prev = hex.EncodeToString(sum[:])
	return nil
}

// Verify reads a log and reports the first line whose chain is broken.
func Verify(r io.Reader) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	prev, n := "genesis", 0
	for sc.Scan() {
		n++
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return n, err
		}
		if e.Prev != prev {
			return n, fmt.Errorf("line %d: chain broken", n)
		}
		sum := sha256.Sum256(sc.Bytes())
		prev = hex.EncodeToString(sum[:])
	}
	return n, sc.Err()
}
