package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jakechampion/lang/internal/manifest"
	"github.com/jakechampion/lang/internal/pkgcache"
)

// runAdd implements `fern -add NAME SPEC [-manifest DIR]`: append a
// declared dependency to the nearest fern.toml. SPEC selects the source:
//
//	path:../helper                          → { path = "../helper" }
//	url:https://example.com/pkg.tar.gz      → fetch, compute the sha256,
//	                                          { url = "…", hash = "sha256:…" }
//	workspace                               → { workspace = true }
//
// The url form is the ergonomic payoff: the user never hand-computes a
// hash — `add` downloads the archive, records the hash it observed (the
// Zig "write url, tool tells you the hash" flow), and leaves it verified
// in the content-addressed store. The manifest is edited textually so
// existing formatting and comments survive, and the result is re-parsed
// before it is written so a bad edit can never leave a broken manifest.
func runAdd(name, spec, dir string) error {
	if !validDepNameCLI(name) {
		return fmt.Errorf("add: invalid dependency name %q (letters, digits, `_`, `-`; must not start with a digit)", name)
	}
	man, err := manifest.FindForDir(dir)
	if err != nil {
		return err
	}
	if man == nil {
		return fmt.Errorf("add: no %s governs %s — create one with a [package] table first", manifest.FileName, dir)
	}
	if _, exists := man.Deps[name]; exists {
		return fmt.Errorf("add: %q is already a dependency in %s (edit it by hand to change its source)", name, filepath.Join(man.Dir, manifest.FileName))
	}

	line, err := depLineFor(name, spec)
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
		return fmt.Errorf("add: the edit would produce an invalid manifest (%w) — no change written", perr)
	}
	if err := os.WriteFile(manPath, []byte(updated), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "add: %s\n", strings.TrimSpace(line))
	return nil
}

// depLineFor builds the `NAME = { … }` manifest line for a SPEC,
// fetching + hashing a url source along the way.
func depLineFor(name, spec string) (string, error) {
	switch {
	case spec == "workspace":
		return fmt.Sprintf("%s = { workspace = true }\n", name), nil
	case strings.HasPrefix(spec, "path:"):
		p := strings.TrimPrefix(spec, "path:")
		if p == "" {
			return "", fmt.Errorf("add: empty path in %q", spec)
		}
		return fmt.Sprintf("%s = { path = %q }\n", name, filepath.ToSlash(p)), nil
	case strings.HasPrefix(spec, "url:"):
		u := strings.TrimPrefix(spec, "url:")
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return "", fmt.Errorf("add: url must be http(s), got %q", u)
		}
		fmt.Fprintf(os.Stderr, "fetching %s to compute its hash…\n", u)
		hash, _, err := pkgcache.FetchUnverified(u)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s = { url = %q, hash = %q }\n", name, u, hash), nil
	default:
		return "", fmt.Errorf("add: unrecognised source %q — use path:DIR, url:HTTPS_URL, or workspace", spec)
	}
}

// insertDependency splices a dependency line into a manifest's
// [dependencies] table, creating the table at EOF when absent. Existing
// content (formatting, comments, other sections) is preserved verbatim.
func insertDependency(src, line string) string {
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "[dependencies]" {
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
