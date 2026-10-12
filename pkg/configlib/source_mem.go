package configlib

import (
	"bytes"
	"context"
	"sync"
)

// MemSource is an in-memory Source used for unit tests and local experiments.
// It keeps the whole tree in memory and broadcasts snapshots to watchers.
type MemSource struct {
	mu       sync.Mutex
	kvs      FlattenKVs
	revision int64
	watchers map[chan *Snapshot]struct{}
	closed   bool
}

// NewMemSource creates a MemSource seeded with kvs. The input map is copied.
func NewMemSource(kvs FlattenKVs) *MemSource {
	return &MemSource{
		kvs:      cloneKVs(kvs),
		revision: 1,
		watchers: make(map[chan *Snapshot]struct{}),
	}
}

// Fetch returns the current tree.
func (s *MemSource) Fetch(_ context.Context) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &Snapshot{KVs: cloneKVs(s.kvs), Revision: s.revision}, nil
}

// Watch registers a buffered channel that receives a snapshot on every Set call.
func (s *MemSource) Watch(ctx context.Context) (<-chan *Snapshot, error) {
	ch := make(chan *Snapshot, 16)

	s.mu.Lock()
	s.watchers[ch] = struct{}{}
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		if _, ok := s.watchers[ch]; ok {
			delete(s.watchers, ch)
			close(ch)
		}
		s.mu.Unlock()
	}()

	return ch, nil
}

// Set atomically replaces the tree and notifies watchers. Notifications use a
// non-blocking send so a slow consumer can never stall the caller.
func (s *MemSource) Set(kvs FlattenKVs) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}

	s.kvs = cloneKVs(kvs)
	s.revision++

	snap := &Snapshot{KVs: cloneKVs(s.kvs), Revision: s.revision}
	for ch := range s.watchers {
		select {
		case ch <- snap:
		default:
		}
	}
}

// Close releases all watchers and makes the source inert.
func (s *MemSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for ch := range s.watchers {
		delete(s.watchers, ch)
		close(ch)
	}
	return nil
}

func cloneKVs(kvs FlattenKVs) FlattenKVs {
	out := make(FlattenKVs, len(kvs))
	for k, v := range kvs {
		out[k] = bytes.Clone(v)
	}
	return out
}
