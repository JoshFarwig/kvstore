package store

import (
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
