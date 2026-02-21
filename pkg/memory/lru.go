package memory

import (
	"container/list"
	"sync"
	"time"
)

type lruStore struct {
	mu                 sync.Mutex
	maxEntriesPerScope int
	// scopeLists maps scopeKey -> LRU list of *Entry (front = most recent)
	scopeLists map[string]*list.List
	// elements maps entryKey -> *list.Element for O(1) lookup
	elements map[string]*list.Element
}

// NewLRU returns a Store backed by a per-scope LRU eviction policy.
// maxEntriesPerScope is the maximum number of entries retained per (scope, scopeID) pair.
func NewLRU(maxEntriesPerScope int) Store {
	return &lruStore{
		maxEntriesPerScope: maxEntriesPerScope,
		scopeLists:         make(map[string]*list.List),
		elements:           make(map[string]*list.Element),
	}
}

func scopeKey(scope, scopeID string) string {
	return scope + "\x00" + scopeID
}

func entryKey(scope, scopeID, key string) string {
	return scope + "\x00" + scopeID + "\x00" + key
}

func (s *lruStore) Set(scope, scopeID, key, value string, opts ...Option) error {
	o := &setOptions{}
	for _, opt := range opts {
		opt(o)
	}

	now := time.Now()
	var expiresAt *time.Time
	if o.ttl > 0 {
		t := now.Add(o.ttl)
		expiresAt = &t
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sk := scopeKey(scope, scopeID)
	ek := entryKey(scope, scopeID, key)

	if elem, ok := s.elements[ek]; ok {
		// Update existing entry and move to front.
		e := elem.Value.(*Entry)
		e.Value = value
		e.ExpiresAt = expiresAt
		e.UpdatedAt = now
		s.scopeLists[sk].MoveToFront(elem)
		return nil
	}

	// New entry.
	entry := &Entry{
		Key:       key,
		Value:     value,
		Scope:     scope,
		ScopeID:   scopeID,
		ExpiresAt: expiresAt,
		UpdatedAt: now,
		CreatedAt: now,
	}

	l, ok := s.scopeLists[sk]
	if !ok {
		l = list.New()
		s.scopeLists[sk] = l
	}

	// Evict from back when at capacity.
	if l.Len() >= s.maxEntriesPerScope {
		back := l.Back()
		if back != nil {
			evicted := l.Remove(back).(*Entry)
			delete(s.elements, entryKey(evicted.Scope, evicted.ScopeID, evicted.Key))
		}
	}

	elem := l.PushFront(entry)
	s.elements[ek] = elem
	return nil
}

func (s *lruStore) Get(scope, scopeID, key string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ek := entryKey(scope, scopeID, key)
	elem, ok := s.elements[ek]
	if !ok {
		return Entry{}, false
	}

	e := elem.Value.(*Entry)

	// Lazy TTL eviction.
	if e.ExpiresAt != nil && time.Now().After(*e.ExpiresAt) {
		sk := scopeKey(scope, scopeID)
		s.scopeLists[sk].Remove(elem)
		delete(s.elements, ek)
		if s.scopeLists[sk].Len() == 0 {
			delete(s.scopeLists, sk)
		}
		return Entry{}, false
	}

	s.scopeLists[scopeKey(scope, scopeID)].MoveToFront(elem)
	return *e, true
}

func (s *lruStore) Delete(scope, scopeID, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	ek := entryKey(scope, scopeID, key)
	elem, ok := s.elements[ek]
	if !ok {
		return false
	}

	sk := scopeKey(scope, scopeID)
	s.scopeLists[sk].Remove(elem)
	delete(s.elements, ek)
	if s.scopeLists[sk].Len() == 0 {
		delete(s.scopeLists, sk)
	}
	return true
}

func (s *lruStore) List(scope, scopeID string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	sk := scopeKey(scope, scopeID)
	l, ok := s.scopeLists[sk]
	if !ok {
		return nil
	}

	now := time.Now()
	var result []Entry
	var toRemove []*list.Element

	for elem := l.Front(); elem != nil; elem = elem.Next() {
		e := elem.Value.(*Entry)
		if e.ExpiresAt != nil && now.After(*e.ExpiresAt) {
			toRemove = append(toRemove, elem)
			continue
		}
		result = append(result, *e)
	}

	// Clean up expired entries found during iteration.
	for _, elem := range toRemove {
		e := elem.Value.(*Entry)
		l.Remove(elem)
		delete(s.elements, entryKey(e.Scope, e.ScopeID, e.Key))
	}
	if l.Len() == 0 {
		delete(s.scopeLists, sk)
	}

	return result
}

func (s *lruStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := 0
	for _, l := range s.scopeLists {
		total += l.Len()
	}
	return total
}
