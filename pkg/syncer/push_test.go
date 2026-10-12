package syncer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectLocalFilesMapsLogicalKeys(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "agents", "coder"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "agents", "coder", "manifest.yaml"), []byte("model: x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "agents", "coder", "prompt.txt"), []byte("plain"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "agents", "coder", "prompt.txt.sops.json"), []byte(`{"sops":1}`), 0o644))

	files, err := CollectLocalFiles(root)
	require.NoError(t, err)

	assert.Equal(t, map[string][]byte{
		"agents/coder/manifest.yaml": []byte("model: x\n"),
		"agents/coder/prompt.txt":    []byte(`{"sops":1}`),
	}, files)
}

func TestCollectLocalFilesEmptyDir(t *testing.T) {
	files, err := CollectLocalFiles(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, files)
}
