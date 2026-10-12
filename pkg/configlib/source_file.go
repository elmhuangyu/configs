package configlib

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/elmhuangyu/configs/pkg/keypath"
)

// FileSource serves configuration from a local directory tree. It is a
// development fallback so applications can run without an etcd cluster, and it
// mirrors the same key convention as `cfgctl push` (see pkg/keypath).
type FileSource struct {
	root     string
	interval time.Duration
	revision atomic.Int64
	onError  func(error)
}

// NewFileSource creates a FileSource rooted at root, polling for changes once
// per second.
func NewFileSource(root string) *FileSource {
	return &FileSource{root: root, interval: time.Second}
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

// Fetch reads the whole directory tree.
func (s *FileSource) Fetch(_ context.Context) (*Snapshot, error) {
	kvs, err := loadDir(s.root)
	if err != nil {
		return nil, err
	}
	return &Snapshot{KVs: kvs, Revision: s.revision.Add(1)}, nil
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

// loadDir flattens a directory tree into logical keys. Plain files shadowed by
// an encrypted raw envelope (`<name>.sops.json`) are skipped so the encrypted
// payload wins.
func loadDir(root string) (FlattenKVs, error) {
	kvs := make(FlattenKVs)

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if _, statErr := os.Stat(p + keypath.SOPSRawSuffix); statErr == nil {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		kvs[keypath.LogicalKey(filepath.ToSlash(rel))] = data
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
