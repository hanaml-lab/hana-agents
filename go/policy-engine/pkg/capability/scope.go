// Package capability implements scoped tokens: the actions and resources an agent may touch.
// Anything outside the scope is forbidden before policy runs.
package capability

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// Token lists what a principal may do. Patterns use path.Match globs, e.g. "invoices.*".
type Token struct {
	Principal string    `json:"sub"`
	Actions   []string  `json:"actions"`
	Resources []string  `json:"resources,omitempty"`
	Expires   time.Time `json:"exp"`
}

func anyMatch(patterns []string, s string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, s); ok {
			return true
		}
	}
	return false
}

// Allows reports why the token does not cover the action and resource, or nil if it does.
func (t *Token) Allows(action, resource string, now time.Time) error {
	if !t.Expires.IsZero() && now.After(t.Expires) {
		return errors.New("capability token expired")
	}
	if !anyMatch(t.Actions, action) {
		return fmt.Errorf("action %s is outside the token's scope", action)
	}
	if resource != "" && len(t.Resources) > 0 && !anyMatch(t.Resources, resource) {
		return fmt.Errorf("resource %s is outside the token's scope", resource)
	}
	return nil
}

// Sign encodes the token as payload.signature with HMAC-SHA256.
func Sign(t Token, key []byte) (string, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify checks the signature and decodes the token.
func Verify(s string, key []byte) (*Token, error) {
	payload, sig, ok := strings.Cut(s, ".")
	if !ok {
		return nil, errors.New("malformed token")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, want) {
		return nil, errors.New("bad token signature")
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}
	var t Token
	return &t, json.Unmarshal(b, &t)
}
