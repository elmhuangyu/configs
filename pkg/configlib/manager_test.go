package configlib

import (
	"context"
	"sync"
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
