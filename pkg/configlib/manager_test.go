package configlib

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type watchConfig struct {
	Mode  string `key:"mode"`
	Level string `key:"level"`
}

func TestManagerInitialLoad(t *testing.T) {
	src := NewMemSource(FlattenKVs{"mode": []byte("prod")})

	m := NewManager[watchConfig](src)
	require.NoError(t, m.Start(context.Background()))

	assert.Equal(t, "prod", m.Get().Mode)
	assert.Equal(t, int64(1), m.Revision())
}

func TestManagerReloadAndOnChange(t *testing.T) {
	src := NewMemSource(FlattenKVs{"mode": []byte("prod")})

	m := NewManager[watchConfig](src)

	changed := make(chan watchConfig, 1)
	m.OnChange(func(_, newCfg *watchConfig) {
		changed <- *newCfg
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, m.Start(ctx))

	src.Set(FlattenKVs{"mode": []byte("staging"), "level": []byte("debug")})

	select {
	case got := <-changed:
		assert.Equal(t, "staging", got.Mode)
		assert.Equal(t, "debug", got.Level)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OnChange callback")
	}

	assert.Equal(t, "staging", m.Get().Mode)
	assert.Equal(t, int64(2), m.Revision())
}

func TestManagerKeepsPreviousConfigOnBadRevision(t *testing.T) {
	type strictConfig struct {
		Manifest struct {
			Count int `yaml:"count"`
		} `key:"manifest.yaml"`
	}
	src := NewMemSource(FlattenKVs{"manifest.yaml": []byte("count: 1\n")})

	m := NewManager[strictConfig](src)
	require.NoError(t, m.Start(context.Background()))
	assert.Equal(t, 1, m.Get().Manifest.Count)

	// The new revision is not valid YAML for the target, so it must be dropped
	// and the previous value retained.
	src.Set(FlattenKVs{"manifest.yaml": []byte("count: not-an-int\n")})

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, m.Get().Manifest.Count, "previous config must be retained")
	assert.Equal(t, int64(1), m.Revision())
}

func TestManagerReportsBindFailure(t *testing.T) {
	type strictConfig struct {
		Manifest struct {
			Count int `yaml:"count"`
		} `key:"manifest.yaml"`
	}

	src := NewMemSource(FlattenKVs{"manifest.yaml": []byte("count: 1\n")})
	m := NewManager[strictConfig](src)

	errs := make(chan error, 1)
	m.OnError(func(err error) { errs <- err })

	require.NoError(t, m.Start(context.Background()))

	// A malformed revision is dropped but must be reported.
	src.Set(FlattenKVs{"manifest.yaml": []byte("count: not-an-int\n")})

	select {
	case err := <-errs:
		assert.Contains(t, err.Error(), "bind revision")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OnError")
	}

	// The previous config stays active.
	assert.Equal(t, 1, m.Get().Manifest.Count)
}

func TestManagerOnChangeSerializedAndCoalesced(t *testing.T) {
	src := NewMemSource(FlattenKVs{"mode": []byte("v0")})
	m := NewManager[watchConfig](src)

	var (
		concurrent    atomic.Int32
		maxConcurrent atomic.Int32
		mu            sync.Mutex
		delivered     []string
	)

	m.OnChange(func(_, newCfg *watchConfig) {
		n := concurrent.Add(1)
		for {
			old := maxConcurrent.Load()
			if n <= old || maxConcurrent.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		concurrent.Add(-1)

		mu.Lock()
		delivered = append(delivered, newCfg.Mode)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, m.Start(ctx))

	// A burst of updates larger than the pending-slot size but small enough
	// that the source does not drop any (MemSource drops on a full buffer).
	for i := 1; i <= 8; i++ {
		src.Set(FlattenKVs{"mode": []byte(fmt.Sprintf("v%d", i))})
	}

	// The final value must eventually be delivered.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) > 0 && delivered[len(delivered)-1] == "v8"
	}, 2*time.Second, 5*time.Millisecond)

	// Callbacks must never overlap.
	assert.EqualValues(t, 1, maxConcurrent.Load())
}

func TestManagerOnChangeAfterStartPanics(t *testing.T) {
	src := NewMemSource(FlattenKVs{"mode": []byte("prod")})
	m := NewManager[watchConfig](src)
	require.NoError(t, m.Start(context.Background()))

	assert.Panics(t, func() { m.OnChange(func(_, _ *watchConfig) {}) })
	assert.Panics(t, func() { m.OnError(func(error) {}) })
}

func TestManagerOnChangeBeforeStartDoesNotPanic(t *testing.T) {
	src := NewMemSource(FlattenKVs{"mode": []byte("prod")})
	m := NewManager[watchConfig](src)

	assert.NotPanics(t, func() { m.OnChange(func(_, _ *watchConfig) {}) })
	assert.NotPanics(t, func() { m.OnError(func(error) {}) })
}

func TestManagerStrictModeReportsMissingKey(t *testing.T) {
	src := NewMemSource(FlattenKVs{"mode": []byte("prod"), "level": []byte("info")})
	m := NewManager[watchConfig](src)
	m.SetStrict(true)

	errs := make(chan error, 1)
	m.OnError(func(err error) { errs <- err })

	require.NoError(t, m.Start(context.Background()))

	// level is declared but now absent, so strict binding rejects the revision.
	src.Set(FlattenKVs{"mode": []byte("staging")})

	select {
	case err := <-errs:
		assert.Contains(t, err.Error(), "missing key")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for strict bind error")
	}

	// Previous config is retained.
	assert.Equal(t, "prod", m.Get().Mode)
}

func TestManagerSetStrictAfterStartPanics(t *testing.T) {
	src := NewMemSource(FlattenKVs{"mode": []byte("prod")})
	m := NewManager[watchConfig](src)
	require.NoError(t, m.Start(context.Background()))

	assert.Panics(t, func() { m.SetStrict(true) })
}

func TestManagerConcurrentReads(t *testing.T) {
	src := NewMemSource(FlattenKVs{"mode": []byte("prod")})
	m := NewManager[watchConfig](src)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, m.Start(ctx))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				assert.NotNil(t, m.Get())
			}
		}()
	}

	src.Set(FlattenKVs{"mode": []byte("staging")})
	wg.Wait()
}
