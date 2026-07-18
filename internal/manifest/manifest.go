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

// Dep is one declared dependency, from exactly one source:
//   - Path: a directory containing the dependency package, relative to
//     the manifest's own directory (or absolute).
//   - URL + Hash: a hash-addressed remote archive (.tar.gz). THE HASH
//     IS THE IDENTITY — `sha256:<hex>` of the archive bytes; the URL is
//     just a mirror hint (the Zig/Roc model the research settled on,
//     closing trust-on-first-use with zero infrastructure). Fetching is
//     an explicit `fern fetch` step into the content-addressed cache;
//     the compiler never touches the network.
type Dep struct {
	Path string
	URL  string
	Hash string // "sha256:<64 hex>" — of the archive bytes
	// Workspace is true for a `{ workspace = true }` dependency: the
	// dependency is another member of the enclosing workspace, located by
	// its package name rather than a path/url. Keeps cross-member deps
	// explicit (isolation) while removing brittle `../../member` paths.
	Workspace bool
	// Version is the Minimum-Version-Selection constraint of a versioned
	// dependency (`dep = "1.2.0"` or `{ version = "1.2.0" }`): the LOWEST
	// acceptable version. The concrete version is chosen by MVS over the
	// [package] index and pinned in fern.lock — never a range, so
	// resolution stays deterministic and lockfile-driven (internal/mvs).
	Version string
}

// Manifest is a parsed fern.toml.
type Manifest struct {
	Dir     string // absolute directory containing the manifest
	Name    string // [package] name (empty for a workspace-only manifest)
	Version string // [package] version (informational; a package's own version)
	Lib     string // [package] lib — entry module for `import "<name>"`; DefaultLib when unset
	Index   string // [package] index — path to the version index for MVS deps (empty = none)
	Deps    map[string]Dep
	// Members is the [workspace] members list (relative directories),
	// non-nil only when a [workspace] table is present. A manifest may be
	// workspace-only (no [package]) or both a package and a workspace root.
	Members []string
	// Excludes is the [exclude] table: package name → versions banned from
	// MVS ("1.9 is broken"). Go's answer to MVS's inexpressible upper
	// bounds, copied per the SOTA research: a demand for an excluded
	// version rounds up to the next higher non-excluded indexed version.
	// Only the TOP-LEVEL manifest's excludes apply during `fern -resolve`
	// (a dependency's [exclude] is ignored), preserving determinism.
	Excludes map[string][]string
}

// VersionDeps returns the names→min-version of this manifest's versioned
// (MVS) dependencies.
func (m *Manifest) VersionDeps() map[string]string {
	out := map[string]string{}
	for name, d := range m.Deps {
		if d.Version != "" {
			out[name] = d.Version
		}
	}
	return out
}

// IsWorkspace reports whether this manifest declares a [workspace] table.
func (m *Manifest) IsWorkspace() bool { return m.Members != nil }

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

// MemberDir resolves a [workspace] member entry to an absolute
// directory (members are relative to the workspace manifest's dir).
func (m *Manifest) MemberDir(rel string) string {
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Join(m.Dir, rel)
}

// FindWorkspace walks from dir toward the filesystem root and returns
// the nearest manifest declaring a [workspace] table, or (nil, nil) if
// none governs dir.
func FindWorkspace(dir string) (*Manifest, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for {
		m, err := Load(d)
		if err != nil {
			return nil, err
		}
		if m != nil && m.IsWorkspace() {
			return m, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return nil, nil
		}
		d = parent
	}
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
			case "package", "dependencies", "exclude":
			case "workspace":
				if m.Members == nil {
					m.Members = []string{}
				}
			default:
				return nil, fmt.Errorf("line %d: unknown section [%s] (supported: [package], [dependencies], [workspace], [exclude])", ln+1, section)
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
			case "index":
				m.Index = filepath.FromSlash(s)
			default:
				return nil, fmt.Errorf("line %d: unknown [package] key %q (supported: name, version, lib, index)", ln+1, key)
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
		case "workspace":
			if key != "members" {
				return nil, fmt.Errorf("line %d: unknown [workspace] key %q (supported: members)", ln+1, key)
			}
			ms, err := parseStringArray(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: members: %w", ln+1, err)
			}
			m.Members = ms
		case "exclude":
			if !validDepName(key) {
				return nil, fmt.Errorf("line %d: invalid [exclude] package name %q (letters, digits, `_`, `-`; must not start with a digit)", ln+1, key)
			}
			vs, err := parseStringArray(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: exclude %s: %w", ln+1, key, err)
			}
			for _, v := range vs {
				if !isVersion(v) {
					return nil, fmt.Errorf("line %d: exclude %s: version must be MAJOR.MINOR.PATCH digits, got %q", ln+1, key, v)
				}
			}
			if m.Excludes == nil {
				m.Excludes = map[string][]string{}
			}
			m.Excludes[key] = vs
		default:
			return nil, fmt.Errorf("line %d: %q outside a section (start with [package], [dependencies], or [workspace])", ln+1, key)
		}
	}
	// A workspace-only manifest (a virtual root that just lists members)
	// needs no [package] name; a package manifest still does.
	if m.Name == "" && !m.IsWorkspace() {
		return nil, fmt.Errorf("missing [package] name")
	}
	return m, nil
}

// parseStringArray parses an inline TOML array of double-quoted strings
// on a single line: `["a", "b/c"]`. Empty (`[]`) is allowed.
func parseStringArray(val string) ([]string, error) {
	if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
		return nil, fmt.Errorf("expected an array like [\"a\", \"b\"], got %q", val)
	}
	body := strings.TrimSpace(val[1 : len(val)-1])
	out := []string{}
	if body == "" {
		return out, nil
	}
	for _, part := range strings.Split(body, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		s, err := parseString(part)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.FromSlash(s))
	}
	return out, nil
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

// parseDep accepts the declared-dependency forms this slice supports:
// `{ path = "…" }`, `{ url = "…", hash = "sha256:…" }`, and
// `{ workspace = true }` — version-only deps (`dep = "1.2"`) belong to
// the registry/MVS slice and error with a pointer at what IS supported.
func parseDep(val string) (Dep, error) {
	// Bare-string form: `dep = "1.2.0"` is a versioned (MVS) dependency,
	// its value the minimum acceptable version. The concrete version comes
	// from the index via MVS and is pinned in fern.lock.
	if !strings.HasPrefix(val, "{") {
		s, err := parseString(val)
		if err != nil {
			return Dep{}, fmt.Errorf("a bare dependency value must be a version string like \"1.2.0\" (or use an inline table): %w", err)
		}
		if !isVersion(s) {
			return Dep{}, fmt.Errorf("version must be MAJOR.MINOR.PATCH digits, got %q", s)
		}
		return Dep{Version: s}, nil
	}
	if !strings.HasSuffix(val, "}") {
		return Dep{}, fmt.Errorf("malformed inline table %q", val)
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
		if k == "workspace" {
			if v != "true" {
				return Dep{}, fmt.Errorf("workspace must be `true` (the only supported value), got %q", v)
			}
			dep.Workspace = true
			continue
		}
		s, err := parseString(v)
		if err != nil {
			return Dep{}, fmt.Errorf("%s: %w", k, err)
		}
		switch k {
		case "path":
			if s == "" {
				return Dep{}, fmt.Errorf("path must not be empty")
			}
			dep.Path = filepath.FromSlash(s)
		case "url":
			if !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "http://") {
				return Dep{}, fmt.Errorf("url must be http(s), got %q", s)
			}
			dep.URL = s
		case "hash":
			hex, ok := strings.CutPrefix(s, "sha256:")
			if !ok || len(hex) != 64 || !isHex(hex) {
				return Dep{}, fmt.Errorf("hash must be `sha256:` + 64 hex digits of the archive bytes, got %q", s)
			}
			dep.Hash = s
		case "version":
			if !isVersion(s) {
				return Dep{}, fmt.Errorf("version must be MAJOR.MINOR.PATCH digits, got %q", s)
			}
			dep.Version = s
		default:
			return Dep{}, fmt.Errorf("unknown dependency key %q (supported: path, url, hash, workspace, version)", k)
		}
	}
	switch {
	case dep.Workspace && (dep.Path != "" || dep.URL != "" || dep.Hash != "" || dep.Version != ""):
		return Dep{}, fmt.Errorf("a `workspace` dependency takes no path/url/hash/version")
	case dep.Workspace:
		return dep, nil
	case dep.Version != "" && (dep.Path != "" || dep.URL != "" || dep.Hash != ""):
		return Dep{}, fmt.Errorf("a `version` dependency takes no path/url/hash (the version resolves through the index)")
	case dep.Version != "":
		return dep, nil
	case dep.Path != "" && (dep.URL != "" || dep.Hash != ""):
		return Dep{}, fmt.Errorf("a dependency is either `path` or `url`+`hash`, not both")
	case dep.Path != "":
		return dep, nil
	case dep.URL != "" && dep.Hash != "":
		return dep, nil
	case dep.URL != "" || dep.Hash != "":
		return Dep{}, fmt.Errorf("url dependencies need BOTH `url` and `hash` — the hash is the identity, the url just a mirror hint")
	default:
		return Dep{}, fmt.Errorf("missing `path` (or `url` + `hash`, `workspace = true`, or a version)")
	}
}

// isVersion reports whether s is a bare MAJOR.MINOR.PATCH version (the
// only form MVS constraints and the index use — no ranges, no
// pre-release, keeping resolution deterministic).
func isVersion(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
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
