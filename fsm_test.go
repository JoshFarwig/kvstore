package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JoshFarwig/kvstore/store"
	"github.com/hashicorp/raft"
)

type errSink struct {
	io.Writer
	closeErr  error
	cancelErr error
}

func (s *errSink) Write(p []byte) (int, error) { return s.Writer.Write(p) }
func (s *errSink) Close() error                { return s.closeErr }
func (s *errSink) Cancel() error               { return s.cancelErr }
func (s *errSink) ID() string                  { return "err" }

func createErrSink(t *testing.T, closeErr error, cancelErr error) *errSink {
	t.Helper()
	return &errSink{
		Writer:    &bytes.Buffer{},
		closeErr:  closeErr,
		cancelErr: cancelErr,
	}
}

type errWriter struct{ err error }

func (w errWriter) Write(p []byte) (int, error) { return 0, w.err }

func createFSM(t *testing.T, seed map[string]store.Item) *fsm {
	t.Helper()
	fsm := fsm{
		store.NewStore(),
	}
	if seed != nil {
		fsm.store.Restore(seed)
	}
	return &fsm
}

func createSnapshotStore(t *testing.T) *raft.InmemSnapshotStore {
	t.Helper()
	return raft.NewInmemSnapshotStore()
}

// fsmSnapshot

func TestPersist(t *testing.T) {
	// setup snapshot store and sink to persist into
	ss := createSnapshotStore(t)
	sink, err := ss.Create(raft.SnapshotVersionMax, 1, 1, raft.Configuration{}, 1, nil)
	if err != nil {
		t.Fatalf("create sink: %v", err)
	}

	// persist snapshot data into the sink
	want := map[string]store.Item{
		"t1": {Value: json.RawMessage(`"test1"`)},
		"t2": {Value: json.RawMessage(`"test2"`), ExpiresAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	snap := fsmSnapshot{Store: want}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// read back what was actually persisted
	_, rc, err := ss.Open(sink.ID())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rc.Close() //nolint:errcheck // test cleanup, inmem always returns nil

	var got map[string]store.Item
	if err := json.NewDecoder(rc).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// compare persisted data to what we wrote
	if !reflect.DeepEqual(got, want) {
		t.Errorf("persisted snapshot = %+v, want %+v", got, want)
	}
}

func TestPersistErrSink(t *testing.T) {
	tests := []struct {
		name      string
		writeErr  error
		closeErr  error
		cancelErr error
		wantErr   string
	}{
		{
			name:     "close error",
			closeErr: errors.New("close boom"),
			wantErr:  "close snapshot sink",
		},
		{
			name:      "write error, cancel error",
			writeErr:  errors.New("write boom"),
			cancelErr: errors.New("cancel boom"),
			wantErr:   "cancel sink",
		},
	}

	snap := fsmSnapshot{Store: map[string]store.Item{"t1": {Value: json.RawMessage(`"test1"`)}}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// setup sink, swapping in a failing writer when the case needs an encode error
			sink := createErrSink(t, tt.closeErr, tt.cancelErr)
			if tt.writeErr != nil {
				sink.Writer = errWriter{err: tt.writeErr}
			}

			// persist and confirm the expected failure path was hit
			err := snap.Persist(sink)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("persist error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// fsm

func TestApply(t *testing.T) {
	tests := []struct {
		name     string
		seed     map[string]store.Item
		command  command
		expected map[string]store.Item
	}{
		{
			name:     "set success",
			seed:     nil,
			command:  command{Op: OpSet, Key: "t1", Item: store.Item{Value: json.RawMessage(`"test1"`)}},
			expected: map[string]store.Item{"t1": {Value: json.RawMessage(`"test1"`)}},
		},
		{
			name:     "del success",
			seed:     map[string]store.Item{"t1": {Value: json.RawMessage(`"test1"`)}},
			command:  command{Op: OpDelete, Key: "t1"},
			expected: map[string]store.Item{},
		},
		{
			name:     "unknown op",
			seed:     nil,
			command:  command{Op: Op("unk")},
			expected: map[string]store.Item{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// setup seeded fsm and command log entry
			f := createFSM(t, tt.seed)
			data, err := json.Marshal(tt.command)
			if err != nil {
				t.Fatalf("marshal command: %v", err)
			}

			// apply the command
			f.Apply(&raft.Log{Data: data})

			// compare resulting state
			got, _ := f.Snapshot()
			if !reflect.DeepEqual(got.Store, tt.expected) {
				t.Errorf("store state = %+v, want %+v", got, tt.expected)
			}
		})
	}
}

func TestSnapshot(t *testing.T) {
	// setup seeded fsm and baseline snapshot
	seed := map[string]store.Item{"t1": {Value: json.RawMessage(`"test1"`)}}
	f := createFSM(t, seed)
	s1, _ := f.Snapshot()

	if !reflect.DeepEqual(s1.Store, seed) {
		t.Errorf("snapshot state = %+v, want %+v", s1, seed)
	}

	// mutate the returned snapshot directly; ensure s1 does not have reference
	// values and is a true deepcopy, if so, next snapshot should be same as seed
	s1.Store["t1"] = store.Item{Value: json.RawMessage(`"mutated"`)}
	s1.Store["t2"] = store.Item{Value: json.RawMessage(`"new"`)}

	// take a second snapshot and compare against the original seed
	s2, _ := f.Snapshot()
	if !reflect.DeepEqual(s2.Store, seed) {
		t.Errorf("store mutated via snapshot alias: got %+v, want %+v", s2.Store, seed)
	}
}

func TestRestore(t *testing.T) {
	// setup seeded fsm; snapshot before mutating since seed aliases the store's
	// map via Restore and would otherwise reflect the mutation below
	seed := map[string]store.Item{"t1": {Value: json.RawMessage(`"test1"`)}}
	mutation := command{Op: OpSet, Key: "t1", Item: store.Item{Value: json.RawMessage(`"mutated"`)}}
	expectedMutation := map[string]store.Item{"t1": {Value: json.RawMessage(`"mutated"`)}}
	f := createFSM(t, seed)
	seedSnapshot, _ := f.Snapshot()

	// marshal seed (restore input) and mutation (apply input)
	seedData, err := json.Marshal(seed)
	if err != nil {
		t.Errorf("marshal command: %v", err)
	}
	mutatedData, err := json.Marshal(mutation)
	if err != nil {
		t.Errorf("marshal command: %v", err)
	}

	// apply the mutation and confirm it took effect
	f.Apply(&raft.Log{Data: mutatedData})
	mutatedSnapshot, _ := f.Snapshot()
	if !reflect.DeepEqual(mutatedSnapshot.Store, expectedMutation) {
		t.Errorf("mutations not equal: got %+v, want %+v", mutatedSnapshot.Store, expectedMutation)
	}

	// restore from the seed data, wrapped as the ReadCloser a snapshot store's Open would return
	rc := io.NopCloser(bytes.NewReader(seedData))
	err = f.Restore(rc)
	if err != nil {
		t.Errorf("restore: %v", err)
	}

	// compare restored state to the pre-mutation snapshot
	restoredSnapshot, _ := f.Snapshot()
	if !reflect.DeepEqual(restoredSnapshot.Store, seedSnapshot.Store) {
		t.Errorf("restore != seed. got %+v, want %+v", restoredSnapshot.Store, seedSnapshot.Store)
	}
}
