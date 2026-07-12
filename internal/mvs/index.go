package mvs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source locates a specific package version's archive/directory: a local
// directory (Path) or a hash-addressed remote archive (URL+Hash). Path
// entries make the index fully usable offline (a local monorepo of
// versioned packages); URL entries flow through the content-addressed
// store exactly like a `url` dependency.
type Source struct {
	Path string // absolute (resolved against the index dir) or relative dir
	URL  string
	Hash string
}

// Index maps package name → version string → source. It is the version
// authority MVS resolves against — a plain file, so no registry service
// is required (the research's "no mandatory registry" stance).
type Index struct {
	Dir      string // directory the index file lives in (for relative Paths)
	Packages map[string]map[string]Source
}

// Versions returns pkg's available versions, ascending.
func (ix *Index) Versions(pkg string) []Version {
	var vs []Version
	for s := range ix.Packages[pkg] {
		if v, err := ParseVersion(s); err == nil {
			vs = append(vs, v)
		}
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].Compare(vs[j]) < 0 })
	return vs
}

// Latest returns pkg's highest available version.
func (ix *Index) Latest(pkg string) (Version, bool) {
	vs := ix.Versions(pkg)
	if len(vs) == 0 {
		return Version{}, false
	}
	return vs[len(vs)-1], true
}

// SourceFor returns the source for a specific (pkg, version).
func (ix *Index) SourceFor(pkg string, v Version) (Source, bool) {
	s, ok := ix.Packages[pkg][v.String()]
	if !ok {
		return Source{}, false
	}
	if s.Path != "" && !filepath.IsAbs(s.Path) {
		s.Path = filepath.Join(ix.Dir, s.Path)
	}
	return s, true
}

// LoadIndex reads and parses an index file.
func LoadIndex(path string) (*Index, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("index %s: %w", path, err)
	}
	ix, err := ParseIndex(string(b))
	if err != nil {
		return nil, fmt.Errorf("index %s: %w", path, err)
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		dir = filepath.Dir(path)
	}
	ix.Dir = dir
	return ix, nil
}

// ParseIndex parses the index format: a `[package]` section per package,
// each line `"VERSION" = { path = "…" }` or
// `"VERSION" = { url = "…", hash = "sha256:…" }`.
func ParseIndex(src string) (*Index, error) {
	ix := &Index{Packages: map[string]map[string]Source{}}
	pkg := ""
	for ln, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("line %d: malformed section header %q", ln+1, line)
			}
			pkg = strings.TrimSpace(line[1 : len(line)-1])
			if pkg == "" {
				return nil, fmt.Errorf("line %d: empty package name", ln+1)
			}
			if ix.Packages[pkg] == nil {
				ix.Packages[pkg] = map[string]Source{}
			}
			continue
		}
		if pkg == "" {
			return nil, fmt.Errorf("line %d: version entry outside a [package] section", ln+1)
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected `\"VERSION\" = { … }`", ln+1)
		}
		ver, err := unquote(strings.TrimSpace(line[:eq]))
		if err != nil {
			return nil, fmt.Errorf("line %d: version key: %w", ln+1, err)
		}
		if _, err := ParseVersion(ver); err != nil {
			return nil, fmt.Errorf("line %d: %w", ln+1, err)
		}
		src, err := parseSource(strings.TrimSpace(line[eq+1:]))
		if err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", ln+1, ver, err)
		}
		ix.Packages[pkg][ver] = src
	}
	return ix, nil
}

func parseSource(val string) (Source, error) {
	if !strings.HasPrefix(val, "{") || !strings.HasSuffix(val, "}") {
		return Source{}, fmt.Errorf("expected an inline table { path = … } or { url = …, hash = … }")
	}
	var s Source
	for _, part := range strings.Split(strings.TrimSpace(val[1:len(val)-1]), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.Index(part, "=")
		if eq < 0 {
			return Source{}, fmt.Errorf("expected `key = value`, got %q", part)
		}
		k := strings.TrimSpace(part[:eq])
		v, err := unquote(strings.TrimSpace(part[eq+1:]))
		if err != nil {
			return Source{}, fmt.Errorf("%s: %w", k, err)
		}
		switch k {
		case "path":
			s.Path = filepath.FromSlash(v)
		case "url":
			s.URL = v
		case "hash":
			s.Hash = v
		default:
			return Source{}, fmt.Errorf("unknown key %q (supported: path, url, hash)", k)
		}
	}
	switch {
	case s.Path != "" && (s.URL != "" || s.Hash != ""):
		return Source{}, fmt.Errorf("a version is either `path` or `url`+`hash`")
	case s.Path != "":
		return s, nil
	case s.URL != "" && s.Hash != "":
		return s, nil
	default:
		return Source{}, fmt.Errorf("a version needs `path` or `url`+`hash`")
	}
}

func unquote(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("expected a quoted string, got %q", s)
	}
	inner := s[1 : len(s)-1]
	if strings.ContainsAny(inner, "\"\\") {
		return "", fmt.Errorf("escapes unsupported in %q", s)
	}
	return inner, nil
}
