package memory

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetGetDelete(t *testing.T) {
	s := NewLRU(10)

	// Set and Get basic entry.
	require.NoError(t, s.Set("global", "", "foo", "bar"))

	entry, ok := s.Get("global", "", "foo")
	require.True(t, ok)
	assert.Equal(t, "foo", entry.Key)
	assert.Equal(t, "bar", entry.Value)
	assert.Equal(t, "global", entry.Scope)
	assert.Equal(t, "", entry.ScopeID)
	assert.Nil(t, entry.ExpiresAt)
	assert.False(t, entry.CreatedAt.IsZero())
	assert.False(t, entry.UpdatedAt.IsZero())

	// Overwrite value.
	require.NoError(t, s.Set("global", "", "foo", "baz"))
	entry, ok = s.Get("global", "", "foo")
	require.True(t, ok)
	assert.Equal(t, "baz", entry.Value)

	// Get missing key.
	_, ok = s.Get("global", "", "missing")
	assert.False(t, ok)

	// Delete existing key.
	deleted := s.Delete("global", "", "foo")
	assert.True(t, deleted)

	_, ok = s.Get("global", "", "foo")
	assert.False(t, ok)

	// Delete non-existent key returns false.
	deleted = s.Delete("global", "", "nonexistent")
	assert.False(t, deleted)
}

func TestLRUEviction(t *testing.T) {
	s := NewLRU(3)

	// Insert 3 entries (at capacity).
	require.NoError(t, s.Set("task", "t1", "a", "1"))
	require.NoError(t, s.Set("task", "t1", "b", "2"))
	require.NoError(t, s.Set("task", "t1", "c", "3"))
	assert.Equal(t, 3, s.Len())

	// Access "a" to make it most recently used (order: a, c, b).
	_, ok := s.Get("task", "t1", "a")
	require.True(t, ok)

	// Insert 4th entry — "b" is least recently used and should be evicted.
	require.NoError(t, s.Set("task", "t1", "d", "4"))
	assert.Equal(t, 3, s.Len())

	_, ok = s.Get("task", "t1", "b")
	assert.False(t, ok, "b should have been evicted as LRU")

	_, ok = s.Get("task", "t1", "a")
	assert.True(t, ok)
	_, ok = s.Get("task", "t1", "c")
	assert.True(t, ok)
	_, ok = s.Get("task", "t1", "d")
	assert.True(t, ok)
}

func TestTTLExpiry(t *testing.T) {
	s := NewLRU(10)

	require.NoError(t, s.Set("global", "", "short", "lived", WithTTL(50*time.Millisecond)))
	require.NoError(t, s.Set("global", "", "long", "lasting", WithTTL(10*time.Second)))

	// Both accessible before TTL.
	_, ok := s.Get("global", "", "short")
	require.True(t, ok)
	_, ok = s.Get("global", "", "long")
	require.True(t, ok)

	// Wait for short TTL to expire.
	time.Sleep(80 * time.Millisecond)

	_, ok = s.Get("global", "", "short")
	assert.False(t, ok, "short entry should have expired")

	_, ok = s.Get("global", "", "long")
	assert.True(t, ok, "long entry should still be valid")
}

func TestScopeIsolation(t *testing.T) {
	s := NewLRU(2)

	// Same key in different scopes must not interfere.
	require.NoError(t, s.Set("task", "t1", "key", "t1-value"))
	require.NoError(t, s.Set("task", "t2", "key", "t2-value"))
	require.NoError(t, s.Set("global", "", "key", "global-value"))

	e1, ok := s.Get("task", "t1", "key")
	require.True(t, ok)
	assert.Equal(t, "t1-value", e1.Value)

	e2, ok := s.Get("task", "t2", "key")
	require.True(t, ok)
	assert.Equal(t, "t2-value", e2.Value)

	eg, ok := s.Get("global", "", "key")
	require.True(t, ok)
	assert.Equal(t, "global-value", eg.Value)

	// Eviction in t1 must not affect t2.
	require.NoError(t, s.Set("task", "t1", "key2", "v2"))
	require.NoError(t, s.Set("task", "t1", "key3", "v3")) // "key" should be evicted from t1

	_, ok = s.Get("task", "t1", "key")
	assert.False(t, ok, "key in t1 should be evicted")

	_, ok = s.Get("task", "t2", "key")
	assert.True(t, ok, "key in t2 must survive eviction in t1")
}

func TestConcurrentAccess(t *testing.T) {
	s := NewLRU(100)
	const goroutines = 20
	const ops = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			scope := fmt.Sprintf("agent%d", id%5)
			for j := range ops {
				key := fmt.Sprintf("k%d", j%10)
				val := fmt.Sprintf("v%d-%d", id, j)
				_ = s.Set(scope, "", key, val)
				_, _ = s.Get(scope, "", key)
				if j%7 == 0 {
					s.Delete(scope, "", key)
				}
			}
		}(i)
	}

	wg.Wait()
	// No race detector errors or panics is the primary assertion.
	assert.GreaterOrEqual(t, s.Len(), 0)
}

func TestListOrder(t *testing.T) {
	s := NewLRU(10)

	require.NoError(t, s.Set("proj", "p1", "first", "1"))
	require.NoError(t, s.Set("proj", "p1", "second", "2"))
	require.NoError(t, s.Set("proj", "p1", "third", "3"))

	// Access "first" to make it most recently used.
	_, ok := s.Get("proj", "p1", "first")
	require.True(t, ok)

	entries := s.List("proj", "p1")
	require.Len(t, entries, 3)

	// Most recently used ("first") should be at index 0.
	assert.Equal(t, "first", entries[0].Key)
	// "third" was inserted after "second" and not accessed since, making it next.
	assert.Equal(t, "third", entries[1].Key)
	assert.Equal(t, "second", entries[2].Key)

	// List on empty scope returns nil.
	assert.Nil(t, s.List("proj", "nosuchscope"))
}

func TestUpdateMovesToFront(t *testing.T) {
	s := NewLRU(3)

	require.NoError(t, s.Set("global", "", "a", "1"))
	require.NoError(t, s.Set("global", "", "b", "2"))
	require.NoError(t, s.Set("global", "", "c", "3"))
	// Order: c(front), b, a(back)

	// Update "a" — should move to front, total count stays at 3.
	require.NoError(t, s.Set("global", "", "a", "updated"))
	assert.Equal(t, 3, s.Len())

	entries := s.List("global", "")
	require.Len(t, entries, 3)
	assert.Equal(t, "a", entries[0].Key, "updated entry should be at front")
	assert.Equal(t, "updated", entries[0].Value)

	// Insert new entry — "b" (now LRU after "a" moved to front) should be evicted, not "a".
	require.NoError(t, s.Set("global", "", "d", "4"))
	assert.Equal(t, 3, s.Len())

	_, ok := s.Get("global", "", "a")
	assert.True(t, ok, "a must survive — it was recently updated")
	_, ok = s.Get("global", "", "b")
	assert.False(t, ok, "b should be evicted as LRU")
}
