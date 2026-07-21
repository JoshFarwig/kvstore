package store

import (
	"fmt"
	"sync"
	"time"
)

type KVStore interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

type Store struct {
	mu   sync.RWMutex
	data map[string]item
}

type item struct {
	value     []byte
	expiresAt time.Time
}

func NewStore() *Store {
	return &Store{data: make(map[string]item)}
}

func (s *Store) Get(key string) ([]byte, error) {
	s.mu.RLock()
	i, ok := s.data[key]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("cannot get value for key: %s, does not exist in store", key)
	}

	if !i.expiresAt.IsZero() && time.Now().UTC().UTC().After(i.expiresAt) {
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
		return nil, fmt.Errorf("entry: %v has expired, no longer exists", key)
	}

	return i.value, nil
}

func (s *Store) Set(key string, value []byte, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[key] = item{
		value:     value,
		expiresAt: expiresAt,
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
