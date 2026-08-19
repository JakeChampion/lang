package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// assocFnIRCases exercise ASSOCIATED FUNCTIONS — receiver-less impl methods
// called as `Type.f(args)` (constructors / static methods) — through the
// stack-IR path. The self-host parser desugars an impl method whose first
// param isn't `self` into a FuncDecl with an empty receiver_name (so the
// emitter labels it `Type.f` with no receiver slot); irlower resolves a
// `Type.f(args)` call site (object = a bare declared-struct name, not a local)
// to `call_direct("Type.f")` with no receiver. Exit codes are the oracle.
//
// Scope (issue #2779 item 1): receiver-less associated functions on both
// structs (constructors) and enums (tag/default-style constructors). A
// nominal-returning enum associated fn returns a leak-only variant box (a heap
// pointer, like any value); the call site recovers the result's enum type from
// its registered "<Enum>.<f>" return type (struct_ret_fns_of records enum
// returns too), so it routes through the IR path — NOT the AST fallback — like
// the struct form. The enum-* cases below pin that down across both backends.
//
// A non-nominal-returning enum associated fn (e.g. `E.zero(): i32`) is the one
// case still left on the AST fallback (a safe miss): recognising it would key
// off a declared-enum check at the call site, which flips an extra self-host
// module onto the IR emit path and inflates the self-host bootstrap binary past
// the CI runner's memory ceiling. Not worth a bootstrap regression for an edge
// case, so it stays on AST until the self-host compiler's own footprint shrinks.
var assocFnIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Basic constructor: bind, then sum fields. 3 + 4 = 7.
	{"basic",
		`trait Mk { function make(a: i32, b: i32): Self; } struct Pt { x: i32, y: i32 } impl Mk for Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { var p: Pt = Pt.make(3, 4); return p.x + p.y; }`, 7},
	// Chained: read a field straight off the constructor result, twice.
	// 3 + 20 = 23.
	{"chained",
		`trait Mk { function make(a: i32, b: i32): Self; } struct Pt { x: i32, y: i32 } impl Mk for Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { return Pt.make(3, 4).x + Pt.make(10, 20).y; }`, 23},
	// Inferred binding: `var p = Pt.make(..)` with NO annotation — the struct
	// type is recovered from the associated-call return type. 5 + 6 = 11.
	{"inferred-binding",
		`trait Mk { function make(a: i32, b: i32): Self; } struct Pt { x: i32, y: i32 } impl Mk for Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { var p = Pt.make(5, 6); return p.x + p.y; }`, 11},
	// Zero-arg constructor: `Pt.origin()` takes no params (the empty-params
	// path that must not index m.params[0]). 0 + 0 + 9 = 9.
	{"zero-arg",
		`trait Orig { function origin(): Self; } struct Pt { x: i32, y: i32 } impl Orig for Pt { function origin(): Pt { return Pt { x: 0, y: 0 }; } } function main(): i32 { var p: Pt = Pt.origin(); return p.x + p.y + 9; }`, 9},
	// Two associated fns on one type, both used; one feeds the other's args.
	// make(2,3) → 5; scaled(5) constructs {5,10} → 15. 5 + 15 = 20.
	{"multi-assoc",
		`trait Mk2 { function make(a: i32, b: i32): Self; function scaled(a: i32): Self; } struct Pt { x: i32, y: i32 } impl Mk2 for Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } function scaled(a: i32): Pt { return Pt { x: a, y: a + a }; } } function main(): i32 { var p: Pt = Pt.make(2, 3); var q: Pt = Pt.scaled(5); return p.x + p.y + q.x + q.y; }`, 20},
	{"enum-tag",
		`trait Mk { function tag(n: i32): Self; } enum E { A(i32), B } impl Mk for E { function tag(n: i32): E { if (n > 0) { return A(n); } return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { var x: E = E.tag(5); var y: E = E.tag(0); return val(x) + val(y); }`, 104},
	{"enum-inline",
		`trait Mk { function tag(n: i32): Self; } enum E { A(i32), B } impl Mk for E { function tag(n: i32): E { if (n > 0) { return A(n); } return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { return val(E.tag(7)); }`, 7},
	{"enum-zero-arg",
		`trait Def { function def(): Self; } enum E { A(i32), B } impl Def for E { function def(): E { return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 42; } } return 0; } function main(): i32 { var x: E = E.def(); return val(x); }`, 42},
	{"enum-str-payload",
		`trait Mk { function of(s: string): Self; } enum E { Tag(string), Nil } impl Mk for E { function of(s: string): E { return Tag(s); } } function val(e: E): i32 { match (e) { Tag(w) => { return w.len(); }, Nil => { return 0; } } return 0; } function main(): i32 { var x: E = E.of("hello"); return val(x); }`, 5},
	{"enum-inferred",
		`trait Mk { function tag(n: i32): Self; } enum E { A(i32), B } impl Mk for E { function tag(n: i32): E { if (n > 0) { return A(n); } return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { var x = E.tag(3); return val(x); }`, 3},
}

// TestSelfHostAssocFnIRX86_64 routes each case through the self-hosted x86-64
// driver (asm_run → emit_module, IR default-on) and asserts the exit code,
// AND probes the routing (asm_pathprobe_run) to pin each case to the "ir"
// path — so a regression that silently kicked associated-function calls off
// the IR path surfaces here.
func TestSelfHostAssocFnIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range assocFnIRCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostAssocFnIRWasm runs the same cases through the wasm IR backend
// (wasm_ir_run -ir) so associated-function dispatch is verified on the
// stack-machine backend too (4-byte struct pointers), not just the register
// ABI.
func TestSelfHostAssocFnIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host assoc-fn wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range assocFnIRCases {
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
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "assocfn_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("assoc-fn wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
