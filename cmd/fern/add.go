package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jakechampion/lang/internal/pkg/manifest"
	"github.com/jakechampion/lang/internal/pkg/pkgcache"
)

// runAdd implements `fern -add NAME SPEC [DIR]`: append a declared
// dependency to the nearest fern.toml. SPEC selects the source:
//
//	path:../helper                          → { path = "../helper" }
//	url:https://example.com/pkg.tar.gz      → fetch, compute the sha256,
//	                                          { url = "…", hash = "sha256:…" }
//	workspace                               → { workspace = true }
//	version:1.2.0                           → "1.2.0"
//
// grant, from -grant, adds a `capabilities` list: "" writes none, "none" an
// empty one.
//
// The url form is the ergonomic benefit: the user never hand-computes a
// hash — `add` downloads the archive, records the hash it observed (the
// Zig "write url, tool tells you the hash" flow), and leaves it verified
// in the content-addressed store. The manifest is edited textually so
// existing formatting and comments survive, and the result is re-parsed
// before it is written so a bad edit can never leave a broken manifest.
func runAdd(name, spec, grant, dir string) error {
	if !validDepNameCLI(name) {
		return fmt.Errorf("fern-oracle: -add: invalid dependency name %q (letters, digits, `_`, `-`; must not start with a digit)", name)
	}
	man, err := manifest.FindForDir(dir)
	if err != nil {
		return err
	}
	if man == nil {
		return fmt.Errorf("fern-oracle: -add: no %s governs %s — create one with a [package] table first", manifest.FileName, dir)
	}
	if _, exists := man.Deps[name]; exists {
		return fmt.Errorf("fern-oracle: -add: %q is already a dependency in %s (edit it by hand to change its source)", name, filepath.Join(man.Dir, manifest.FileName))
	}

	line, err := depLineFor(name, spec, grant)
	if err != nil {
		return err
	}
	manPath := filepath.Join(man.Dir, manifest.FileName)
	orig, err := os.ReadFile(manPath)
	if err != nil {
		return err
	}
	updated := insertDependency(string(orig), line)
	// Re-parse before committing: never leave a broken manifest on disk.
	if _, perr := manifest.Parse(updated); perr != nil {
		return fmt.Errorf("fern-oracle: -add: the edit would produce an invalid manifest (%w) — no change written", perr)
	}
	if err := os.WriteFile(manPath, []byte(updated), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "fern-oracle: added %s\n", strings.TrimSpace(line))
	return nil
}

// depLineFor builds the manifest line declaring name from SPEC and grant,
// fetching + hashing a url source along the way. A version with no grant
// takes the bare form, `name = "1.2.0"`.
func depLineFor(name, spec, grant string) (string, error) {
	fields, err := depSource(spec)
	if err != nil {
		return "", err
	}
	if grant == "" {
		if v, ok := strings.CutPrefix(spec, "version:"); ok {
			return fmt.Sprintf("%s = %q\n", name, v), nil
		}
		return fmt.Sprintf("%s = { %s }\n", name, fields), nil
	}
	granted, err := grantList(grant)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s = { %s, capabilities = %s }\n", name, fields, granted), nil
}

// depSource is the inline-table fields naming SPEC's source.
func depSource(spec string) (string, error) {
	switch {
	case spec == "workspace":
		return "workspace = true", nil
	case strings.HasPrefix(spec, "path:"):
		p := strings.TrimPrefix(spec, "path:")
		if p == "" {
			return "", fmt.Errorf("fern-oracle: -add: empty path in %q", spec)
		}
		return fmt.Sprintf("path = %q", filepath.ToSlash(p)), nil
	case strings.HasPrefix(spec, "version:"):
		v := strings.TrimPrefix(spec, "version:")
		if !manifest.IsVersion(v) {
			return "", fmt.Errorf("fern-oracle: -add: version must be MAJOR.MINOR.PATCH digits, got %q", v)
		}
		return fmt.Sprintf("version = %q", v), nil
	case strings.HasPrefix(spec, "url:"):
		u := strings.TrimPrefix(spec, "url:")
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return "", fmt.Errorf("fern-oracle: -add: url must be http(s), got %q", u)
		}
		fmt.Fprintf(os.Stderr, "fetching %s to compute its hash…\n", u)
		hash, _, err := pkgcache.FetchUnverified(u)
		if err != nil {
			return "", fmt.Errorf("fern-oracle: -add: %w", err)
		}
		return fmt.Sprintf("url = %q, hash = %q", u, hash), nil
	default:
		return "", fmt.Errorf("fern-oracle: -add: unrecognised source %q — use path:DIR, url:HTTPS_URL, workspace or version:X.Y.Z", spec)
	}
}

// grantList is -grant's comma-separated capabilities as a manifest array, in
// the manifest's order, "[]" for none.
func grantList(grant string) (string, error) {
	if grant == "none" {
		return "[]", nil
	}
	quoted := []string{}
	for _, n := range strings.Split(grant, ",") {
		quoted = append(quoted, fmt.Sprintf("%q", n))
	}
	names, err := manifest.ParseCapabilities("[" + strings.Join(quoted, ", ") + "]")
	if err != nil {
		return "", fmt.Errorf("fern-oracle: -add: -grant: %w", err)
	}
	quoted = quoted[:0]
	for _, n := range names {
		quoted = append(quoted, fmt.Sprintf("%q", n))
	}
	return "[" + strings.Join(quoted, ", ") + "]", nil
}

// insertDependency splices a dependency line into a manifest's
// [dependencies] table, creating the table at EOF when absent. Existing
// content (formatting, comments, other sections) is preserved verbatim.
func insertDependency(src, line string) string {
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		if strings.TrimSpace(manifest.Line(l)) == "[dependencies]" {
			out := append([]string{}, lines[:i+1]...)
			out = append(out, strings.TrimRight(line, "\n"))
			out = append(out, lines[i+1:]...)
			return strings.Join(out, "\n")
		}
	}
	// No [dependencies] table yet — append one.
	sep := "\n"
	if strings.HasSuffix(src, "\n") {
		sep = ""
	}
	return src + sep + "\n[dependencies]\n" + line
}

func validDepNameCLI(s string) bool {
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
