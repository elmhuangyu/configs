package configlib

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/elmhuangyu/configs/pkg/cfgfs"
)

// FileSource serves configuration from a local directory tree. It is a
// development fallback so applications can run without an etcd cluster, and it
// mirrors the same key convention as `cfgctl push` (see pkg/cfgfs).
type FileSource struct {
	root     string
	interval time.Duration
	revision atomic.Int64
	onError  func(error)
}

// NewFileSource creates a FileSource rooted at root, polling for changes once
// per second.
func NewFileSource(root string) *FileSource {
	s := &FileSource{root: root, interval: time.Second}
	s.revision.Store(1)
	return s
}

// OnError registers a callback for polling failures. Set it before Start.
func (s *FileSource) OnError(fn func(error)) {
	s.onError = fn
}

func (s *FileSource) reportError(err error) {
	if s.onError != nil {
		s.onError(err)
	}
}

// Fetch reads the whole directory tree. Its revision reflects the last observed
// content change (like a backend revision), not a fetch counter.
func (s *FileSource) Fetch(_ context.Context) (*Snapshot, error) {
	kvs, err := loadDir(s.root)
	if err != nil {
		return nil, err
	}
	return &Snapshot{KVs: kvs, Revision: s.revision.Load()}, nil
}

// Watch polls the directory and emits a snapshot whenever its contents change.
func (s *FileSource) Watch(ctx context.Context) (<-chan *Snapshot, error) {
	// Capture the baseline synchronously so changes made right after Watch
	// returns are always observed.
	last, err := loadDir(s.root)
	if err != nil {
		return nil, err
	}

	out := make(chan *Snapshot)

	go func() {
		defer close(out)

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				kvs, err := loadDir(s.root)
				if err != nil {
					s.reportError(fmt.Errorf("reload %s: %w", s.root, err))
					continue
				}
				if equalKVs(last, kvs) {
					continue
				}
				last = kvs

				select {
				case out <- &Snapshot{KVs: kvs, Revision: s.revision.Add(1)}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}

// Close is a no-op; the directory holds no resources.
func (s *FileSource) Close() error { return nil }

// loadDir flattens a directory tree into logical keys. It applies the same
// ignore rules as `cfgctl push` (built-in excludes plus .cfgctlignore), so the
// file driver observes exactly the keys the CLI would publish. Plain files
// shadowed by an encrypted raw envelope (`<name>.sops.json`) are skipped so the
// encrypted payload wins.
func loadDir(root string) (FlattenKVs, error) {
	kvs := make(FlattenKVs)

	err := cfgfs.Walk(root, func(rel, full string) error {
		if _, statErr := os.Stat(full + cfgfs.SOPSRawSuffix); statErr == nil {
			return nil
		}

		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		kvs[cfgfs.LogicalKey(rel)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return kvs, nil
}

func equalKVs(a, b FlattenKVs) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !bytes.Equal(av, bv) {
			return false
		}
	}
	return true
}

var _ Source = (*FileSource)(nil)
