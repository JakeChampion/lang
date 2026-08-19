package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// genEnumPayloadGenEnumIRCase is a self-host program where a generic enum's
// variant payload is ITSELF a generic enum parameterised by the outer type
// param — `enum E[U] { A(Opt[U]), B }`. When `E[i32]` is monomorphised, the
// cloned variant `A`'s payload `Opt[U]` substitutes to `Opt[i32]`, which must in
// turn be mangled to `Opt__i32` and cloned; and a nested `match (o)` on the
// bound payload must recover `o`'s real type `Opt[i32]` (not the outer arg
// `i32`). A cloned variant field keeping the un-mangled `Opt[i32]` dangles on
// the dropped generic `Opt`, and typing the binding as the bare outer arg bails
// the module.
// Native handles it, so this closes a goal-1 IR-subset gap. Each exit code is
// pinned against the native interpreter oracle and kept <= 120.
type genEnumPayloadGenEnumIRCase struct {
	name     string
	src      string
	expected int
}

var genEnumPayloadGenEnumIRCases = []genEnumPayloadGenEnumIRCase{
	// the core shape: A(Opt[i32]), extracted through two matches.
	{"i32_payload", `enum Opt[T] { Sm(T), Nn }
enum E[U] { A(Opt[U]), B }
function main(): i32 {
    var e: E[i32] = A(Sm(5));
    match (e) { A(o) => { match (o) { Sm(n) => { return n; }, Nn => { return 0; } } }, B => { return 0; } }
}`, 5},
	// string payload, method dispatch on the innermost binding.
	{"string_payload", `enum Opt[T] { Sm(T), Nn }
enum E[U] { A(Opt[U]), B }
function main(): i32 {
    var e: E[string] = A(Sm("hi"));
    match (e) { A(o) => { match (o) { Sm(n) => { return n.len(); }, Nn => { return 0; } } }, B => { return 0; } }
}`, 2},
	// the OUTER unit variant `B` (pinned from the annotation).
	{"outer_unit", `enum Opt[T] { Sm(T), Nn }
enum E[U] { A(Opt[U]), B }
function main(): i32 {
    var e: E[i32] = B;
    match (e) { A(o) => { return 1; }, B => { return 8; } }
}`, 8},
	// arithmetic on the extracted inner payload.
	{"inner_arith", `enum Opt[T] { Sm(T), Nn }
enum E[U] { A(Opt[U]), B }
function main(): i32 {
    var e: E[i32] = A(Sm(4));
    match (e) { A(o) => { match (o) { Sm(n) => { return n + 1; }, Nn => { return 0; } } }, B => { return 0; } }
}`, 5},
	// two distinct instantiations of E (`E[i32]` + `E[string]`) coexisting.
	{"two_instantiations", `enum Opt[T] { Sm(T), Nn }
enum E[U] { A(Opt[U]), B }
function main(): i32 {
    var x: E[i32] = A(Sm(3));
    var y: E[string] = A(Sm("zz"));
    var r: i32 = 0;
    match (x) { A(o) => { match (o) { Sm(n) => { r = n; }, Nn => { } } }, B => { } }
    match (y) { A(o) => { match (o) { Sm(s) => { r = r + s.len(); }, Nn => { } } }, B => { } }
    return r;
}`, 5},
}

// TestSelfHostGenEnumPayloadGenEnumIRX86_64 runs each case through the self-host
// asm_run driver. A size bound proves the small IR path was taken rather than a
// bail to the ~35 KB AST runtime.
func TestSelfHostGenEnumPayloadGenEnumIRX86_64(t *testing.T) {
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

	for _, tc := range genEnumPayloadGenEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("asm is %d bytes — expected small IR output; the module likely bailed to the AST runtime", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "genenum_payload_genenum_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("genenum-payload-genenum %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostGenEnumPayloadGenEnumWasmIR is the wasm sibling: the enum pass is a
// target-independent parser pass, so the wasm IR backend gets these for free.
func TestSelfHostGenEnumPayloadGenEnumWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host genenum-payload-genenum wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range genEnumPayloadGenEnumIRCases {
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
			watFile := filepath.Join(dir, "gepge_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("genenum-payload-genenum wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
