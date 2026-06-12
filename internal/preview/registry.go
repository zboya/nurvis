// Package preview provides a shared registry mapping short-lived tokens to
// local directories for the web_preview tool. The tool registers a directory
// and receives a token; the HTTP handler resolves the token to serve static files.
//
// The registry lives in its own package so both the tool package and the
// gateway/http package can import it without a cycle.
package preview

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// DefaultTTL is how long a preview registration stays valid.
const DefaultTTL = 6 * time.Hour

type entry struct {
	dir       string
	expiresAt time.Time
}

// Registry maps opaque tokens to served directories with expiry.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]entry
	ttl     time.Duration
}

// NewRegistry creates a preview registry with the given TTL (0 → DefaultTTL).
func NewRegistry(ttl time.Duration) *Registry {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Registry{entries: make(map[string]entry), ttl: ttl}
}

// Register stores dir under a freshly generated token and returns the token.
// If dir is already registered (and unexpired), the existing token is reused so
// repeated previews of the same directory produce a stable URL.
func (r *Registry) Register(dir string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gcLocked()

	for tok, e := range r.entries {
		if e.dir == dir {
			e.expiresAt = time.Now().Add(r.ttl)
			r.entries[tok] = e
			return tok, nil
		}
	}

	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b[:])
	r.entries[tok] = entry{dir: dir, expiresAt: time.Now().Add(r.ttl)}
	return tok, nil
}

// Resolve returns the directory registered under token, or "" if absent/expired.
func (r *Registry) Resolve(token string) string {
	r.mu.RLock()
	e, ok := r.entries[token]
	r.mu.RUnlock()
	if !ok || time.Now().After(e.expiresAt) {
		return ""
	}
	return e.dir
}

// gcLocked removes expired entries. Caller must hold the write lock.
func (r *Registry) gcLocked() {
	now := time.Now()
	for tok, e := range r.entries {
		if now.After(e.expiresAt) {
			delete(r.entries, tok)
		}
	}
}
