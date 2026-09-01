package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fnLabels returns the emitted function labels in a GAS text, in order.
//
// Both compilers label a function at column 0 as `__fn_<name>:` or
// `__method_<name>:`, so this is a count of what the artifact actually carries
// rather than of what was loaded.
func fnLabels(asm string) []string {
	re := regexp.MustCompile(`(?m)^(__fn_[A-Za-z_0-9]+|__method_[A-Za-z_0-9]+):`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(asm, -1) {
		out = append(out, m[1])
	}
	return out
}

// TestSelfHostCLIPrunesStdlibImportClosureX86_64 pins that `fern.fern` — the
// self-hosted CLI, the binary the whole toolchain ships — prunes the functions
// a program cannot reach before it emits.
//
// It did not. `treeshake.treeshake` was called in exactly one place there, on
// the diagnostics side of `capability_violations`, and its result was thrown
// away; the module handed to codegen was the whole merged import closure. One
// `import "std/string"` reaches core/int, core/bigint, std/array and
// std/unicode transitively, so examples/bench/string_count_byte emitted 958
// functions and 81,463 instructions against the native compiler's 27 and 640 —
// 127x, on a benchmark both compilers had a checked-in size baseline for.
//
// Neither baseline could see it. `.github/perf-baseline-selfhost.txt` compares
// the self-host against ITSELF across the three targets, and
// `.github/perf-baseline.txt` covers the native compiler; the two count the
// same thing the same way (`grep -c '^[[:space:]]'` over the same `.s`) on the
// same benchmark names and are never read against each other. This test is
// that missing comparison, as a gate rather than an advisory lane.
//
// Two assertions, because the count alone is a weak claim. NAMED dead
// functions must be absent — the shape internal/e2e/treeshake_backend_dce_test
// uses, and the one that says what was pruned rather than how much. And the
// total must stay under a ceiling, which is what catches a whole closure
// surviving again. The ceiling is deliberately loose: the self-host shake
// over-approximates reachability BY NAME, so `==` in any reachable line keeps
// every `*.eq` in the program and `<` keeps every `*.cmp` — 69 functions
// against native's 1 on the case below. A tight ratchet would fail on an
// unrelated stdlib edit; the defect this exists to catch is two orders of
// magnitude, not a few percent.
//
// Native only: the CLI takes host filesystem paths as argv, so a qemu runner
// would not resolve them (mirrors TestSelfHostStdTestE2E).
func TestSelfHostCLIPrunesStdlibImportClosureX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading CLI test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	const src = `import "std/string";
function main(): i32 {
    var s: string = "hello world";
    if (s.starts_with("hello")) { return 0; }
    return 1;
}
`
	prog := filepath.Join(dir, "ts_stdlib_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	asmPath := filepath.Join(dir, "ts_stdlib_prog.s")
	if out, err := exec.Command(driverBin, "-target", "x86-64-linux", "-emit", "asm", prog, stdlib, "-o", asmPath).CombinedOutput(); err != nil {
		t.Fatalf("self-host compile failed: %v\n%s", err, out)
	}
	asmBytes, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	asm := string(asmBytes)
	labels := fnLabels(asm)

	// std/string functions this program cannot reach. `starts_with` is the one
	// it calls; `eq` and `cmp` survive the operator over-approximation. These
	// six are reached by neither route, so each one present is a prune that did
	// not happen.
	for _, dead := range []string{
		"__fn_string__repeat",
		"__fn_string__contains",
		"__fn_string__index_of",
		"__fn_string__last_index_of",
		"__fn_string__ends_with",
		"__fn_string__split",
	} {
		if strings.Contains(asm, "\n"+dead+":") {
			t.Errorf("%s is emitted, but nothing in the program can reach it — the import closure was not pruned", dead)
		}
	}
	if !strings.Contains(asm, "\n__fn_string__starts_with:") {
		t.Errorf("__fn_string__starts_with is NOT emitted, but main calls it — the prune dropped a reachable function")
	}

	// The cliff detector. 69 with the prune, 958 without it.
	const ceiling = 300
	if len(labels) > ceiling {
		t.Errorf("self-host emitted %d functions for a program using one std/string method, ceiling %d — "+
			"a count this high means the transitive import closure survived to codegen", len(labels), ceiling)
	}

	// The prune must not have changed the answer.
	bin := buildBin(t, gcc, dir, "ts_stdlib_prog", asm)
	run := exec.Command(bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("pruned program exited %d, want 0", code)
	}
}

// TestSelfHostTreeshakeKeepsDropFinalizerX86_64 pins the one root the walk
// cannot discover for itself.
//
// A `Drop` finalizer has no call site anywhere in the program: its only caller
// is the `__drop_struct_<C>` glue lowering synthesises AFTER the prune runs. So
// a reachability walk seeded from `main` drops it, and the glue is then left
// naming a symbol nothing emitted. treeshake roots every `Drop` impl's methods
// whole-program for the same reason native's `treeshake.DropImplMethods` does —
// gating it on whether a live function can hold a `C` is type reachability this
// pass does not compute.
func TestSelfHostTreeshakeKeepsDropFinalizerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading CLI test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	// `drop` is never spelled in this program — not as a call, not as a field
	// access — so nothing but the impl root can keep it.
	const src = `import "core/mem";
import "std/i32";
struct W { n: i32 }
impl mem.Drop for W {
    function drop(self: Self): void { print("drop " + self.n.to_string()); }
}
function main(): i32 {
    var a: W = W { n: 7 };
    return a.n - 7;
}
`
	prog := filepath.Join(dir, "ts_drop_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	asmPath := filepath.Join(dir, "ts_drop_prog.s")
	if out, err := exec.Command(driverBin, "-target", "x86-64-linux", "-emit", "asm", prog, stdlib, "-o", asmPath).CombinedOutput(); err != nil {
		t.Fatalf("self-host compile failed: %v\n%s", err, out)
	}
	asmBytes, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	if !strings.Contains(string(asmBytes), "\n__fn_W__drop:") {
		t.Errorf("__fn_W__drop is not emitted: the prune dropped the Drop finalizer, which the drop glue names")
	}
}

// TestSelfHostTreeshakeKeepsDerivedMethodsX86_64 pins the second root of that
// kind, and the one that actually broke.
//
// A `@derive`d method has no syntactic call site either: the map lowering emits
// the call to a key type's `hash` after the prune has run. So
// `@derive(cmp.Eq, cmp.Hash) struct Sku` used as a map key lost `Sku.hash`, and
// the wasm leg rejected the module for calling a function it never defines —
// conformance/cases/map_iter_struct_value, map_struct_enum_keys and
// map_struct_key_grow, which are the corpus gate on this and are why the roots
// are not a matter of reasoning about which ones might be needed.
//
// The root is by RECEIVER, not by a list of derivable method names, so a new
// derive is covered without a second place to remember.
func TestSelfHostTreeshakeKeepsDerivedMethodsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading CLI test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	// `hash` is never spelled here; only the map lowering asks for it.
	const src = `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Sku { code: i32 }

function main(): i32 {
    var m: Map[Sku, i32] = map_new(8);
    m = m.insert(Sku { code: 5 }, 7);
    return m.get_or(Sku { code: 5 }, 0) - 7;
}
`
	prog := filepath.Join(dir, "ts_derive_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	asmPath := filepath.Join(dir, "ts_derive_prog.s")
	if out, err := exec.Command(driverBin, "-target", "x86-64-linux", "-emit", "asm", prog, stdlib, "-o", asmPath).CombinedOutput(); err != nil {
		t.Fatalf("self-host compile failed: %v\n%s", err, out)
	}
	asmBytes, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	asm := string(asmBytes)
	if !strings.Contains(asm, "\n__fn_Sku__hash:") {
		t.Errorf("__fn_Sku__hash is not emitted: the prune dropped a @derive'd method the map lowering calls")
	}

	// Every function the artifact calls must be one it defines. This is the
	// property the wasm leg enforces by rejecting the module, stated directly
	// on the x86-64 text so a failure names the symbol.
	callRe := regexp.MustCompile(`(?m)^[ \t]+call[ \t]+(__fn_[A-Za-z_0-9]+|__method_[A-Za-z_0-9]+)\b`)
	defined := map[string]bool{}
	for _, l := range fnLabels(asm) {
		defined[l] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, m := range callRe.FindAllStringSubmatch(asm, -1) {
		if !defined[m[1]] && !seen[m[1]] {
			seen[m[1]] = true
			missing = append(missing, m[1])
		}
	}
	if len(missing) != 0 {
		t.Errorf("the artifact calls %d function(s) it does not define: %s — the prune dropped something reachable",
			len(missing), strings.Join(missing, ", "))
	}
}
