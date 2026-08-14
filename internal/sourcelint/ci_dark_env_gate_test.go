// Package sourcelint holds fast, dependency-free repo-hygiene checks that run
// in the ordinary `go test ./...` lane (no build tools, no fixtures).
package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A test guarded by `if os.Getenv(NAME) == "" { t.Skip(...) }` runs nowhere
// unless something sets NAME. When nothing does, the test is DARK: it is in
// the tree, it looks like coverage, `go test ./...` reports it as a pass, and
// it has never executed.
//
// A sweep of every such gate against .github/, Makefile and scripts/ found
// three set by NOTHING — RUN_EMITALL_FIXPOINT, RUN_EMITALL_CHECK,
// FERN_NATIVE_ASM. Each had a sound reason for being gated, recorded only in a
// prose comment with no mechanism to ever re-check it, and at least one had
// gone stale: FERN_NATIVE_ASM covers the pure-Go in-process assembler, which
// is the DEFAULT assemble+link path for `cmd/fern -target x86-64-linux` and falls
// back to gcc automatically, so a gap in it degrades silently. It had a
// missing `ud2` encoding that made FERN_RC_FREE_DEBUG=1 — the use-after-free
// detector, and the tool that found #6021 — unbuildable on the production
// path.
//
// So: every RUN_* / FERN_* / DIFF_ORACLE_* gate in test-side code must either
// be SET somewhere under .github/ (a real lane runs it) or carry an explicit
//
//	// CI-DARK: NAME — <reason>
//
// marker in its own file. The marker is not a rubber stamp — it is the
// enumerable list of "coverage we are knowingly not getting", which is the
// thing that was impossible to produce before.
var envGateRe = regexp.MustCompile(`os\.Getenv\("((?:RUN_|FERN_|DIFF_ORACLE_)[A-Z0-9_]+)"\)`)

// ciDarkRe matches the acknowledgement marker. The reason text after the name
// is required — a bare marker would just be a way to silence the check.
var ciDarkRe = regexp.MustCompile(`CI-DARK:\s*((?:RUN_|FERN_|DIFF_ORACLE_)[A-Z0-9_]+)\s*[—:-]\s*\S`)

// gateScanRoots are the test-side trees whose env gates decide whether
// coverage happens. Non-test production code (internal/ast's compile-mode
// knobs, internal/pkgcache's cache dir) reads env vars for configuration, not
// for gating tests, and is deliberately out of scope.
var gateScanRoots = []string{
	filepath.Join("internal", "e2e"),
	filepath.Join("internal", "e2eselfhost"),
	filepath.Join("internal", "e2eharness"),
}

func TestNoSilentlyCIDarkEnvGates(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	ciRefs := envNamesReferencedByCI(t, root)

	for _, rel := range gateScanRoots {
		dir := filepath.Join(root, rel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			text := string(src)

			acknowledged := map[string]bool{}
			for _, m := range ciDarkRe.FindAllStringSubmatch(text, -1) {
				acknowledged[m[1]] = true
			}
			for _, m := range envGateRe.FindAllStringSubmatch(text, -1) {
				name := m[1]
				if ciRefs[name] || acknowledged[name] {
					continue
				}
				t.Errorf("%s: %s gates test-side behaviour but nothing under .github/ sets it, "+
					"and the file carries no `// CI-DARK: %s — <reason>` marker. "+
					"Either wire a lane that sets it or record why the coverage is knowingly skipped.",
					filepath.Join(rel, e.Name()), name, name)
			}
		}
	}
}

// envNamesReferencedByCI returns every RUN_* / FERN_* / DIFF_ORACLE_* name
// mentioned anywhere under .github/. Mention rather than assignment on
// purpose: a name can reach a job through a matrix entry, a composite action's
// input, or a shell expansion, and this check only needs to know that CI knows
// about it — over-matching here fails OPEN, which is the right direction for a
// hygiene lint.
func envNamesReferencedByCI(t *testing.T, root string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	nameRe := regexp.MustCompile(`(?:RUN_|FERN_|DIFF_ORACLE_)[A-Z0-9_]+`)
	err := filepath.Walk(filepath.Join(root, ".github"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range nameRe.FindAllString(string(src), -1) {
			found[m] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk .github: %v", err)
	}
	return found
}
