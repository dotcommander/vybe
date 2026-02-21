package memory

import "time"

// Store is a scoped key-value store with optional TTL.
type Store interface {
	Set(scope, scopeID, key, value string, opts ...Option) error
	Get(scope, scopeID, key string) (Entry, bool)
	Delete(scope, scopeID, key string) bool
	List(scope, scopeID string) []Entry
	Len() int
}

// Entry represents a stored key-value pair.
type Entry struct {
	Key       string     `json:"key"`
	Value     string     `json:"value"`
	Scope     string     `json:"scope"`
	ScopeID   string     `json:"scope_id"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type setOptions struct {
	ttl time.Duration
}

// Option configures a Set operation.
type Option func(*setOptions)

// WithTTL sets a time-to-live on the entry.
func WithTTL(d time.Duration) Option {
	return func(o *setOptions) {
		o.ttl = d
	}
}
