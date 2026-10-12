package cfgfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogicalKey(t *testing.T) {
	assert.Equal(t, "agents/coder/prompt.txt", LogicalKey("agents/coder/prompt.txt.sops.json"))
	assert.Equal(t, "app.yaml", LogicalKey("app.yaml"))
	assert.Equal(t, "a.sops.json", LogicalKey("a.sops.json.sops.json"))
}

func TestIsBuiltinExcluded(t *testing.T) {
	for _, rel := range []string{
		".git/config",
		"sub/.git/config",
		".sops.yaml",
		".cfgctlignore",
		"nested/.gitignore",
	} {
		assert.True(t, IsBuiltinExcluded(rel), rel)
	}

	for _, rel := range []string{".", "", "app.yaml", "secrets/x.yaml", "gitignore"} {
		assert.False(t, IsBuiltinExcluded(rel), rel)
	}
}

func TestMatcherPatterns(t *testing.T) {
	root := t.TempDir()
	// Comments and blank lines are ignored; later patterns take priority.
	require.NoError(t, os.WriteFile(filepath.Join(root, IgnoreFileName),
		[]byte("# a comment\n\nsecrets/\n*.tmp\n!secrets/keep.yaml\n"), 0o644))

	m, err := LoadIgnore(root)
	require.NoError(t, err)

	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"secrets/x.yaml", false, true},
		{"secrets", true, true},
		{"release.tmp", false, true},
		{"app.yaml", false, false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, m.Matches(tc.rel, tc.isDir), "%s (isDir=%v)", tc.rel, tc.isDir)
	}

	// Built-in excludes apply regardless of the file.
	assert.True(t, m.Matches(".sops.yaml", false))
	assert.True(t, m.Matches(".git/config", false))
}

func TestLoadIgnoreMissingFile(t *testing.T) {
	m, err := LoadIgnore(t.TempDir())
	require.NoError(t, err)
	assert.False(t, m.Matches("app.yaml", false))
	assert.True(t, m.Matches(".sops.yaml", false))
}

func TestWalk(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "agents"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "config"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".sops.yaml"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "agents", "prompt.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, IgnoreFileName), []byte("agents/secret.txt\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "agents", "secret.txt"), []byte("x"), 0o644))

	var seen []string
	require.NoError(t, Walk(root, func(rel, _ string) error {
		seen = append(seen, rel)
		return nil
	}))

	assert.ElementsMatch(t, []string{"app.yaml", "agents/prompt.txt"}, seen)
}

func TestWalkNegation(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "private.yaml"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "public.yaml"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), []byte("x"), 0o644))

	// A glob, not a directory exclusion, so re-inclusion works.
	require.NoError(t, os.WriteFile(filepath.Join(root, IgnoreFileName),
		[]byte("secrets/*\n!secrets/public.yaml\n"), 0o644))

	var seen []string
	require.NoError(t, Walk(root, func(rel, _ string) error {
		seen = append(seen, rel)
		return nil
	}))

	assert.ElementsMatch(t, []string{"app.yaml", "secrets/public.yaml"}, seen)
}
