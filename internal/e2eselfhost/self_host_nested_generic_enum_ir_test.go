package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// nestedGenericEnumIRCase is a self-host generic-enum program whose type
// argument is ITSELF a generic enum (`Opt[Opt[i32]]`) — a COMPOSITE
// instantiation key. The base generic-enum pass (#3572) keys clones with a
// `__`-joined string of simple nominals (`Opt[i32]` → `Opt__i32`) and rejects a
// composite arg (`is_simple_key`), so a nested generic enum bailed to the legacy
// AST emitter. This follow-up extends monomorphize_enums (parser.fern): for a
// SINGLE-type-param enum the one arg may itself be a (recursively) mangled
// generic-enum clone, so `Opt[Opt[i32]]` clones to `Opt__Opt__i32` with field
// type `Opt__i32`, the inner `Opt[i32]` is enqueued + cloned too, a nested
// `match` recovers the bound payload's instantiation, and a nested unit-variant
// payload (`Sm(Nn)`) is pinned from the outer annotation. Each exit code is
// pinned against the native interpreter oracle and kept <= 120.
type nestedGenericEnumIRCase struct {
	name     string
	src      string
	expected int
}

var nestedGenericEnumIRCases = []nestedGenericEnumIRCase{
	// the core shape: construct `Sm(Sm(3))` and extract through two matches.
	{"two_level_i32", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    var o: Opt[Opt[i32]] = Sm(Sm(3));
    match (o) {
        Sm(n) => { match (n) { Sm(m) => { return m; }, Nn => { return 0; } } },
        Nn => { return 0; }
    }
}`, 3},
	// the OUTER value is the unit variant `Nn` (pinned from the annotation).
	{"outer_unit", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    var o: Opt[Opt[i32]] = Nn;
    match (o) { Sm(n) => { return 1; }, Nn => { return 9; } }
}`, 9},
	// the INNER payload is the unit variant `Nn` (`Sm(Nn)`): the inner `Nn` has
	// nothing to infer from, so its instantiation is propagated from the outer
	// `Opt[Opt[i32]]` annotation into the construction argument.
	{"inner_unit", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    var o: Opt[Opt[i32]] = Sm(Nn);
    match (o) {
        Sm(n) => { match (n) { Sm(m) => { return m; }, Nn => { return 7; } } },
        Nn => { return 0; }
    }
}`, 7},
	// three levels of nesting (`Opt[Opt[Opt[i32]]]`).
	{"three_level", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    var o: Opt[Opt[Opt[i32]]] = Sm(Sm(Sm(5)));
    match (o) {
        Sm(a) => { match (a) { Sm(b) => { match (b) { Sm(c) => { return c; }, Nn => { return 0; } } }, Nn => { return 0; } } },
        Nn => { return 0; }
    }
}`, 5},
	// two DIFFERENT generic enums nested (`Outer[Inner[i32]]`).
	{"two_kinds", `enum Inner[T] { I(T) }
enum Outer[U] { O(U) }
function main(): i32 {
    var o: Outer[Inner[i32]] = O(I(5));
    match (o) { O(x) => { match (x) { I(n) => { return n + 1; } } } }
}`, 6},
	// a nested enum and a plain instantiation of the same enum coexisting.
	{"coexist_with_flat", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    var a: Opt[Opt[i32]] = Sm(Sm(3));
    var b: Opt[i32] = Sm(4);
    var x: i32 = 0;
    match (a) { Sm(n) => { match (n) { Sm(m) => { x = m; }, Nn => { } } }, Nn => { } }
    match (b) { Sm(k) => { x = x + k; }, Nn => { } }
    return x;
}`, 7},
	// nested string payload, with method dispatch on the innermost binding.
	{"nested_string_method", `enum Box[T] { V(T) }
function main(): i32 {
    var o: Box[Box[string]] = V(V("hello"));
    match (o) { V(inner) => { match (inner) { V(s) => { return s.len(); } } } }
}`, 5},
}

// TestSelfHostNestedGenericEnumIRX86_64 runs each nested generic-enum program
// through the self-host asm_run driver (Fern → x86-64 asm → binary → exit code).
// A size bound proves the small IR path was taken rather than a bail to the
// ~35 KB AST runtime.
func TestSelfHostNestedGenericEnumIRX86_64(t *testing.T) {
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

	for _, tc := range nestedGenericEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("asm is %d bytes — expected small IR output; the nested generic-enum module likely bailed to the AST runtime", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "nested_generic_enum_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("nested-generic-enum %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostNestedGenericEnumWasmIR is the wasm sibling: monomorphize_enums is
// a target-independent parser pass, so the wasm IR backend gets nested generic
// enums for free. Each case asserts the same oracle exit code.
func TestSelfHostNestedGenericEnumWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-generic-enum wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range nestedGenericEnumIRCases {
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
			watFile := filepath.Join(dir, "nge_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("nested-generic-enum wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
