package store

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const forever = time.Duration(0)

// set is a helper so tests read in terms of "expires in X" rather than
// building absolute timestamps. A zero duration means no expiry.
func set(s *Store, key, value string, in time.Duration) {
	var expiresAt time.Time
	if in != forever {
		expiresAt = time.Now().UTC().Add(in)
	}
	s.Set(key, []byte(value), expiresAt)
}

func mustGet(t *testing.T, s *Store, key string) Item {
	t.Helper()
	i, err := s.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): unexpected error: %v", key, err)
	}
	return i
}

func mustMiss(t *testing.T, s *Store, key string) {
	t.Helper()
	if i, err := s.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(%q) = (%v, %v), want ErrNotFound", key, i, err)
	}
}

func TestSetAndGet(t *testing.T) {
	s := NewStore()
	set(s, "key1", "value1", 24*time.Hour)

	if got := mustGet(t, s, "key1"); !bytes.Equal(got.Value, []byte("value1")) {
		t.Errorf("value = %s, want value1", got.Value)
	}
}

func TestSetOverwrites(t *testing.T) {
	s := NewStore()
	set(s, "key1", "value1", 24*time.Hour)
	set(s, "key1", "value2", forever) // also clears the expiry

	got := mustGet(t, s, "key1")
	if !bytes.Equal(got.Value, []byte("value2")) {
		t.Errorf("value = %s, want value2", got.Value)
	}
	if !got.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt = %v, want zero after overwrite with no expiry", got.ExpiresAt)
	}
}

// Every way a key can be absent must report the same sentinel, so callers can
// map all of them to one 404 without inspecting messages.
func TestGetMissingCases(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Store)
	}{
		{"never set", func(*Store) {}},
		{"deleted", func(s *Store) { set(s, "k", "v", forever); s.Delete("k") }},
		{"expired", func(s *Store) { set(s, "k", "v", -time.Hour) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStore()
			tt.setup(s)
			mustMiss(t, s, "k")
		})
	}
}

// Deleting an absent key is a success that removed nothing, so repeated calls
// must be indistinguishable from a single one.
func TestDeleteIsIdempotent(t *testing.T) {
	s := NewStore()
	set(s, "k", "v", forever)

	s.Delete("k")
	s.Delete("k")
	s.Delete("k")

	mustMiss(t, s, "k")
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
		set(s, fmt.Sprintf("key%d", k), "seed", forever)
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
