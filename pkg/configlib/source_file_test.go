package configlib

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileSourceFetchFlattensTree(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "agents", "coder"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "agents", "coder", "prompt.txt"), []byte("hi"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "global.yaml"), []byte("a: b\n"), 0o644))

	src := NewFileSource(root)
	snap, err := src.Fetch(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []byte("hi"), snap.KVs["agents/coder/prompt.txt"])
	assert.Equal(t, []byte("a: b\n"), snap.KVs["global.yaml"])
}

func TestFileSourcePrefersEncryptedEnvelope(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "prompt.txt"), []byte("plaintext"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "prompt.txt.sops.json"), []byte(`{"sops":{}}`), 0o644))

	src := NewFileSource(root)
	snap, err := src.Fetch(context.Background())
	require.NoError(t, err)

	require.Len(t, snap.KVs, 1)
	assert.Equal(t, []byte(`{"sops":{}}`), snap.KVs["prompt.txt"])
}

func TestFileSourceHonorsIgnore(t *testing.T) {
	root := t.TempDir()

	// Built-in excludes and .cfgctlignore must apply, exactly as `cfgctl push`
	// would see the directory.
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "config"), []byte("gitcfg"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".sops.yaml"), []byte("creation_rules: []\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".cfgctlignore"), []byte("ignored.yaml\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ignored.yaml"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), []byte("a: b\n"), 0o644))

	src := NewFileSource(root)
	snap, err := src.Fetch(context.Background())
	require.NoError(t, err)

	assert.Equal(t, FlattenKVs{"app.yaml": []byte("a: b\n")}, snap.KVs)
}

func TestFileSourceReportsPollError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a"), []byte("1"), 0o644))

	src := NewFileSource(root)
	src.interval = 10 * time.Millisecond

	errs := make(chan error, 1)
	src.OnError(func(err error) {
		select {
		case errs <- err:
		default:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := src.Watch(ctx)
	require.NoError(t, err)

	// Removing the directory makes subsequent polls fail.
	require.NoError(t, os.RemoveAll(root))

	select {
	case err := <-errs:
		assert.Contains(t, err.Error(), "reload")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for poll error report")
	}
}

func TestFileSourceWatchEmitsOnChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mode")
	require.NoError(t, os.WriteFile(path, []byte("prod"), 0o644))

	src := NewFileSource(root)
	src.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := src.Watch(ctx)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(path, []byte("staging"), 0o644))

	select {
	case snap := <-events:
		assert.Equal(t, []byte("staging"), snap.KVs["mode"])
		assert.Equal(t, int64(2), snap.Revision, "revision must advance on change")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for file source watch event")
	}
}

func TestFileSourceFetchKeepsRevision(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "mode"), []byte("prod"), 0o644))

	src := NewFileSource(root)

	first, err := src.Fetch(context.Background())
	require.NoError(t, err)
	second, err := src.Fetch(context.Background())
	require.NoError(t, err)

	assert.Equal(t, int64(1), first.Revision)
	assert.Equal(t, first.Revision, second.Revision, "a plain fetch must not bump the revision")
}
