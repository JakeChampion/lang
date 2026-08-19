package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// genEnumOfGenStructIRCase is a self-host program where a generic enum's type
// argument is itself a generic STRUCT — `Opt[Box[i32]]`, and the built-in
// `Option[Box[i32]]` / `Result[Box[i32], …]` forms. This is a cross-pass
// composite key: the struct monomorphiser (`Box[i32]` → `Box__i32`) runs before
// the enum monomorphiser, but the enum/Option annotation `Opt[Box[i32]]` was
// left with the un-mangled inner `Box[i32]` (mg_ty only recursed into the args
// of a generic-STRUCT base), so the enum pass saw a composite arg it couldn't
// key and the module bailed to the AST emitter. The fix makes the struct pass's
// mg_ty recurse into the args of a NON-generic-struct generic base too
// (`Opt[Box[i32]]` → `Opt[Box__i32]`), so the enum pass then sees a plain
// nominal arg `Box__i32` it keys the usual way. Native monomorphises these, so
// this closes a goal-1 IR-subset gap. Each exit code is pinned against the
// native interpreter oracle and kept <= 120.
type genEnumOfGenStructIRCase struct {
	name     string
	src      string
	expected int
}

var genEnumOfGenStructIRCases = []genEnumOfGenStructIRCase{
	// user generic enum wrapping a user generic struct, i32 payload.
	{"user_enum_i32", `enum Opt[T] { Sm(T), Nn }
struct Box[U] { v: U }
function main(): i32 {
    var o: Opt[Box[i32]] = Sm(Box { v: 7 });
    match (o) { Sm(b) => { return b.v; }, Nn => { return 0; } }
}`, 7},
	// string payload, method dispatch on the inner struct's field.
	{"user_enum_string", `enum Opt[T] { Sm(T), Nn }
struct Box[U] { v: U }
function main(): i32 {
    var o: Opt[Box[string]] = Sm(Box { v: "hi" });
    match (o) { Sm(b) => { return b.v.len(); }, Nn => { return 0; } }
}`, 2},
	// the unit variant pinned from the annotation (no payload to infer from).
	{"user_enum_unit", `enum Opt[T] { Sm(T), Nn }
struct Box[U] { v: U }
function main(): i32 {
    var o: Opt[Box[i32]] = Nn;
    match (o) { Sm(b) => { return b.v; }, Nn => { return 9; } }
}`, 9},
	// built-in Option of a generic struct.
	{"option_of_struct", `struct Box[U] { v: U }
function main(): i32 {
    var o: Option[Box[i32]] = Some(Box { v: 5 });
    match (o) { Some(b) => { return b.v; }, None => { return 0; } }
}`, 5},
	// built-in Result of a generic struct, propagated across a call boundary.
	{"result_of_struct", `struct Box[U] { v: U }
function f(): Result[Box[i32], string] { return Ok(Box { v: 9 }); }
function main(): i32 {
    match (f()) { Ok(b) => { return b.v; }, Err(e) => { return 0; } }
}`, 9},
	// a struct with two fields as the enum payload (read both via the binding).
	{"struct_two_fields", `enum Opt[T] { Sm(T), Nn }
struct Pt[U] { x: U, y: U }
function main(): i32 {
    var o: Opt[Pt[i32]] = Sm(Pt { x: 3, y: 4 });
    match (o) { Sm(p) => { return p.x + p.y; }, Nn => { return 0; } }
}`, 7},
}

// TestSelfHostGenEnumOfGenStructIRX86_64 runs each case through the self-host
// asm_run driver. A size bound proves the small IR path was taken rather than a
// bail to the ~35 KB AST runtime.
func TestSelfHostGenEnumOfGenStructIRX86_64(t *testing.T) {
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

	for _, tc := range genEnumOfGenStructIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("asm is %d bytes — expected small IR output; the module likely bailed to the AST runtime", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "genenum_of_genstruct_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("genenum-of-genstruct %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostGenEnumOfGenStructWasmIR is the wasm sibling: both monomorphisers
// are target-independent parser passes, so the wasm IR backend gets these for
// free. Each case asserts the same oracle exit code.
func TestSelfHostGenEnumOfGenStructWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host genenum-of-genstruct wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range genEnumOfGenStructIRCases {
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
			watFile := filepath.Join(dir, "geogs_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("genenum-of-genstruct wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
