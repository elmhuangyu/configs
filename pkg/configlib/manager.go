package configlib

import (
	"context"
	"fmt"
	"sync/atomic"
)

// ConfigManager owns a live configuration value of type T. Reads are served
// lock-free from an atomic pointer, while a background goroutine keeps the
// value in sync with the Source.
type ConfigManager[T any] struct {
	source   Source
	ptr      atomic.Pointer[T]
	revision atomic.Int64
	onChange func(oldCfg, newCfg *T)
	onError  func(error)
}

// NewManager creates a manager bound to source. Call Start to load the initial
// configuration and begin watching for changes.
func NewManager[T any](source Source) *ConfigManager[T] {
	return &ConfigManager[T]{source: source}
}

// OnChange registers a callback invoked after a successful reload. The callback
// runs on its own goroutine and must be set before Start.
func (m *ConfigManager[T]) OnChange(fn func(oldCfg, newCfg *T)) {
	m.onChange = fn
}

// OnError registers a callback for reload failures. Without it, a revision that
// fails to bind (for example because a decryption key is missing) is dropped
// silently and the previous config keeps serving. Set it before Start.
func (m *ConfigManager[T]) OnError(fn func(error)) {
	m.onError = fn
}

func (m *ConfigManager[T]) reportError(err error) {
	if m.onError != nil {
		m.onError(err)
	}
}

// Get returns the current configuration. It is a pure in-memory load and is
// safe for concurrent use.
func (m *ConfigManager[T]) Get() *T {
	return m.ptr.Load()
}

// Revision returns the backend revision of the currently active configuration.
func (m *ConfigManager[T]) Revision() int64 {
	return m.revision.Load()
}

// Start performs the initial fetch and bind, then launches the watch loop.
func (m *ConfigManager[T]) Start(ctx context.Context) error {
	snap, err := m.source.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("initial fetch failed: %w", err)
	}

	newVal := new(T)
	if err := Bind(snap.KVs, newVal); err != nil {
		return fmt.Errorf("initial bind failed: %w", err)
	}

	m.ptr.Store(newVal)
	m.revision.Store(snap.Revision)

	events, err := m.source.Watch(ctx)
	if err != nil {
		return fmt.Errorf("watch failed: %w", err)
	}

	go m.watchLoop(events)
	return nil
}

func (m *ConfigManager[T]) watchLoop(events <-chan *Snapshot) {
	for snap := range events {
		nextVal := new(T)
		// Defensive reload: a malformed revision is dropped instead of
		// crashing the process with a broken configuration. The failure is
		// reported so it never goes unnoticed.
		if err := Bind(snap.KVs, nextVal); err != nil {
			m.reportError(fmt.Errorf("bind revision %d: %w", snap.Revision, err))
			continue
		}

		oldVal := m.ptr.Swap(nextVal)
		m.revision.Store(snap.Revision)

		if m.onChange != nil {
			go m.onChange(oldVal, nextVal)
		}
	}
}
