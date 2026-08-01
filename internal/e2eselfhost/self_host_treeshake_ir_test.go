package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The self-host treeshake pass (examples/self_host/treeshake.fern) + stdlib
// loading. The self-host loader can resolve `core/…` / `std/…` imports under a
// stdlib root, but a stdlib-importing program drags in the whole transitive
// closure, blowing asm_ir's 512-function IR budget so the program is forced
// onto the legacy AST emitter. `-treeshake` prunes the merged module to the
// functions reachable from main, so the program fits the IR path. These tests
// drive the self-hosted x86-64 driver (asm_load_run) with the repo's real
// stdlib as the root and assert: (a) a stdlib-heavy program flips ast→ir under
// -treeshake, (b) the emitted IR runs correctly (oracle-checked against the
// native interpreter), and (c) treeshake never changes behaviour (the AST and
// IR builds agree).

// copySelfHostTree copies every examples/self_host/*.fern into a fresh temp dir
// so the driver (and the asm buildBin writes) stay out of the repo tree.
func copySelfHostTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir("../../examples/self_host")
	if err != nil {
		t.Fatalf("readdir self_host: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".fern") {
			continue
		}
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}
	return dir
}

// derive-heavy program: 4 derives (incl. string fields → pulls core/sort) +
// JSON, plus three independent stdlib modules (std/http, std/regex, std/time)
// that are NOT in the cmp/json transitive closure. Without treeshake the merged
// module pulls all of them (~580 funcs) and exceeds the 512 IR budget (→ ast);
// with treeshake only the reachable slice (~90) survives, so it fits (→ ir).
// (The http/regex/time imports are load-bearing: cmp+json alone already lower
// to ~480 funcs — under budget — so json's Map.iter flipping to IR removed the
// old over-budget margin; the extra modules restore a genuine >512 closure.)
// Returns 7 (the count of passing checks), a stable oracle independent of hash
// internals.
const treeshakeHeavyProg = `import "core/cmp";
import "std/json";
import "std/http" as http;
import "std/regex" as regex;
import "std/time" as time;
@derive(cmp.Eq, cmp.Ord, cmp.Hash, json.Json)
struct Rec { name: string, id: i32, tag: string }
function main(): i32 {
    var a = Rec { name: "x", id: 1, tag: "p" };
    var b = Rec { name: "y", id: 2, tag: "q" };
    var n = 0;
    if (a.eq(a)) { n = n + 1; }
    if (a.cmp(b) < 0) { n = n + 1; }
    if (a.hash() != b.hash()) { n = n + 1; }
    if (a.to_json().len() > 0) { n = n + 1; }
    if (http.http_status_text(200).len() > 0) { n = n + 1; }
    if (regex.regex_match("a", "a")) { n = n + 1; }
    var d = time.date_make(2026, 6, 28);
    if (d.year == 2026) { n = n + 1; }
    return n;
}`

// a lighter derive(Eq) program: returns 42 iff eq is correct (no hash-value
// dependence), independent of treeshake/AST internals.
const treeshakeLightProg = `import "core/cmp";
@derive(cmp.Eq)
struct P { x: i32, y: i32 }
function main(): i32 { var a = P { x: 1, y: 2 }; var b = P { x: 1, y: 2 }; var c = P { x: 1, y: 9 }; if (a.eq(b) && !a.eq(c)) { return 42; } return 0; }`

func TestSelfHostTreeshakeStdlibIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// runDriver invokes the self-host driver with the given args, returning
	// trimmed stdout (decide modes) or raw stdout (emit) and the exit code.
	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	runProg := func(name, src string, want int) {
		entry := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(entry, []byte(src+"\n"), 0o644); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		// Oracle: the native interpreter's result.
		if _, code := runFixtureInterp(t, entry, ""); code != want {
			t.Fatalf("%s native interp = %d, want %d", name, code, want)
		}
		// With -treeshake the merged (stdlib-loaded) module must route IR.
		if out, _ := runDriver(entry, root, "-treeshake", "-decide"); strings.TrimSpace(out) != "ir" {
			t.Errorf("%s -treeshake decide = %q, want \"ir\"", name, strings.TrimSpace(out))
		}
		// Emit the treeshaked IR, assemble, run — must match the oracle.
		asm, _ := runDriver(entry, root, "-treeshake")
		if len(asm) == 0 {
			t.Fatalf("%s: driver emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name+"_bin", asm)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s -treeshake run = %d, want %d", name, code, want)
		}
	}

	t.Run("light-derive-eq", func(t *testing.T) { runProg("ts_light", treeshakeLightProg, 42) })

	t.Run("heavy-flips-ast-to-ir", func(t *testing.T) {
		entry := filepath.Join(dir, "ts_heavy.fern")
		if err := os.WriteFile(entry, []byte(treeshakeHeavyProg+"\n"), 0o644); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		// Oracle.
		if _, code := runFixtureInterp(t, entry, ""); code != 7 {
			t.Fatalf("heavy native interp = %d, want 7", code)
		}
		// The keystone: WITHOUT the prune (explicit -no-treeshake) the loaded
		// module is over budget → ast; with the prune (the default once a stdlib
		// root is given) it fits → ir.
		if out, _ := runDriver(entry, root, "-no-treeshake", "-decide"); strings.TrimSpace(out) != "ast" {
			t.Errorf("heavy decide (no treeshake) = %q, want \"ast\"", strings.TrimSpace(out))
		}
		if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
			t.Errorf("heavy decide (default treeshake) = %q, want \"ir\"", strings.TrimSpace(out))
		}
		// The PRUNED build compiles, links and runs correctly.
		asm, _ := runDriver(entry, root)
		if len(asm) == 0 {
			t.Fatal("heavy (pruned): 0 bytes")
		}
		bin := buildBin(t, gcc, dir, "ts_heavy_ir", asm)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 7 {
			t.Errorf("heavy (pruned) run = %d, want 7", code)
		}
		// And the UNPRUNED one is REFUSED. This used to build too — the AST
		// emitter took it, and the pair asserted "treeshake changes the path, not
		// behaviour". #3457 slice 5 deleted that emitter, so the over-budget route
		// the -decide above still reports has nowhere to go: emitting 0 bytes IS
		// the contract now, and it is what makes the prune load-bearing rather
		// than merely preferable.
		if noPrune, _ := runDriver(entry, root, "-no-treeshake"); len(noPrune) != 0 {
			t.Errorf("heavy (unpruned) emitted %d bytes, want a refusal (over budget, no AST emitter)", len(noPrune))
		}
	})

	// Regression: a function referenced ONLY from a match-arm `when` guard must
	// survive treeshake. ts_names_stmt's StmtMatch arm walked the scrutinee and
	// arm bodies but NOT the guard, so guard_only was pruned and the emitted
	// guard called a stripped symbol → segfault (exit 139). Routing-independent
	// (a treeshake reachability bug), so this asserts the run result, not ir/ast.
	t.Run("guard-reachability", func(t *testing.T) {
		const guardProg = `function guard_only(n: i32): boolean { return n > 5; }
enum E { N(i32), Z }
function main(): i32 {
    var e: E = E.N(7);
    match (e) { E.N(v) when guard_only(v) => { return 42; }, _ => { return 0; } }
}`
		entry := filepath.Join(dir, "ts_guard.fern")
		if err := os.WriteFile(entry, []byte(guardProg+"\n"), 0o644); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		if _, code := runFixtureInterp(t, entry, ""); code != 42 {
			t.Fatalf("guard native interp = %d, want 42", code)
		}
		asm, _ := runDriver(entry, root, "-treeshake")
		if len(asm) == 0 {
			t.Fatalf("guard: driver emitted 0 bytes")
		}
		bin := buildBin(t, gcc, dir, "ts_guard_bin", asm)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("guard -treeshake run = %d, want 42 (guard-only fn pruned by treeshake?)", code)
		}
	})
}
