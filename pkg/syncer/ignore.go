package syncer

import (
	"bufio"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	gitignore "github.com/sabhiram/go-gitignore"
)

// IgnoreFileName is the optional gitignore-style file honoured by both
// `cfgctl encrypt` and `cfgctl push`. It lives at the root of the processed
// directory and its patterns are matched against paths relative to that root.
const IgnoreFileName = ".cfgctlignore"

// isBuiltinExcluded reports whether rel (slash separated, relative to the walk
// root) is covered by a built-in exclude.
func isBuiltinExcluded(rel string) bool {
	if rel == "" || rel == "." {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".git" {
			return true
		}
	}
	switch path.Base(rel) {
	case ".gitignore", IgnoreFileName, ".sops.yaml":
		return true
	}
	return false
}

// ignoreMatcher combines the built-in excludes with the patterns of a
// .cfgctlignore file.
type ignoreMatcher struct {
	patterns *gitignore.GitIgnore
}

// loadIgnore reads <root>/.cfgctlignore if present. A missing file is not an
// error; the built-in excludes still apply.
func loadIgnore(root string) (*ignoreMatcher, error) {
	lines, err := readLines(filepath.Join(root, IgnoreFileName))
	if err != nil {
		return nil, err
	}
	return &ignoreMatcher{patterns: gitignore.CompileIgnoreLines(lines...)}, nil
}

// matches reports whether rel is excluded from processing.
func (m *ignoreMatcher) matches(rel string) bool {
	if isBuiltinExcluded(rel) {
		return true
	}
	return m.patterns.MatchesPath(rel)
}

func readLines(name string) ([]string, error) {
	f, err := os.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

// walkConfigFiles walks root, applying the ignore rules, and invokes fn for
// every non-ignored regular file. fn receives the slash-separated path relative
// to root and the full path. Ignored directories are pruned.
func walkConfigFiles(root string, fn func(rel, full string) error) error {
	ign, err := loadIgnore(root)
	if err != nil {
		return err
	}

	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if rel != "." && ign.matches(rel) {
				return fs.SkipDir
			}
			return nil
		}
		if ign.matches(rel) {
			return nil
		}
		return fn(rel, p)
	})
}
