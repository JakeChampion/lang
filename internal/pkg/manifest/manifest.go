package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/pkg/caps"
	"github.com/jakechampion/lang/internal/pkg/pkgcache"
)

const FileName = "fern.toml"

const DefaultLib = "lib.fern"

type Dep struct {
	Path         string
	URL          string
	Hash         string
	Workspace    bool
	Version      string
	Capabilities []string
}

type Manifest struct {
	Dir      string
	Name     string
	Version  string
	Lib      string
	Index    string
	Deps     map[string]Dep
	Members  []string
	Excludes map[string][]string
}

func (m *Manifest) VersionDeps() map[string]string {
	out := map[string]string{}
	for name, d := range m.Deps {
		if d.Version != "" {
			out[name] = d.Version
		}
	}
	return out
}

func (m *Manifest) IsWorkspace() bool { return m.Members != nil }

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

func (m *Manifest) MemberDir(rel string) string {
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Join(m.Dir, rel)
}

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
		if me, ok := err.(*Error); ok {
			me.Path = p
		}
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	m.Dir = abs
	return m, nil
}

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

type Error struct {
	Path string
	Line int
	Msg  string
}

func (e *Error) Error() string {
	at := fmt.Sprintf("%d:1", e.Line)
	if e.Path != "" {
		at = e.Path + ":" + at
	}
	return at + ": error[E088]: " + e.Msg
}

func (e *Error) Code() string { return "E088" }

func lineError(line int, format string, args ...any) *Error {
	return &Error{Line: line, Msg: fmt.Errorf(format, args...).Error()}
}

func Parse(src string) (*Manifest, error) {
	m := &Manifest{Lib: DefaultLib, Deps: map[string]Dep{}}
	section := ""
	seenSections := map[string]bool{}
	seenKeys := map[[2]string]bool{}
	pkgLine := 1
	for ln, raw := range strings.Split(src, "\n") {
		line := Line(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, lineError(ln+1, "malformed section header %q", line)
			}
			section = strings.TrimSpace(line[1 : len(line)-1])
			if section == "package" {
				pkgLine = ln + 1
			}
			switch section {
			case "package", "dependencies", "exclude", "lint", "lint.options":
			case "workspace":
				if m.Members == nil {
					m.Members = []string{}
				}
			default:
				return nil, lineError(ln+1, "unknown section [%s] (supported: [package], [dependencies], [workspace], [exclude], [lint], [lint.options])", section)
			}
			if seenSections[section] {
				return nil, lineError(ln+1, "duplicate section [%s]", section)
			}
			seenSections[section] = true
			continue
		}
		key, val, ok := splitKeyValue(line)
		if !ok {
			return nil, lineError(ln+1, "expected `key = value`, got %q", line)
		}
		if section != "" {
			if seenKeys[[2]string{section, key}] {
				return nil, lineError(ln+1, "duplicate key %q in [%s]", key, section)
			}
			seenKeys[[2]string{section, key}] = true
		}
		switch section {
		case "package":
			s, err := parseString(val)
			if err != nil {
				return nil, lineError(ln+1, "%s: %w", key, err)
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
				return nil, lineError(ln+1, "unknown [package] key %q (supported: name, version, lib, index)", key)
			}
		case "dependencies":
			dep, err := parseDep(val)
			if err != nil {
				return nil, lineError(ln+1, "dependency %q: %w", key, err)
			}
			if !validDepName(key) {
				return nil, lineError(ln+1, "invalid dependency name %q (letters, digits, `_`, `-`; must not start with a digit)", key)
			}
			m.Deps[key] = dep
		case "workspace":
			if key != "members" {
				return nil, lineError(ln+1, "unknown [workspace] key %q (supported: members)", key)
			}
			ms, err := parseStringArray(val)
			if err != nil {
				return nil, lineError(ln+1, "members: %w", err)
			}
			m.Members = ms
		case "lint", "lint.options":
			if _, err := parseScalar(val); err != nil {
				return nil, lineError(ln+1, "[%s] %s: %w", section, key, err)
			}
			if section == "lint.options" && !strings.Contains(key, ".") {
				return nil, lineError(ln+1, "[lint.options] key %q must be spelled <rule>.<option>", key)
			}
		case "exclude":
			if !validDepName(key) {
				return nil, lineError(ln+1, "invalid [exclude] package name %q (letters, digits, `_`, `-`; must not start with a digit)", key)
			}
			vs, err := parseStringArray(val)
			if err != nil {
				return nil, lineError(ln+1, "exclude %s: %w", key, err)
			}
			for _, v := range vs {
				if !IsVersion(v) {
					return nil, lineError(ln+1, "exclude %s: version must be MAJOR.MINOR.PATCH digits, got %q", key, v)
				}
			}
			if m.Excludes == nil {
				m.Excludes = map[string][]string{}
			}
			m.Excludes[key] = vs
		default:
			return nil, lineError(ln+1, "%q outside a section (start with [package], [dependencies], or [workspace])", key)
		}
	}
	if m.Name == "" && !m.IsWorkspace() {
		return nil, &Error{Line: pkgLine, Msg: "missing [package] name"}
	}
	return m, nil
}

func Line(raw string) string {
	quoted := false
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '"':
			quoted = !quoted
		case '#':
			if !quoted {
				return strings.TrimSpace(raw[:i])
			}
		}
	}
	return strings.TrimSpace(raw)
}

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

func parseScalar(val string) (string, error) {
	if strings.HasPrefix(val, "\"") {
		return parseString(val)
	}
	if val == "" {
		return "", fmt.Errorf("expected a quoted string or a number, got an empty value")
	}
	for i := 0; i < len(val); i++ {
		if val[i] == '-' && i == 0 && len(val) > 1 {
			continue
		}
		if val[i] < '0' || val[i] > '9' {
			return "", fmt.Errorf("expected a quoted string or a number, got %q", val)
		}
	}
	return val, nil
}

func parseDep(val string) (Dep, error) {
	if !strings.HasPrefix(val, "{") {
		s, err := parseString(val)
		if err != nil {
			return Dep{}, fmt.Errorf("a bare dependency value must be a version string like \"1.2.0\" (or use an inline table): %w", err)
		}
		if !IsVersion(s) {
			return Dep{}, fmt.Errorf("version must be MAJOR.MINOR.PATCH digits, got %q", s)
		}
		return Dep{Version: s}, nil
	}
	if !strings.HasSuffix(val, "}") {
		return Dep{}, fmt.Errorf("malformed inline table %q", val)
	}
	body := strings.TrimSpace(val[1 : len(val)-1])
	dep := Dep{}
	for _, part := range splitTopLevel(body) {
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
		if k == "capabilities" {
			cs, err := ParseCapabilities(v)
			if err != nil {
				return Dep{}, fmt.Errorf("capabilities: %w", err)
			}
			dep.Capabilities = cs
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
			if err := pkgcache.ValidateHash(s); err != nil {
				return Dep{}, err
			}
			dep.Hash = s
		case "version":
			if !IsVersion(s) {
				return Dep{}, fmt.Errorf("version must be MAJOR.MINOR.PATCH digits, got %q", s)
			}
			dep.Version = s
		default:
			return Dep{}, fmt.Errorf("unknown dependency key %q (supported: path, url, hash, workspace, version, capabilities)", k)
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

func splitTopLevel(body string) []string {
	var out []string
	depth, start := 0, 0
	quoted := false
	for i, r := range body {
		switch {
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '[':
			depth++
		case r == ']':
			depth--
		case r == ',':
			if depth == 0 {
				out = append(out, body[start:i])
				start = i + 1
			}
		}
	}
	return append(out, body[start:])
}

func ParseCapabilities(val string) ([]string, error) {
	names, err := parseStringArray(val)
	if err != nil {
		return nil, err
	}
	valid := map[string]bool{}
	for _, c := range caps.Capabilities {
		valid[c] = true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, n := range names {
		if !valid[n] {
			return nil, fmt.Errorf("unknown capability %q (valid capabilities: %s)", n, strings.Join(caps.Capabilities, ", "))
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

func IsVersion(s string) bool {
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
