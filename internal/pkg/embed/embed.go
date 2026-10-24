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

type Set struct {
	root  string
	files map[string]string
}

func (s *Set) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Set) Lookup(name string) (string, bool) {
	if s == nil {
		return "", false
	}
	v, ok := s.files[name]
	return v, ok
}

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

func Load(dir string) (*Set, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("-embed %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("-embed %s: not a directory", dir)
	}
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

func (s *Set) Suggest(want string) string {
	best, bestScore := "", 0
	for _, n := range s.Names() {
		score := commonAffix(n, want)
		if score > bestScore {
			best, bestScore = n, score
		}
	}
	if bestScore*3 < len(want) {
		return ""
	}
	return best
}

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
