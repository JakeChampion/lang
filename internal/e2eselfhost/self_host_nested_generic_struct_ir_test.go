package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// nestedGenericStructIRCase is a self-host generic-struct program whose type
// argument is ITSELF a generic struct (`Box[Box[i32]]`) — a COMPOSITE
// instantiation key. The generic-struct monomorphiser (parser.fern) rewrites a
// nested struct literal's inner instantiation first, so `infer_lit_key` already
// sees the mangled inner type (`Box__i32`) and keys the outer clone
// `Box__Box__i32`; the bug was phase-2 (and the method clone) splitting that
// single-param key on `__`, fracturing the mangled nested arg back into `["Box",
// "i32"]` and substituting the field type to a dangling bare `Box`. The fix uses
// the whole key as the one concrete arg for a single-type-param struct (and its
// methods); multi-param keys still split unambiguously. The native compiler
// monomorphises these, so this closes a goal-1 IR-subset gap. Each exit code is
// pinned against the native interpreter oracle and kept <= 120.
type nestedGenericStructIRCase struct {
	name     string
	src      string
	expected int
}

var nestedGenericStructIRCases = []nestedGenericStructIRCase{
	// the core shape, annotation-driven instantiation.
	{"two_level_i32", `struct Box[T] { v: T }
function main(): i32 {
    var b: Box[Box[i32]] = Box { v: Box { v: 7 } };
    return b.v.v;
}`, 7},
	// no annotation: the inner literal's own instantiation drives the outer key.
	{"two_level_no_anno", `struct Box[T] { v: T }
function main(): i32 {
    var b = Box { v: Box { v: 7 } };
    return b.v.v;
}`, 7},
	// three levels of nesting.
	{"three_level", `struct Box[T] { v: T }
function main(): i32 {
    var b: Box[Box[Box[i32]]] = Box { v: Box { v: Box { v: 5 } } };
    return b.v.v.v;
}`, 5},
	// nested string payload, method dispatch on the innermost field.
	{"nested_string_method", `struct Box[T] { v: T }
function main(): i32 {
    var b: Box[Box[string]] = Box { v: Box { v: "hello" } };
    return b.v.v.len();
}`, 5},
	// a method on the generic struct, called on a nested instantiation (the
	// method clone keys the same composite single-param way).
	{"nested_method", `struct Box[T] { v: T }
function (b: Box[T]) get(): T { return b.v; }
function main(): i32 {
    var b: Box[Box[i32]] = Box { v: Box { v: 7 } };
    return b.get().v;
}`, 7},
	// the method is called at BOTH levels (`b.get()` returns the inner Box,
	// then `.get()` on that returns the i32).
	{"nested_method_both_levels", `struct Box[T] { v: T }
function (b: Box[T]) get(): T { return b.v; }
function main(): i32 {
    var b: Box[Box[i32]] = Box { v: Box { v: 7 } };
    var inner = b.get();
    return inner.get();
}`, 7},
	// a nested instantiation and a flat instantiation of the same struct
	// coexisting (each clones independently).
	{"coexist_with_flat", `struct Box[T] { v: T }
function main(): i32 {
    var a: Box[Box[i32]] = Box { v: Box { v: 3 } };
    var c: Box[i32] = Box { v: 4 };
    return a.v.v + c.v;
}`, 7},
}

// TestSelfHostNestedGenericStructIRX86_64 runs each nested generic-struct
// program through the self-host asm_run driver (Fern → x86-64 asm → binary →
// exit code). A size bound proves the small IR path was taken rather than a bail
// to the ~35 KB AST runtime.
func TestSelfHostNestedGenericStructIRX86_64(t *testing.T) {
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

	for _, tc := range nestedGenericStructIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("asm is %d bytes — expected small IR output; the nested generic-struct module likely bailed to the AST runtime", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "nested_generic_struct_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("nested-generic-struct %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostNestedGenericStructWasmIR is the wasm sibling: monomorphize_structs
// is a target-independent parser pass, so the wasm IR backend gets nested generic
// structs for free. Each case asserts the same oracle exit code.
func TestSelfHostNestedGenericStructWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-generic-struct wasm IR e2e")
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

	for _, tc := range nestedGenericStructIRCases {
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
			watFile := filepath.Join(dir, "ngs_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("nested-generic-struct wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
