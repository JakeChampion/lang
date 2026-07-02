package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostPerModuleSubdirCacheX86_64 guards subdir-aware source hashing in
// the per-module incremental cache (#3451 step 6 / #3458). The bundler keys a
// module by its basename (`import "./sub/leaf"` → ns "leaf"), so an early
// version of module_src_hash read only the flat <dir><ns>.fern and returned "?"
// for any module resolved from a sub-directory — disabling the cache (always
// rebuild) for it. module_src_hash now resolves the source through the loader
// (modloader.resolve_module_src, keyed by the original import path), so a subdir
// module gets a real content hash and participates in the cache.
//
// Tree: main → mid → sub/leaf. leaf lives in a sub-directory; the test asserts
// it gets a non-"?" src_hash, is served from cache on an unchanged rebuild, and
// is correctly re-emitted (and only it) when its body changes.
func TestSelfHostPerModuleSubdirCacheX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "asm_modload_run.fern", "driver")

	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	cacheDir := filepath.Join(proj, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	writeLeaf := func(body string) {
		if err := os.WriteFile(filepath.Join(proj, "sub", "leaf.fern"), []byte(body), 0o644); err != nil {
			t.Fatalf("write sub/leaf.fern: %v", err)
		}
	}
	writeLeaf("pub function leaf_val(): i32 { return 40; }\n")
	if err := os.WriteFile(filepath.Join(proj, "mid.fern"), []byte(
		"import \"./sub/leaf\";\npub function mid_val(): i32 { return leaf.leaf_val() + 2; }\n"), 0o644); err != nil {
		t.Fatalf("write mid.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.fern"), []byte(
		"import \"./mid\";\nfunction main(): i32 { return mid.mid_val(); }\n"), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	entry := filepath.Join(proj, "main.fern")

	drive := func(args ...string) (string, string) {
		t.Helper()
		full := append([]string{entry}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		var errb strings.Builder
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("driver %v: %v\nstderr:\n%s", args, err, errb.String())
		}
		return string(out), errb.String()
	}

	// leafSrcHash returns the src_hash column for the "leaf" module from the manifest.
	leafSrcHash := func() string {
		for _, ln := range strings.Split(mustStdout(drive("-per-module-manifest")), "\n") {
			f := strings.Split(strings.TrimSpace(ln), "|")
			if len(f) == 4 && f[0] == "leaf" {
				return f[1]
			}
		}
		t.Fatal("no leaf line in manifest")
		return ""
	}

	// The subdir module must resolve to a real content hash, not "?".
	h0 := leafSrcHash()
	if h0 == "?" || h0 == "" {
		t.Fatalf("subdir module leaf got src_hash %q, want a real content hash (resolver did not find sub/leaf.fern)", h0)
	}

	var nsOrder []string
	for _, ln := range strings.Split(mustStdout(drive("-per-module-manifest")), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			nsOrder = append(nsOrder, strings.SplitN(ln, "|", 2)[0])
		}
	}
	var needArgs []string
	for _, ln := range strings.Split(mustStdout(drive("-per-module-needs")), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	build := func() ([]string, []string) {
		var hits, misses []string
		for i := range nsOrder {
			_, errs := drive(append([]string{"-per-module-emit", strconv.Itoa(i), "-cache-dir", cacheDir}, needArgs...)...)
			for _, ln := range strings.Split(errs, "\n") {
				ln = strings.TrimSpace(ln)
				if m := strings.TrimPrefix(ln, "cache-hit "); m != ln {
					hits = append(hits, m)
				} else if m := strings.TrimPrefix(ln, "cache-miss "); m != ln {
					misses = append(misses, m)
				}
			}
		}
		sort.Strings(hits)
		sort.Strings(misses)
		return hits, misses
	}
	eq := func(a []string, want ...string) bool {
		b := append([]string(nil), want...)
		sort.Strings(b)
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	// Cold: all miss. Warm: all hit — crucially INCLUDING the subdir module leaf
	// (which the flat-only src_hash could never cache).
	if _, misses := build(); !eq(misses, nsOrder...) {
		t.Fatalf("cold: misses=%v, want all", misses)
	}
	hits, misses := build()
	if len(misses) != 0 || !eq(hits, nsOrder...) {
		t.Fatalf("warm: hits=%v misses=%v, want all-hit (subdir leaf must be cacheable)", hits, misses)
	}

	// Editing the subdir module's body must change its hash and re-emit only it.
	writeLeaf("pub function leaf_val(): i32 { return 41; }\n")
	if leafSrcHash() == h0 {
		t.Fatal("subdir leaf body edit did not change its src_hash")
	}
	hits, misses = build()
	if !eq(misses, "leaf") {
		t.Fatalf("after subdir body edit: misses=%v, want exactly [leaf]", misses)
	}
	if !eq(hits, "mid", "__entry") {
		t.Fatalf("after subdir body edit: hits=%v, want exactly [mid __entry]", hits)
	}
}
