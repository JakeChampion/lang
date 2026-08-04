// Package embed loads a directory of assets for compile-time embedding.
//
// The compiler resolves `__fern_asset("name")` against a Set at const-fold
// time, replacing the call with a plain string literal (see internal/constfold).
// Everything downstream — interning, the immortal rc sentinel, all four
// backends — then treats an asset exactly like any other string literal, so
// this package deliberately knows nothing about codegen.
//
// Asset bytes are arbitrary: the emitted literal carries an explicit byte
// length at data-4, so the `.asciz` NUL terminator is never load-bearing and
// binary assets (images, fonts, wasm) round-trip unchanged.
//
// # Cost of the string-literal route
//
// Assets reach the native backends as GAS assembly text via escapeForGAS,
// which spends 4 characters on each byte below 32 (`\NNN` octal) and 1 on
// most others. Measured on this repository's own site bundle (HTML, JS, CSS,
// SVG, WOFF2, PNG — 494 KB): 1.31x overall. The spread is what matters:
//
//	pure ASCII text     1.00x
//	HTML / CSS / JS     1.03x - 1.17x
//	compressed binary   1.37x - 1.51x   (PNG, WOFF2)
//	all-NUL bytes       4.00x           (worst case)
//
// 4x is reachable only by data that is overwhelmingly low bytes — which is
// also data that compresses to near nothing, and assets served over HTTP are
// meant to be stored pre-compressed anyway. Issue #6069 budgeted for ~4x as
// the expected case and proposed splicing raw bytes into a section from the
// in-process linker to avoid it; the measurement says realistic bundles cost
// ~1.3x, so that path is not built.
package embed

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	set := &Set{root: dir, files: map[string]string{}}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
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
// so a bundle of hundreds does not bury the diagnostic that matters.
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
