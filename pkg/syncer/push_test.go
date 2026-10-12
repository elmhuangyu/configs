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

func TestCollectLocalFilesHonorsIgnore(t *testing.T) {
	root := t.TempDir()

	// Built-in excludes: VCS metadata and SOPS' own configuration.
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "config"), []byte("gitcfg"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".sops.yaml"), []byte("creation_rules: []\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0o644))

	// User patterns from .cfgctlignore.
	require.NoError(t, os.WriteFile(filepath.Join(root, ".cfgctlignore"), []byte("# ignored\nsecret/\n*.tmp\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secret"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secret", "a.yaml"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "release.tmp"), []byte("x"), 0o644))

	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), []byte("a: b\n"), 0o644))

	files, err := CollectLocalFiles(root)
	require.NoError(t, err)

	assert.Equal(t, map[string][]byte{"app.yaml": []byte("a: b\n")}, files)
}
