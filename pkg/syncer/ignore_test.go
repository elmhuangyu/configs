package syncer

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
