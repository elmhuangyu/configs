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
	strict   bool
	started  atomic.Bool
	changeCh chan change[T]
}

// NewManager creates a manager bound to source. Call Start to load the initial
// configuration and begin watching for changes.
func NewManager[T any](source Source) *ConfigManager[T] {
	return &ConfigManager[T]{source: source}
}

// OnChange registers a callback invoked after a successful reload. Callbacks run
// serially on a dedicated goroutine, in order, and are never executed
// concurrently; a burst of updates is coalesced so only the latest pending
// change is delivered.
//
// It must be called before Start; calling it afterwards panics, because the
// dispatcher is not started and the callback would never fire.
func (m *ConfigManager[T]) OnChange(fn func(oldCfg, newCfg *T)) {
	if m.started.Load() {
		panic("configlib: OnChange must be set before Start")
	}
	m.onChange = fn
}

// OnError registers a callback for reload failures. Without it, a revision that
// fails to bind (for example because a decryption key is missing) is dropped
// silently and the previous config keeps serving.
//
// It must be called before Start; calling it afterwards panics.
func (m *ConfigManager[T]) OnError(fn func(error)) {
	if m.started.Load() {
		panic("configlib: OnError must be set before Start")
	}
	m.onError = fn
}

// SetStrict enables strict binding: a revision in which a key-tagged leaf field
// has no matching key is rejected (and reported via OnError) instead of leaving
// the field zero. This catches misspelled key tags. It must be called before
// Start; calling it afterwards panics.
func (m *ConfigManager[T]) SetStrict(strict bool) {
	if m.started.Load() {
		panic("configlib: SetStrict must be called before Start")
	}
	m.strict = strict
}

// bind maps a snapshot onto target using the configured mode.
func (m *ConfigManager[T]) bind(kvs FlattenKVs, target *T) error {
	if m.strict {
		return BindStrict(kvs, target)
	}
	return Bind(kvs, target)
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

// Start performs the initial fetch and bind, then launches the watch loop. Once
// Start has been called, OnChange and OnError can no longer be set.
func (m *ConfigManager[T]) Start(ctx context.Context) error {
	m.started.Store(true)

	snap, err := m.source.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("initial fetch failed: %w", err)
	}

	newVal := new(T)
	if err := m.bind(snap.KVs, newVal); err != nil {
		return fmt.Errorf("initial bind failed: %w", err)
	}

	m.ptr.Store(newVal)
	m.revision.Store(snap.Revision)

	events, err := m.source.Watch(ctx)
	if err != nil {
		return fmt.Errorf("watch failed: %w", err)
	}

	m.changeCh = make(chan change[T], 1)
	if m.onChange != nil {
		go m.dispatchLoop()
	}
	go m.watchLoop(events)
	return nil
}

func (m *ConfigManager[T]) watchLoop(events <-chan *Snapshot) {
	defer close(m.changeCh)

	for snap := range events {
		nextVal := new(T)
		// Defensive reload: a malformed revision is dropped instead of
		// crashing the process with a broken configuration. The failure is
		// reported so it never goes unnoticed.
		if err := m.bind(snap.KVs, nextVal); err != nil {
			m.reportError(fmt.Errorf("bind revision %d: %w", snap.Revision, err))
			continue
		}

		oldVal := m.ptr.Swap(nextVal)
		m.revision.Store(snap.Revision)
		m.publish(oldVal, nextVal)
	}
}

// change is a config transition handed to the serial dispatcher.
type change[T any] struct {
	old *T
	new *T
}

// publish queues a change for OnChange. At most one change is pending, so a
// burst of updates collapses into the latest and no goroutine is spawned per
// event.
func (m *ConfigManager[T]) publish(oldVal, newVal *T) {
	if m.onChange == nil {
		return
	}

	c := change[T]{old: oldVal, new: newVal}
	for {
		select {
		case m.changeCh <- c:
			return
		default:
		}

		// The pending slot is full: drop the stale change and retry with the
		// newest one.
		select {
		case <-m.changeCh:
		default:
		}
	}
}

// dispatchLoop delivers changes to OnChange one at a time, preserving order.
func (m *ConfigManager[T]) dispatchLoop() {
	for c := range m.changeCh {
		m.onChange(c.old, c.new)
	}
}
