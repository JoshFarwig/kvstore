package store

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type KVStore interface {
	Get(key string) (Item, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

type Store struct {
	mu   sync.RWMutex
	data map[string]Item
}

type Item struct {
	Value     json.RawMessage `json:"value"`
	ExpiresAt time.Time       `json:"expires_at"`
}

func NewStore() *Store {
	return &Store{data: make(map[string]Item)}
}

func (s *Store) Get(key string) (Item, error) {
	s.mu.RLock()
	i, ok := s.data[key]
	s.mu.RUnlock()

	if !ok {
		return Item{}, fmt.Errorf("cannot get value for key: %s, does not exist in store", key)
	}

	if !i.ExpiresAt.IsZero() && time.Now().UTC().After(i.ExpiresAt) {
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
		return Item{}, fmt.Errorf("entry: %v has expired, no longer exists", key)
	}

	return i, nil
}

func (s *Store) Set(key string, value []byte, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[key] = Item{
		Value:     value,
		ExpiresAt: expiresAt,
	}
	return nil
}

func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data[key]; ok {
		delete(s.data, key)
		return nil
	}
	return fmt.Errorf("cannot delete a key: %s that does not exist", key)
}
