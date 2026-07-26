package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// genStructFieldGenStructIRCase is a self-host program where a generic struct's
// FIELD is itself a generic struct parameterised by the outer type param —
// `struct Holder[U] { b: Box[U] }`. When `Holder[i32]` is monomorphised, the
// cloned field `Box[U]` substitutes to `Box[i32]` (already handled via mg_ty) —
// but inferring the OUTER key from the field literal failed: the inner literal
// `Box { v: 9 }` is rewritten to `Box__i32 { … }` BEFORE the outer key is
// inferred, and `bind_unify` couldn't unify the field pattern `Box[U]` against
// the mangled concrete `Box__i32`, so `U` stayed unbound and the module bailed.
// The fix teaches `bind_unify` to recover a mangled clone's type args
// (`Box__i32` → `[i32]`) and unify them with the generic pattern. Native handles
// it, so this closes a goal-1 IR-subset gap (the struct sibling of #3679). Each
// exit code is pinned against the native interpreter oracle and kept <= 120.
type genStructFieldGenStructIRCase struct {
	name     string
	src      string
	expected int
}

var genStructFieldGenStructIRCases = []genStructFieldGenStructIRCase{
	// the core shape: a generic struct field holding another generic struct.
	{"i32_field", `struct Box[T] { v: T }
struct Holder[U] { b: Box[U] }
function main(): i32 {
    var h: Holder[i32] = Holder { b: Box { v: 9 } };
    return h.b.v;
}`, 9},
	// string field, method dispatch on the innermost value.
	{"string_field", `struct Box[T] { v: T }
struct Holder[U] { b: Box[U] }
function main(): i32 {
    var h: Holder[string] = Holder { b: Box { v: "hey" } };
    return h.b.v.len();
}`, 3},
	// the outer struct also has a plain field alongside the generic one.
	{"mixed_fields", `struct Box[T] { v: T }
struct Holder[U] { tag: i32, b: Box[U] }
function main(): i32 {
    var h: Holder[i32] = Holder { tag: 3, b: Box { v: 4 } };
    return h.tag + h.b.v;
}`, 7},
	// two distinct instantiations of the outer struct coexisting.
	{"two_instantiations", `struct Box[T] { v: T }
struct Holder[U] { b: Box[U] }
function main(): i32 {
    var a: Holder[i32] = Holder { b: Box { v: 4 } };
    var c: Holder[string] = Holder { b: Box { v: "xyz" } };
    return a.b.v + c.b.v.len();
}`, 7},
	// the outer struct arrives as a function parameter.
	{"struct_param", `struct Box[T] { v: T }
struct Holder[U] { b: Box[U] }
function get(h: Holder[i32]): i32 { return h.b.v; }
function main(): i32 { return get(Holder { b: Box { v: 6 } }); }`, 6},
}

// TestSelfHostGenStructFieldGenStructIRX86_64 runs each case through the
// self-host asm_run driver. A size bound proves the small IR path was taken
// rather than a bail to the ~35 KB AST runtime.
func TestSelfHostGenStructFieldGenStructIRX86_64(t *testing.T) {
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

	for _, tc := range genStructFieldGenStructIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("asm is %d bytes — expected small IR output; the module likely bailed to the AST runtime", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "genstruct_field_genstruct_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("genstruct-field-genstruct %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostGenStructFieldGenStructWasmIR is the wasm sibling: the struct pass
// is a target-independent parser pass, so the wasm IR backend gets these free.
func TestSelfHostGenStructFieldGenStructWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host genstruct-field-genstruct wasm IR e2e")
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

	for _, tc := range genStructFieldGenStructIRCases {
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
			watFile := filepath.Join(dir, "gsfgs_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("genstruct-field-genstruct wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
