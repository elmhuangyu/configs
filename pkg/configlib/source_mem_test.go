package configlib

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemSourceCloseClosesWatchers(t *testing.T) {
	src := NewMemSource(FlattenKVs{"a": []byte("1")})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := src.Watch(ctx)
	require.NoError(t, err)

	require.NoError(t, src.Close())

	_, ok := <-ch
	assert.False(t, ok, "watch channel must be closed by Close")

	// Close is idempotent.
	require.NoError(t, src.Close())
}

func TestMemSourceCancelClosesWatcher(t *testing.T) {
	src := NewMemSource(FlattenKVs{"a": []byte("1")})

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := src.Watch(ctx)
	require.NoError(t, err)

	cancel()

	_, ok := <-ch
	assert.False(t, ok)
}

func TestMemSourceSetAfterCloseIsNoop(t *testing.T) {
	src := NewMemSource(FlattenKVs{"a": []byte("1")})
	require.NoError(t, src.Close())

	src.Set(FlattenKVs{"a": []byte("2")})

	snap, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []byte("1"), snap.KVs["a"])
}
