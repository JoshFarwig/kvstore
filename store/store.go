package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrNotFound = errors.New("key not found")

type Store struct {
	mu   sync.RWMutex
	data map[string]Item
}

type Item struct {
	Value     json.RawMessage `json:"value"`
	ExpiresAt time.Time       `json:"expiresAt"`
}

func NewStore() *Store {
	return &Store{data: make(map[string]Item)}
}

func (s *Store) Get(key string) (Item, error) {
	s.mu.RLock()
	i, ok := s.data[key]
	s.mu.RUnlock()

	if !ok {
		return Item{}, fmt.Errorf("get %q: %w", key, ErrNotFound)
	}

	if !i.ExpiresAt.IsZero() && time.Now().UTC().After(i.ExpiresAt) {
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
		return Item{}, fmt.Errorf("get %q: expired: %w", key, ErrNotFound)
	}

	return i, nil
}

func (s *Store) Set(key string, value []byte, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[key] = Item{
		Value:     value,
		ExpiresAt: expiresAt,
	}
}

func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
}

func (s *Store) ReapExpired() {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.data {
		if !v.ExpiresAt.IsZero() && now.After(v.ExpiresAt) {
			delete(s.data, k)
		}
	}
}

// Raft methods

func (s *Store) Snapshot() map[string]Item {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// NOTE: maps.Copy/Clone will leave underlying pointers to reference data (Item's value becomes byte[]) which
	// leaves potential for concurrent r/w on byte slice, could produce torn data if ever accessing directly.
	// Snapshot() makes independent copy as a precautionary. Copying in-mem map is cheap here so the I/O concern
	// does not apply (in-mem). A disk based fsm should not copy if here, and instead copy in FSM layer

	snapshot := make(map[string]Item, len(s.data))

	for k, v := range s.data {
		snapshot[k] = Item{
			Value:     bytes.Clone(v.Value),
			ExpiresAt: v.ExpiresAt,
		}
	}
	return snapshot
}

func (s *Store) Restore(snapshot map[string]Item) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// NOTE: direct reasign with reference value for s.data
	// is fine here since snapshot is not shared mutable state UNTIL it
	// becomes s.data, and lock releases

	if snapshot == nil {
		snapshot = map[string]Item{}
	}
	s.data = snapshot
}
