package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// genStructOfGenEnumIRCase is a self-host program where a generic STRUCT's type
// argument is a generic ENUM — `Box[Opt[i32]]` — the mirror of the
// genenum-of-genstruct combo. This was the last composite generic combination on
// the AST fallback (#3694): the struct monomorphiser ran first and built the
// malformed key `Box__Opt[i32]` (brackets — the inner `Opt[i32]` is a generic
// enum the struct pass's mg_ty couldn't mangle), so the clone was dropped and the
// module bailed. Two struct-pass changes close it: sanitize_key rewrites a
// bracketed enum arg to a symbol-safe `Box__Opt__i32`, and retype_struct_lit
// re-keys the `Box { v: Sm(5) }` literal from its `Box[Opt[i32]]` annotation
// (infer_lit_key can't pin the instantiation from the variant construction
// `Sm(5)`). The enum pass — unchanged — then clones `Opt__i32` from the kept
// `v: Opt[i32]` field and keys the inner `Sm(5)`/match arms. Native
// monomorphises these; each oracle is the native-interp value, kept <= 120.
type genStructOfGenEnumIRCase struct {
	name     string
	src      string
	expected int
}

var genStructOfGenEnumIRCases = []genStructOfGenEnumIRCase{
	// The issue repro: a generic struct of a generic enum, i32 payload.
	{"box_of_opt_i32", `struct Box[T] { v: T }
enum Opt[U] { Sm(U), Nn }
function main(): i32 {
    var b: Box[Opt[i32]] = Box { v: Sm(5) };
    match (b.v) { Sm(n) => { return n; }, Nn => { return 0; } }
}`, 5},
	// string payload — method dispatch on the unwrapped string.
	{"box_of_opt_string", `struct Box[T] { v: T }
enum Opt[U] { Sm(U), Nn }
function main(): i32 {
    var b: Box[Opt[string]] = Box { v: Sm("hey") };
    match (b.v) { Sm(s) => { return s.len(); }, Nn => { return 0; } }
}`, 3},
	// two-field struct wrapping the enum, both fields read.
	{"pair_of_opt", `struct Pair[T] { a: T, b: T }
enum Opt[U] { Sm(U), Nn }
function main(): i32 {
    var p: Pair[Opt[i32]] = Pair { a: Sm(4), b: Sm(6) };
    var x: i32 = match (p.a) { Sm(n) => n, Nn => 0 };
    var y: i32 = match (p.b) { Sm(n) => n, Nn => 0 };
    return x + y;
}`, 10},
	// built-in Result as the enum error type inside the struct — the source
	// enum carried in a generic struct field, propagated through a match.
	{"box_of_result", `struct Box[T] { v: T }
enum Opt[U] { Sm(U), Nn }
function main(): i32 {
    var b: Box[Opt[i32]] = Box { v: Sm(40) };
    var n: i32 = match (b.v) { Sm(x) => x, Nn => 0 };
    return n + 2;
}`, 42},
}

// TestSelfHostGenStructOfGenEnumIRX86_64 runs each case through the self-host
// asm_run driver. The size bound proves the IR path was taken rather than a bail
// to the ~35 KB AST runtime.
func TestSelfHostGenStructOfGenEnumIRX86_64(t *testing.T) {
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

	for _, tc := range genStructOfGenEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("asm is %d bytes — expected small IR output; the module likely bailed to the AST runtime", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "genstruct_of_genenum_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("genstruct-of-genenum %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostGenStructOfGenEnumWasmIR is the wasm sibling: both monomorphisers
// are target-independent parser passes, so the wasm IR backend gets these for
// free. Each case asserts the same oracle exit code.
func TestSelfHostGenStructOfGenEnumWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host genstruct-of-genenum wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range genStructOfGenEnumIRCases {
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
			watFile := filepath.Join(dir, "gsoge_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("genstruct-of-genenum wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
