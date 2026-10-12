package cfgfs

import (
	"bufio"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// IgnoreFileName is the optional gitignore-style file honoured by the Walk
// helper. It lives at the root of the processed directory and its patterns are
// matched against paths relative to that root.
const IgnoreFileName = ".cfgctlignore"

// commentPrefix marks a comment line in an ignore file, matching git.
const commentPrefix = "#"

// WalkFunc is invoked for every non-ignored regular file found by Walk. rel is
// the slash-separated path relative to root; full is the path to open.
type WalkFunc func(rel, full string) error

// IsBuiltinExcluded reports whether rel (slash separated, relative to the walk
// root) is covered by a built-in exclude: VCS metadata and SOPS' own
// configuration, which must never be treated as configuration nor re-encrypted
// in place.
func IsBuiltinExcluded(rel string) bool {
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

// Matcher combines the built-in excludes with the patterns of a .cfgctlignore
// file.
type Matcher struct {
	patterns gitignore.Matcher
}

// LoadIgnore reads <root>/.cfgctlignore if present. A missing file is not an
// error; the built-in excludes still apply. Comment and blank lines are
// skipped, as in git.
func LoadIgnore(root string) (*Matcher, error) {
	lines, err := readIgnoreLines(filepath.Join(root, IgnoreFileName))
	if err != nil {
		return nil, err
	}

	patterns := make([]gitignore.Pattern, 0, len(lines))
	for _, line := range lines {
		patterns = append(patterns, gitignore.ParsePattern(line, nil))
	}
	return &Matcher{patterns: gitignore.NewMatcher(patterns)}, nil
}

// Matches reports whether rel is excluded from processing.
func (m *Matcher) Matches(rel string, isDir bool) bool {
	if IsBuiltinExcluded(rel) {
		return true
	}
	return m.patterns.Match(strings.Split(rel, "/"), isDir)
}

// readIgnoreLines returns the meaningful lines of an ignore file, dropping
// comments and blanks.
func readIgnoreLines(name string) ([]string, error) {
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
		line := sc.Text()
		if strings.HasPrefix(line, commentPrefix) || strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines, sc.Err()
}

// Walk walks root, applying the ignore rules, and invokes fn for every
// non-ignored regular file. Ignored directories are pruned.
func Walk(root string, fn WalkFunc) error {
	ign, err := LoadIgnore(root)
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
			if rel != "." && ign.Matches(rel, true) {
				return fs.SkipDir
			}
			return nil
		}
		if ign.Matches(rel, false) {
			return nil
		}
		return fn(rel, p)
	})
}
