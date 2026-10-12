package configlib

import (
	"context"
)

// The mapping between on-disk paths and logical keys (including the stripping
// of the ".sops.json" envelope suffix) lives in pkg/keypath, shared with the
// publishing CLI so both sides agree on a single convention.

// FlattenKVs is a flat dictionary of configuration entries. Keys are paths
// relative to the source prefix and values are raw payloads that may still be
// SOPS encrypted.
type FlattenKVs map[string][]byte

// Snapshot is an immutable, point-in-time view of the configuration tree.
// Revision is the monotonic backend revision the snapshot was read at.
type Snapshot struct {
	KVs      FlattenKVs
	Revision int64
}

// Source abstracts a configuration backend. Implementations must return a full
// snapshot on Fetch and emit a fresh snapshot on every observed change.
type Source interface {
	// Fetch reads the complete configuration tree.
	Fetch(ctx context.Context) (*Snapshot, error)
	// Watch streams snapshots as the tree changes. The returned channel is
	// closed when ctx is cancelled or the underlying watch terminates.
	Watch(ctx context.Context) (<-chan *Snapshot, error)
	// Close releases resources held by the source.
	Close() error
}
