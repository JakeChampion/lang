package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostWasmIncrementalCache is the test #5331 exists for.
//
// Everything before it — #5499, #5500, #5501, #5502, #5503 — was enabling work,
// verified by byte-identity (nothing broke) or by the N=2 probe (two units link
// and run). None of it demonstrated the actual claim: that after editing ONE
// module, the others are not re-emitted.
//
// wasm_modload_run keys each module's object-cache entry on its own source hash
// folded with the SIGNATURE hashes of the modules it imports
// (modloader.module_cache_key). The asymmetry that buys is what the four cases
// below pin, and it is the whole design:
//
//   - a body-only edit re-emits that module ALONE — its importers' call-site
//     tagging cannot have changed, so their keys are untouched
//   - a signature edit re-emits that module AND its importers — their tagging
//     DOES depend on it, so reusing their old units would ship stale codegen
//
// The negative case matters as much as the positive one. A cache that always
// hits would pass "body edit reuses the importer" while being catastrophically
// wrong; a cache that never hits would pass "signature edit invalidates" while
// being useless. Only asserting both directions distinguishes a correct cache
// from either failure.
func TestSelfHostWasmIncrementalCache(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern",
		"flatten.fern", "modloader.fern", "fern_toml.fern", "wasm_modload_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_modload_run.fern", "wasm_modload_run")

	// A two-module program: entry calls into leaf.
	proj := t.TempDir()
	cacheDir := filepath.Join(proj, "cache")
	if err := os.Mkdir(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	writeLeaf := func(t *testing.T, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(proj, "leaf.fern"), []byte(body), 0o644); err != nil {
			t.Fatalf("write leaf.fern: %v", err)
		}
	}
	entry := `import "./leaf";
function main(): i32 { return leaf.value() + 1; }`
	if err := os.WriteFile(filepath.Join(proj, "entry.fern"), []byte(entry), 0o644); err != nil {
		t.Fatalf("write entry.fern: %v", err)
	}
	writeLeaf(t, `pub function value(): i32 { return 10; }`)

	entryPath := filepath.Join(proj, "entry.fern")

	run := func(t *testing.T, args ...string) (string, string) {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("driver %v failed: %v\nstderr: %s", args, err, stderr.String())
		}
		return stdout.String(), stderr.String()
	}

	// How many modules? Guards the rest: if resolution silently found one
	// module, every "the other module was reused" assertion below is vacuous.
	countOut, _ := run(t, entryPath, "-per-module-count")
	if strings.TrimSpace(countOut) != "2" {
		t.Fatalf("module count = %q, want \"2\" — import resolution did not find both modules, so the reuse assertions would be meaningless", strings.TrimSpace(countOut))
	}

	// emitBoth emits both units and reports, per module ns, whether it was a
	// cache hit or a miss.
	emitBoth := func(t *testing.T) map[string]string {
		t.Helper()
		got := map[string]string{}
		for i := 0; i < 2; i++ {
			_, stderr := run(t, entryPath, "-per-module-emit", strconv.Itoa(i), "-cache-dir", cacheDir)
			for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
				f := strings.Fields(line)
				if len(f) == 2 && (f[0] == "cache-hit" || f[0] == "cache-miss") {
					got[f[1]] = f[0]
				}
			}
		}
		return got
	}

	// entryVerdict returns the verdict for the ENTRY module — whichever ns is
	// not "leaf". The bundler mangles the entry's namespace, so looking it up by
	// source filename silently yields "" and every assertion below passes
	// vacuously. Derived rather than hardcoded so a mangling change fails loudly
	// here instead of quietly disarming the test.
	entryVerdict := func(t *testing.T, got map[string]string) string {
		t.Helper()
		for ns, verdict := range got {
			if ns != "leaf" {
				return verdict
			}
		}
		t.Fatalf("no entry-module verdict in %v", got)
		return ""
	}

	t.Run("cold_build_misses_everything", func(t *testing.T) {
		got := emitBoth(t)
		if len(got) != 2 {
			t.Fatalf("expected a hit/miss line for each of 2 modules, got %v", got)
		}
		for ns, verdict := range got {
			if verdict != "cache-miss" {
				t.Errorf("module %q reported %s on a cold cache, want cache-miss", ns, verdict)
			}
		}
	})

	t.Run("unchanged_rebuild_reuses_everything", func(t *testing.T) {
		got := emitBoth(t)
		for ns, verdict := range got {
			if verdict != "cache-hit" {
				t.Errorf("module %q reported %s with nothing edited, want cache-hit", ns, verdict)
			}
		}
	})

	// THE CLAIM. leaf's body changes; its signature does not. leaf must be
	// re-emitted and entry must NOT be.
	t.Run("body_only_edit_reemits_only_that_module", func(t *testing.T) {
		writeLeaf(t, `pub function value(): i32 { return 20 + 2 - 2; }`)
		got := emitBoth(t)
		if got["leaf"] != "cache-miss" {
			t.Errorf("leaf reported %s after its body changed, want cache-miss", got["leaf"])
		}
		if v := entryVerdict(t, got); v != "cache-hit" {
			t.Errorf("entry reported %s after a BODY-ONLY edit to leaf, want cache-hit — this is the incremental claim #5331 makes, and it is not holding (full map: %v)", v, got)
		}
	})

	// The other direction: leaf's SIGNATURE changes, so entry's call-site
	// tagging depends on it and entry must be re-emitted too. Without this a
	// cache that never reused anything would pass the case above.
	t.Run("signature_edit_also_invalidates_the_importer", func(t *testing.T) {
		writeLeaf(t, `pub function value(): i64 { return 20i64; }`)
		got := emitBoth(t)
		if got["leaf"] != "cache-miss" {
			t.Errorf("leaf reported %s after its signature changed, want cache-miss", got["leaf"])
		}
		if v := entryVerdict(t, got); v != "cache-miss" {
			t.Errorf("entry reported %s after leaf's RETURN TYPE changed, want cache-miss — reusing entry's unit here would ship stale call-site tagging (full map: %v)", v, got)
		}
	})
}
