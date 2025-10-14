// Package approval binds a person's approval to a digest of the exact arguments, so approving
// a payment of 4,200 to one account does not approve 4,300 to another.
package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Digest is a SHA-256 over the canonical JSON of principal, action and arguments.
// encoding/json sorts map keys, which makes the encoding canonical for JSON-like values.
func Digest(principal, action string, args map[string]any) (string, error) {
	b, err := json.Marshal(struct {
		P string         `json:"p"`
		A string         `json:"a"`
		X map[string]any `json:"x"`
	}{principal, action, args})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Approval records who approved which digest, until when.
type Approval struct {
	Digest   string    `json:"digest"`
	Approver string    `json:"approver"`
	Expires  time.Time `json:"expires"`
}

var (
	ErrNotApproved = errors.New("no approval for these exact arguments")
	ErrExpired     = errors.New("approval expired")
)

// Store keeps pending approvals; each can be used once.
type Store struct {
	mu   sync.Mutex
	byID map[string]Approval
}

func NewStore() *Store { return &Store{byID: map[string]Approval{}} }

func (s *Store) Approve(a Approval) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[a.Digest] = a
}

// Consume uses up the approval for digest at time now.
func (s *Store) Consume(digest string, now time.Time) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[digest]
	if !ok {
		return Approval{}, ErrNotApproved
	}
	delete(s.byID, digest)
	if !a.Expires.IsZero() && now.After(a.Expires) {
		return a, ErrExpired
	}
	return a, nil
}
