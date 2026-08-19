package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// genericEnumIRCase is a self-host generic-enum (`enum E[T]`) program whose
// exit code is pinned against the native interpreter's oracle value. Each case
// exercises the parser's monomorphize_enums pass (parser.fern): a generic enum
// is cloned per concrete instantiation (`Opt[i32]` → `Opt__i32` with
// `Sm__i32(i32)`), the variant constructions + match arm patterns + annotations
// are mangled to the clone, so the variant payload types are concrete and the
// module lowers through the IR path instead of bailing to the legacy AST
// emitter (issue #3572). Exit codes are kept <= 120 (native) / <= 125 (WASI).
type genericEnumIRCase struct {
	name     string
	src      string
	expected int
}

var genericEnumIRCases = []genericEnumIRCase{
	// i32 payload: construction + match + payload binding through a cloned
	// `Sm__i32(i32)` variant.
	{"i32_payload", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    var o: Opt[i32] = Sm(5);
    match (o) { Sm(n) => { return n; }, Nn => { return 0; } }
}`, 5},
	// string payload: the case the erased shape miscompiled — a method on the
	// bound payload (`s.len()`) needs the concrete `string` type to dispatch.
	{"string_payload_method", `enum Box[T] { V(T) }
function main(): i32 {
    var b: Box[string] = V("hi");
    match (b) { V(s) => { return s.len(); } }
}`, 2},
	// unit variant: `Nn` has no payload, so its instantiation is pinned by the
	// `var o: Opt[i32]` annotation rather than an argument.
	{"unit_variant", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    var o: Opt[i32] = Nn;
    match (o) { Sm(n) => { return n; }, Nn => { return 9; } }
}`, 9},
	// unit variant passed BARE as a call argument (#5247): the callee's declared
	// parameter type `Opt[i32]` — not a var annotation at the use site — must pin
	// the bare `Nn`'s instantiation. Before the fix the argument stayed
	// un-mangled, monomorphize_enums dropped the generic `Nn` struct, and the
	// dangling reference bailed the whole module (and the AST emitter it then fell
	// to miscompiled it into a SIGSEGV).
	{"unit_variant_call_arg", `enum Opt[T] { Sm(T), Nn }
function get(o: Opt[i32]): i32 { match (o) { Sm(v) => { return v; }, Nn => { return 42; } } }
function main(): i32 { return get(Nn); }`, 42},
	// mixed call arguments pin per-index off the callee's parameter list: arg 0
	// is a bare unit variant (needs the param type), arg 1 a payload variant
	// (infers from `40`). 1 + 40 == 41.
	{"unit_variant_call_arg_mixed", `enum Opt[T] { Sm(T), Nn }
function combine(a: Opt[i32], b: Opt[i32]): i32 {
    var x: i32 = 0;
    match (a) { Sm(v) => { x = v; }, Nn => { x = 1; } }
    match (b) { Sm(w) => { x = x + w; }, Nn => { x = x + 2; } }
    return x;
}
function main(): i32 { return combine(Nn, Sm(40)); }`, 41},
	// construction-only (no match): the construction alone must lower.
	{"construction_only", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    var o: Opt[i32] = Sm(5);
    return 1;
}`, 1},
	// two distinct instantiations of the same enum coexisting (`Opt[i32]` +
	// `Opt[string]`): each clones to its own concrete enum + variant structs.
	{"two_instantiations", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    var a: Opt[i32] = Sm(7);
    var b: Opt[string] = Sm("hey");
    var x: i32 = 0;
    match (a) { Sm(n) => { x = n; }, Nn => { } }
    var y: i32 = 0;
    match (b) { Sm(s) => { y = s.len(); }, Nn => { } }
    return x + y;
}`, 10},
	// match on a CALL result (`match (wrap(4))`): the scrutinee's instantiation
	// comes from the callee's declared return type, not an annotation.
	{"call_scrutinee", `enum Opt[T] { Sm(T), Nn }
function wrap(v: i32): Opt[i32] { return Sm(v); }
function main(): i32 {
    match (wrap(4)) { Sm(x) => { return x + 1; }, Nn => { return 0; } }
}`, 5},
	// array of a generic enum, iterated + matched per element. The unit-variant
	// element (`Nn`) gets its instantiation from the array's element type, and
	// the `for` loop variable carries the element type into the match.
	{"array_iter", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    var xs: Opt[i32][] = [Sm(1), Sm(2), Nn];
    var s: i32 = 0;
    for o in xs { match (o) { Sm(x) => { s = s + x; }, Nn => { } } }
    return s;
}`, 3},
	// match on an index into a generic-enum array (`match (xs[0])`).
	{"index_scrutinee", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    var xs: Opt[i32][] = [Sm(9), Nn];
    match (xs[0]) { Sm(x) => { return x; }, Nn => { return 0; } }
}`, 9},
	// string-payload array, method dispatch on the bound element through a `for`.
	{"string_array_method", `enum Box[T] { V(T) }
function main(): i32 {
    var xs: Box[string][] = [V("ab"), V("cde")];
    var n: i32 = 0;
    for b in xs { match (b) { V(s) => { n = n + s.len(); } } }
    return n;
}`, 5},
	// TWO type params: `Pair[i32, i32]` keys off the annotation, joining both
	// args into `i32__i32`; me_infer_variant_key unifies each payload field, so
	// the clone has concrete fields for both. 3 + 4 == 7.
	{"multiparam_i32", `enum Pair[K, V] { P(K, V) }
function main(): i32 {
    var p: Pair[i32, i32] = P(3, 4);
    match (p) { P(a, b) => { return a + b; } }
}`, 7},
	// two type params with MIXED payload types (i32 + string) + a unit variant —
	// the key `i32__string` drives concrete field types so `b.len()` dispatches.
	// 5 + "hi".len() == 7.
	{"multiparam_mixed", `enum Pair[K, V] { P(K, V), Z }
function main(): i32 {
    var p: Pair[i32, string] = P(5, "hi");
    match (p) { P(a, b) => { return a + b.len(); }, Z => { return 0; } }
}`, 7},
}

// TestSelfHostGenericEnumIRX86_64 builds the self-host asm_run driver and runs
// each generic-enum program through it (Fern source → x86-64 asm → native
// binary → exit code), asserting the oracle value. A size bound proves the
// small IR path was taken — a bail to the ~35 KB AST runtime would be far
// larger (and, for the un-monomorphised generic shapes, miscompiles).
func TestSelfHostGenericEnumIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range genericEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("asm is %d bytes — expected small IR output; the generic-enum module likely bailed to the AST runtime", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "generic_enum_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("generic-enum %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostGenericEnumWasmIR is the wasm sibling: monomorphize_enums is a
// target-independent parser pass, so the wasm IR backend gets generic enums for
// free. Each case asserts the same oracle exit code via the wasm_ir_run driver.
func TestSelfHostGenericEnumWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host generic-enum wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range genericEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "ge_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("generic-enum wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
