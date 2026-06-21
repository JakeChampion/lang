package e2e

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
	// construction-only (no match): the construction alone previously bailed.
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
			if len(asm) == 0 || len(asm) > 15000 {
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
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
