// Package manifest reads `fern.toml`, the per-package manifest that
// names a Fern package and declares its dependencies. This is the
// first implemented slice of the package-management design in
// docs/PACKAGE-MANAGEMENT-SOTA.md + docs/MODULE-PACKAGES-RESEARCH.md
// (Rec §1): local `path` dependencies only — no registry, no network,
// no lockfile yet. A manifest-less program keeps today's behaviour;
// once a `fern.toml` governs a file, bare (non-`./`, non-std) import
// prefixes may name a declared dependency and resolve into that
// package's directory, and modload enforces that a dependency import
// is declared (resolver-side isolation — the phantom-dependency
// defence layout alone cannot give, per the refuted pnpm claim the
// SOTA doc records).
//
// The parser accepts the small TOML subset the manifest needs —
// `[section]` headers, `key = "string"` pairs, and inline tables
// `key = { k = "v", ... }` — and rejects everything else with a
// pointed error. Zero third-party dependencies, mirroring the
// repo's Go module (and the "manifests must be parseable cheaply"
// stance the research doc takes against manifests-as-code).
package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the manifest file modload looks for next to (or above)
// a loaded module.
const FileName = "fern.toml"

// DefaultLib is the module a bare `import "<dep>"` resolves to inside
// the dependency's directory when its manifest doesn't set `lib`.
const DefaultLib = "lib.fern"

// Dep is one declared dependency. Path is the only supported source
// in this slice: a directory containing the dependency package,
// relative to the manifest's own directory (or absolute).
type Dep struct {
	Path string
}

// Manifest is a parsed fern.toml.
type Manifest struct {
	Dir     string // absolute directory containing the manifest
	Name    string // [package] name
	Version string // [package] version (informational in this slice)
	Lib     string // [package] lib — entry module for `import "<name>"`; DefaultLib when unset
	Deps    map[string]Dep
}

// DepDir resolves a declared dependency's directory to an absolute
// path (Path entries are relative to the manifest directory).
func (m *Manifest) DepDir(name string) (string, bool) {
	d, ok := m.Deps[name]
	if !ok {
		return "", false
	}
	if filepath.IsAbs(d.Path) {
		return filepath.Clean(d.Path), true
	}
	return filepath.Join(m.Dir, d.Path), true
}

// Load reads and parses `<dir>/fern.toml`. Returns (nil, nil) when the
// file does not exist.
func Load(dir string) (*Manifest, error) {
	p := filepath.Join(dir, FileName)
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := Parse(string(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	m.Dir = abs
	return m, nil
}

// FindForDir walks from dir toward the filesystem root and loads the
// nearest fern.toml. Returns (nil, nil) when no manifest governs dir.
func FindForDir(dir string) (*Manifest, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for {
		m, err := Load(d)
		if err != nil {
			return nil, err
		}
		if m != nil {
			return m, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return nil, nil
		}
		d = parent
	}
}

// Parse parses manifest source. Exposed for tests and future tooling
// (`fern add` will rewrite manifests; keeping parse round-trippable
// and strict now is cheaper than loosening later).
func Parse(src string) (*Manifest, error) {
	m := &Manifest{Lib: DefaultLib, Deps: map[string]Dep{}}
	section := ""
	for ln, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("line %d: malformed section header %q", ln+1, line)
			}
			section = strings.TrimSpace(line[1 : len(line)-1])
			switch section {
			case "package", "dependencies":
			default:
				return nil, fmt.Errorf("line %d: unknown section [%s] (supported: [package], [dependencies])", ln+1, section)
			}
			continue
		}
		key, val, ok := splitKeyValue(line)
		if !ok {
			return nil, fmt.Errorf("line %d: expected `key = value`, got %q", ln+1, line)
		}
		switch section {
		case "package":
			s, err := parseString(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %w", ln+1, key, err)
			}
			switch key {
			case "name":
				m.Name = s
			case "version":
				m.Version = s
			case "lib":
				m.Lib = s
			default:
				return nil, fmt.Errorf("line %d: unknown [package] key %q (supported: name, version, lib)", ln+1, key)
			}
		case "dependencies":
			dep, err := parseDep(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: dependency %q: %w", ln+1, key, err)
			}
			if !validDepName(key) {
				return nil, fmt.Errorf("line %d: invalid dependency name %q (letters, digits, `_`, `-`; must not start with a digit)", ln+1, key)
			}
			m.Deps[key] = dep
		default:
			return nil, fmt.Errorf("line %d: %q outside a section (start with [package] or [dependencies])", ln+1, key)
		}
	}
	if m.Name == "" {
		return nil, fmt.Errorf("missing [package] name")
	}
	return m, nil
}

func splitKeyValue(line string) (key, val string, ok bool) {
	i := strings.Index(line, "=")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

// parseString accepts a double-quoted basic string with no escapes —
// paths and names don't need them, and rejecting `\` early avoids
// silently mangling Windows-style separators (write `/`; they work on
// every platform via filepath.Join).
func parseString(val string) (string, error) {
	if len(val) < 2 || val[0] != '"' || val[len(val)-1] != '"' {
		return "", fmt.Errorf("expected a double-quoted string, got %q", val)
	}
	s := val[1 : len(val)-1]
	if strings.ContainsAny(s, "\"\\") {
		return "", fmt.Errorf("escapes are not supported in %q (use forward slashes in paths)", val)
	}
	return s, nil
}

// parseDep accepts the two declared-dependency forms this slice
// supports: an inline table `{ path = "…" }`, and nothing else —
// version-only deps (`dep = "1.2"`) belong to the registry/MVS slice
// and error with a pointer at what IS supported.
func parseDep(val string) (Dep, error) {
	if !strings.HasPrefix(val, "{") || !strings.HasSuffix(val, "}") {
		return Dep{}, fmt.Errorf("only path dependencies are supported in this slice — write `{ path = \"../dir\" }`")
	}
	body := strings.TrimSpace(val[1 : len(val)-1])
	dep := Dep{}
	for _, part := range strings.Split(body, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := splitKeyValue(part)
		if !ok {
			return Dep{}, fmt.Errorf("expected `key = value` in inline table, got %q", part)
		}
		switch k {
		case "path":
			s, err := parseString(v)
			if err != nil {
				return Dep{}, err
			}
			if s == "" {
				return Dep{}, fmt.Errorf("path must not be empty")
			}
			dep.Path = filepath.FromSlash(s)
		default:
			return Dep{}, fmt.Errorf("unknown dependency key %q (supported: path)", k)
		}
	}
	if dep.Path == "" {
		return Dep{}, fmt.Errorf("missing `path`")
	}
	return dep, nil
}

func validDepName(s string) bool {
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
