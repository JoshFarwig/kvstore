package store

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSetAndGet(t *testing.T) {
	key := "key1"
	value := []byte("value1")
	expiresAt := time.Now().Add(24 * time.Hour)

	s := NewStore()
	s.Set(key, value, expiresAt)

	v, err := s.Get(key)
	if err != nil {
		t.Errorf("no entry with key: %s found in store. %v", key, err)
	}
	if !bytes.Equal(v, value) {
		t.Errorf("value set: %v does not equal value in store: %v", value, v)
	}
}

func TestSetOverrideAndForever(t *testing.T) {
	key := "key1"
	value := []byte("value1")
	newValue := []byte("value2")
	expiresAt := time.Now().Add(24 * time.Hour)

	s := NewStore()
	s.Set(key, value, expiresAt)
	// overridden with no expiresAt, zero value for time, ttl = forever
	s.Set(key, newValue, time.Time{})

	v, err := s.Get(key)
	if err != nil {
		t.Errorf("no entry with key: %s found in store. %v", key, err)
	}
	if !bytes.Equal(v, newValue) {
		t.Errorf("expected overridden value: %v, got: %v", newValue, v)
	}
}

func TestGetNotExists(t *testing.T) {
	key := "nonexistentkey"

	s := NewStore()

	_, err := s.Get(key)
	if err == nil {
		t.Errorf("no entry with key: %s found in store. %v", key, err)
	}
}

func TestGetExpires(t *testing.T) {
	key := "key1"
	value := []byte("value1")
	expiresAt := time.Now().Add(-2 * time.Hour)

	s := NewStore()
	s.Set(key, value, expiresAt)

	v, err := s.Get(key)
	if err == nil {
		t.Errorf("no entry with key: %s should be expired. value: %v", key, v)
	}
}

func TestDeleteThenGet(t *testing.T) {
	key := "key1"
	value := []byte("value1")

	s := NewStore()
	s.Set(key, value, time.Time{})

	deleteErr := s.Delete(key)
	if deleteErr != nil {
		t.Errorf("unable to delete entry with key: %s. err: %v", key, deleteErr)
	}

	_, err := s.Get(key)
	if err == nil {
		t.Errorf("entry with key: %s found in store", key)
	}
}

func TestDeleteNotExists(t *testing.T) {
	key := "nonexistentkey"

	s := NewStore()

	err := s.Delete(key)
	if err == nil {
		t.Errorf("should not be able to delete a non-existent key: %s ", key)
	}
}

func TestConcurrentConsumerProducer(t *testing.T) {
	numOps := 100
	numKeys := 10
	numProducers, numConsumers := 10, 10
	deleteInterval := 7

	var wg sync.WaitGroup
	var hits, misses atomic.Int64
	s := NewStore()

	// seed so consumers are not erring from unitialized entries
	for k := range numKeys {
		s.Set(fmt.Sprintf("key%d", k), []byte("seed"), time.Time{})
	}

	for p := range numProducers {
		wg.Go(func() {
			for i := range numOps {
				key := fmt.Sprintf("key%d", i%numKeys)
				s.Set(key, fmt.Appendf(nil, "p%d-%d", i, p), time.Time{})
			}
		})
	}

	for range numConsumers {
		wg.Go(func() {
			for i := range numOps {
				key := fmt.Sprintf("key%d", i%numKeys)
				if _, err := s.Get(key); err != nil {
					misses.Add(1)
				} else {
					hits.Add(1)
				}
				if i%deleteInterval == 0 {
					s.Delete(key)
				}
			}
		})
	}

	wg.Wait()
	t.Logf("deleteInterval=%d producers=%d consumers=%d hits=%d misses=%d", deleteInterval, numProducers, numConsumers, hits.Load(), misses.Load())
}
