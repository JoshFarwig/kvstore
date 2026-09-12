package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/JoshFarwig/kvstore/store"
	"github.com/hashicorp/raft"
)

type fsm struct {
	store *store.Store
}

type Op string

const (
	OpSet    Op = "set"
	OpDelete Op = "del"
)

type command struct {
	Op   Op         `json:"op,omitempty"`
	Key  string     `json:"key,omitempty"`
	Item store.Item `json:"item"`
}

// fsmSnapshot

type fsmSnapshot struct {
	Store map[string]store.Item
}

func (fs *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	// encode straight into the sink; on failure, cancel so raft discards the partial snapshot
	if err := json.NewEncoder(sink).Encode(fs.Store); err != nil {
		if cerr := sink.Cancel(); cerr != nil {
			return fmt.Errorf("unable to persist fsm snapshot: %w (cancel sink: %v)", err, cerr)
		}
		return fmt.Errorf("unable to persist fsm snapshot: %w", err)
	}

	if err := sink.Close(); err != nil {
		return fmt.Errorf("close snapshot sink: %w", err)
	}

	return nil
}

// NOTE: Release(): no-op inherently since we are using an in-mem setup for our store.
// Lock is held on the store itself when snapshot occurs. if we were in a disk-based setup, this
// is where
func (fs *fsmSnapshot) Release() {
}

// fsm

func (f *fsm) Apply(l *raft.Log) any {
	// decode the log entry back into a command
	var c command
	err := json.Unmarshal(l.Data, &c)
	if err != nil {
		panic(fmt.Errorf("could not unmarshal commmand from log: %w", err))
	}

	// dispatch on operation
	switch c.Op {
	case OpSet:
		f.store.Set(c.Key, c.Item.Value, c.Item.ExpiresAt)
		return nil
	case OpDelete:
		f.store.Delete(c.Key)
		return nil
	default:
		return fmt.Errorf("no operation exists for %v", c.Op)
	}
}

func (f *fsm) Snapshot() (*fsmSnapshot, error) {
	// store.Snapshot() returns an independent deep copy, safe to hand off to raft
	s := f.store.Snapshot()
	snapshot := fsmSnapshot{
		Store: s,
	}
	return &snapshot, nil
}

func (f *fsm) Restore(snapshot io.ReadCloser) error {
	var store map[string]store.Item
	var err error

	defer func() {
		if cerr := snapshot.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close snapshot: %w", cerr)
		}
	}()

	// decode the persisted snapshot bytes
	if err = json.NewDecoder(snapshot).Decode(&store); err != nil {
		return fmt.Errorf("unable to restore fsm snapshot: %w", err)
	}

	// swap it in as the store's new state
	f.store.Restore(store)
	return nil
}
