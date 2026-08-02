package e2eselfhost

import (
	"bytes"
	"errors"
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
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern",
		"flatten.fern", "modloader.fern", "fern_toml.fern", "wasm_objfile.fern",
		"wasm_modload_run.fern",
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

// TestSelfHostWasmLinkFromCache closes the loop #5331 opened: a module assembled
// from CACHED units, after only one of them was re-emitted, must still run and
// give the right answer.
//
// The incremental test above proves which modules get re-emitted. That is not the
// same as proving the result is usable — a cache that reuses stale or truncated
// units would pass it while producing a module that traps, or refuses to parse.
// Only linking the cached objects and RUNNING the result distinguishes those.
//
// The sequence deliberately mirrors a real edit-rebuild cycle: build cold, edit
// one module's body, rebuild, and require BOTH that the untouched module was
// reused AND that the linked output still computes the new answer.
func TestSelfHostWasmLinkFromCache(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm link-from-cache e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm link-from-cache e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern",
		"flatten.fern", "modloader.fern", "fern_toml.fern", "wasm_objfile.fern",
		"wasm_modload_run.fern",
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

	proj := t.TempDir()
	cacheDir := filepath.Join(proj, "cache")
	if err := os.Mkdir(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, "entry.fern"), []byte(`import "./leaf";
function main(): i32 { return leaf.value() + 5; }`), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	writeLeaf := func(t *testing.T, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(proj, "leaf.fern"), []byte(body), 0o644); err != nil {
			t.Fatalf("write leaf: %v", err)
		}
	}
	writeLeaf(t, `pub function value(): i32 { return 10; }`)
	entryPath := filepath.Join(proj, "entry.fern")

	drive := func(t *testing.T, args ...string) (string, string) {
		t.Helper()
		var cmd *exec.Cmd
		full := append([]string{entryPath}, args...)
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("driver %v failed: %v\nstderr: %s", args, err, stderr.String())
		}
		return stdout.String(), stderr.String()
	}

	// rebuild is the real orchestration loop: emit EVERY module (a cache hit for
	// the unchanged, a rebuild for the changed), then hit-only -link. It returns
	// the linked module's exit code plus the concatenated emit+link stderr so a
	// test can see which modules were reused. This is exactly what a build tool
	// would do — one emit process per module (keeping each lowering's arena peak
	// isolated), then a link that lowers nothing.
	countOut, _ := drive(t, "-per-module-count")
	nmod, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || nmod < 2 {
		t.Fatalf("module count = %q", countOut)
	}
	rebuild := func(t *testing.T, tag string) (int, string) {
		t.Helper()
		var reuse strings.Builder
		for i := 0; i < nmod; i++ {
			_, se := drive(t, "-per-module-emit", strconv.Itoa(i), "-cache-dir", cacheDir)
			reuse.WriteString(se)
		}
		wat, se := drive(t, "-link", "-cache-dir", cacheDir)
		reuse.WriteString(se)
		watPath := filepath.Join(dir, tag+".wat")
		if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		corePath := filepath.Join(dir, tag+".wasm")
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
			t.Fatalf("%s: wasm-tools parse: %v\n%s", tag, err, out)
		}
		out, runErr := exec.Command(wasmtime, "run", corePath).CombinedOutput()
		code := 0
		if runErr != nil {
			var ee *exec.ExitError
			if !errors.As(runErr, &ee) {
				t.Fatalf("%s: wasmtime run: %v\n%s", tag, runErr, out)
			}
			code = ee.ExitCode()
		}
		return code, reuse.String()
	}

	// Cold: every module emitted, link lowers nothing, the result runs.
	code, reuse := rebuild(t, "cold")
	if code != 15 {
		t.Fatalf("cold build returned %d, want 15 (10 + 5)", code)
	}
	if !strings.Contains(reuse, "cache-miss") {
		t.Fatalf("cold build engaged no cache: %s", reuse)
	}

	// Warm: nothing edited. Every emit is a hit, so nothing re-lowers, and the
	// module still runs — which proves the cached units round-tripped through the
	// object format intact, not merely that files were found on disk.
	code, reuse = rebuild(t, "warm")
	if code != 15 {
		t.Errorf("module rebuilt entirely from cache returned %d, want 15 — cached units did not round-trip correctly", code)
	}
	if strings.Contains(reuse, "cache-miss") {
		t.Errorf("warm rebuild re-emitted something with nothing edited: %s", reuse)
	}

	// Incremental: leaf's body changes. Only leaf re-emits, entry is REUSED, and
	// the linked result reflects the new value. A stale-unit bug shows up here as
	// the OLD answer, which no hit/miss assertion could catch.
	writeLeaf(t, `pub function value(): i32 { return 30; }`)
	code, reuse = rebuild(t, "incremental")
	if code != 35 {
		t.Errorf("after editing leaf, rebuilt module returned %d, want 35 (30 + 5) — a stale unit was served", code)
	}
	if !strings.Contains(reuse, "cache-hit") {
		t.Errorf("a body-only edit to leaf re-emitted everything; entry should have been reused: %s", reuse)
	}
	if !strings.Contains(reuse, "cache-miss leaf") {
		t.Errorf("leaf was not re-emitted after its body changed: %s", reuse)
	}
}

// TestSelfHostWasmLinkHitOnly pins that -link NEVER lowers: a module with no
// cached object is an error naming it, not an in-process build.
//
// This is not a niceties test. At whole-compiler scale a build-on-miss link
// lowers every uncached module in one process and exhausts the bump arena
// (exit 137, which masquerades as a SIGKILL). Erroring instead keeps that
// failure unreachable by construction and gives the orchestrator an actionable
// message rather than an opaque OOM.
func TestSelfHostWasmLinkHitOnly(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern",
		"flatten.fern", "modloader.fern", "fern_toml.fern", "wasm_objfile.fern",
		"wasm_modload_run.fern",
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

	proj := t.TempDir()
	cacheDir := filepath.Join(proj, "cache")
	if err := os.Mkdir(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, "entry.fern"), []byte(`import "./leaf";
function main(): i32 { return leaf.value() + 1; }`), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, "leaf.fern"), []byte(`pub function value(): i32 { return 9; }`), 0o644); err != nil {
		t.Fatalf("write leaf: %v", err)
	}
	entryPath := filepath.Join(proj, "entry.fern")

	drive := func(args ...string) (string, string, error) {
		var cmd *exec.Cmd
		full := append([]string{entryPath}, args...)
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		var so, se strings.Builder
		cmd.Stdout = &so
		cmd.Stderr = &se
		err := cmd.Run()
		return so.String(), se.String(), err
	}

	// Cold cache: -link must FAIL (non-zero) and name the missing modules,
	// having emitted no module text at all.
	out, stderr, err := drive("-link", "-cache-dir", cacheDir)
	if err == nil {
		t.Fatalf("-link succeeded on a cold cache; it should have errored on the missing units\nstdout len=%d", len(out))
	}
	if len(out) != 0 {
		t.Errorf("-link wrote %d bytes of module text despite a cold cache; it must emit nothing when a unit is missing", len(out))
	}
	if !strings.Contains(stderr, "no cached object") {
		t.Errorf("-link error does not name the missing units; got: %q", stderr)
	}
	// The cache dir must be untouched: a hit-only link writes no objects.
	if ents, _ := os.ReadDir(cacheDir); len(ents) != 0 {
		t.Errorf("-link populated the cache with %d objects; it must never build", len(ents))
	}

	// Emit each module (its own process), then -link succeeds and lowers nothing.
	countOut, _, _ := drive("-per-module-count")
	n, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || n < 2 {
		t.Fatalf("module count = %q", countOut)
	}
	for i := 0; i < n; i++ {
		if _, se, err := drive("-per-module-emit", strconv.Itoa(i), "-cache-dir", cacheDir); err != nil {
			t.Fatalf("emit %d failed: %v\n%s", i, err, se)
		}
	}
	out, stderr, err = drive("-link", "-cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("-link failed after emitting every module: %v\n%s", err, stderr)
	}
	if strings.Contains(stderr, "cache-miss") {
		t.Errorf("-link built a module despite a warm cache: %q", stderr)
	}
	if !strings.Contains(out, "(module") {
		t.Errorf("-link produced no module text after a warm build")
	}
}
