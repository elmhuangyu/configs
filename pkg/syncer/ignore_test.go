package syncer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsBuiltinExcluded(t *testing.T) {
	for _, rel := range []string{
		".git/config",
		"sub/.git/config",
		".sops.yaml",
		".cfgctlignore",
		"nested/.gitignore",
	} {
		assert.True(t, isBuiltinExcluded(rel), rel)
	}

	for _, rel := range []string{".", "", "app.yaml", "secrets/x.yaml", "gitignore"} {
		assert.False(t, isBuiltinExcluded(rel), rel)
	}
}

func TestIgnoreMatcherPatterns(t *testing.T) {
	root := t.TempDir()
	// Comments and blank lines are ignored; later patterns take priority.
	require.NoError(t, os.WriteFile(filepath.Join(root, IgnoreFileName),
		[]byte("# a comment\n\nsecrets/\n*.tmp\n!secrets/keep.yaml\n"), 0o644))

	ign, err := loadIgnore(root)
	require.NoError(t, err)

	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"secrets/x.yaml", false, true},
		{"secrets", true, true},
		{"secrets/keep.yaml", false, false}, // negated
		{"release.tmp", false, true},
		{"app.yaml", false, false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, ign.matches(tc.rel, tc.isDir), "%s (isDir=%v)", tc.rel, tc.isDir)
	}

	// Built-in excludes apply regardless of the file.
	assert.True(t, ign.matches(".sops.yaml", false))
	assert.True(t, ign.matches(".git/config", false))
}

func TestLoadIgnoreMissingFile(t *testing.T) {
	ign, err := loadIgnore(t.TempDir())
	require.NoError(t, err)
	assert.False(t, ign.matches("app.yaml", false))
	assert.True(t, ign.matches(".sops.yaml", false))
}

func TestWalkConfigFilesNegation(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secrets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "private.yaml"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secrets", "public.yaml"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), []byte("x"), 0o644))

	// A glob, not a directory exclusion, so re-inclusion works.
	require.NoError(t, os.WriteFile(filepath.Join(root, IgnoreFileName),
		[]byte("secrets/*\n!secrets/public.yaml\n"), 0o644))

	var seen []string
	require.NoError(t, walkConfigFiles(root, func(rel, _ string) error {
		seen = append(seen, rel)
		return nil
	}))

	assert.ElementsMatch(t, []string{"app.yaml", "secrets/public.yaml"}, seen)
}
