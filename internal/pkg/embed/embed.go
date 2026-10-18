// Package embed loads a directory of assets for compile-time embedding.
//
// The compiler resolves the asset builtins against a Set at const-fold time
// (see internal/check/constfold): a text form becomes a string literal and a
// bytes form a u8[] literal, so this package deliberately knows nothing about
// codegen. File contents are held as loaded; whether a file must be UTF-8 is
// the call site's question. A file NAME reaches the program as a string, so
// Load refuses one that is not UTF-8. docs/EMBED.md is the contract.
package embed

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Set is a loaded asset bundle: lookup name -> file contents. Names are
// slash-separated paths relative to the embed root, so `-embed ./assets`
// makes `assets/html/index.html` available as "html/index.html" on every
// host OS.
type Set struct {
	root  string
	files map[string]string
}

// Root reports the directory the set was loaded from, for diagnostics.
func (s *Set) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// Lookup returns the bytes embedded under name.
func (s *Set) Lookup(name string) (string, bool) {
	if s == nil {
		return "", false
	}
	v, ok := s.files[name]
	return v, ok
}

// Names lists every embedded asset in sorted order — stable across hosts,
// so diagnostics and any generated table are deterministic.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.files))
	for n := range s.files {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Load reads every regular file under dir into a Set.
//
// Symlinks are not followed: WalkDir reports a symlink as an irregular file
// and it is skipped, so an asset tree can never reach outside its root or
// wedge the compiler on a cycle.
func Load(dir string) (*Set, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("-embed %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("-embed %s: not a directory", dir)
	}
	// WalkDir lstats its root, so a `-embed` naming a symlink to a directory
	// yields the link itself as one non-regular entry and the skip below drops
	// it: an empty bundle from a directory full of files, with nothing said.
	// os.Stat has already followed the link to decide this IS a directory, so
	// the walk follows it too and the two agree. Entries under the root are
	// still lstat'd by WalkDir and still skipped — it is the root that is named
	// on the command line, and only the root.
	walkRoot, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("-embed %s: %w", dir, err)
	}
	set := &Set{root: dir, files: map[string]string{}}
	err = filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(walkRoot, path)
		if err != nil {
			return err
		}
		// A name reaches the program as a string, so it must be text.
		if !utf8.ValidString(rel) {
			return fmt.Errorf("file name %q is not valid UTF-8", filepath.ToSlash(rel))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		set.files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("-embed %s: %w", dir, err)
	}
	return set, nil
}

// Suggest returns the embedded name closest to want, for a "did you mean"
// on a misspelled asset. It reports "" when nothing is close enough — a
// suggestion that shares no prefix with the typo is noise, not help.
func (s *Set) Suggest(want string) string {
	best, bestScore := "", 0
	for _, n := range s.Names() {
		score := commonAffix(n, want)
		if score > bestScore {
			best, bestScore = n, score
		}
	}
	// Require a third of the typed name to match before offering it.
	if bestScore*3 < len(want) {
		return ""
	}
	return best
}

// commonAffix scores two names by the longer of their shared prefix and
// shared suffix, which catches both a mistyped tail ("index.htm") and a
// wrong directory ("html/app.js" vs "js/app.js").
func commonAffix(a, b string) int {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a) && suf < len(b) && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	if suf > pre {
		return suf
	}
	return pre
}

// FormatAvailable renders the embedded names for an error message, capped
// so a bundle of hundreds does not hide the diagnostic that matters.
func (s *Set) FormatAvailable() string {
	names := s.Names()
	if len(names) == 0 {
		return "no assets were embedded"
	}
	const max = 10
	shown := names
	suffix := ""
	if len(shown) > max {
		shown = shown[:max]
		suffix = fmt.Sprintf(" (and %d more)", len(names)-max)
	}
	return "embedded assets: " + strings.Join(shown, ", ") + suffix
}
