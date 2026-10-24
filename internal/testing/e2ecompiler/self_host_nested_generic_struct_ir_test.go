package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type nestedGenericStructIRCase struct {
	name     string
	src      string
	expected int
}

var nestedGenericStructIRCases = []nestedGenericStructIRCase{
	{"two_level_i32", `struct Box[T] { v: T }
function main(): i32 {
    let b: Box[Box[i32]] = Box { v: Box { v: 7 } };
    return b.v.v;
}`, 7},
	{"two_level_no_anno", `struct Box[T] { v: T }
function main(): i32 {
    let b = Box { v: Box { v: 7 } };
    return b.v.v;
}`, 7},
	{"three_level", `struct Box[T] { v: T }
function main(): i32 {
    let b: Box[Box[Box[i32]]] = Box { v: Box { v: Box { v: 5 } } };
    return b.v.v.v;
}`, 5},
	{"nested_string_method", `struct Box[T] { v: T }
function main(): i32 {
    let b: Box[Box[string]] = Box { v: Box { v: "hello" } };
    return b.v.v.len();
}`, 5},
	{"nested_method", `struct Box[T] { v: T }
function (b: Box[T]) get(): T { return b.v; }
function main(): i32 {
    let b: Box[Box[i32]] = Box { v: Box { v: 7 } };
    return b.get().v;
}`, 7},
	{"nested_method_both_levels", `struct Box[T] { v: T }
function (b: Box[T]) get(): T { return b.v; }
function main(): i32 {
    let b: Box[Box[i32]] = Box { v: Box { v: 7 } };
    let inner = b.get();
    return inner.get();
}`, 7},
	{"field_of_unannotated_local", `struct P[T] { b: T }
function main(): i32 {
    let p = P { b: "xy" };
    let q = P { b: p.b };
    return q.b.len();
}`, 2},
	{"rebind_from_unannotated_fields", `struct P[T] { a: T[], b: T }
function main(): i32 {
    let p = P { a: ["q"], b: "xy" };
    let q = P { a: p.a, b: p.b };
    return q.a.len();
}`, 1},
	{"reassign_over_own_field", `struct P[T] { a: T[], b: T }
function main(): i32 {
    let p = P { a: ["q"], b: "xy" };
    p = P { a: p.a.append("z"), b: p.b };
    return p.a.len();
}`, 2},
	{"field_of_call_bound_local", `struct P[T] { b: T }
function mk(): P[string] { return P { b: "xy" }; }
function main(): i32 {
    let p = mk();
    let q = P { b: p.b };
    return q.b.len();
}`, 2},
	{"field_of_for_var", `struct P[T] { b: T }
function main(): i32 {
    let ps: P[string][] = [P { b: "xy" }, P { b: "abc" }];
    let n: i32 = 0;
    for p in ps { let q = P { b: p.b }; n = n + q.b.len(); }
    return n;
}`, 5},
	{"field_of_unannotated_ok_payload", `struct P[T] { b: T }
function mk(): Result[P[string], string] { return Ok(P { b: "xy" }); }
function main(): i32 {
    let r = mk();
    match (r) {
        Ok(p) => { let q = P { b: p.b }; return q.b.len(); },
        Err(e) => { return 9; }
    }
}`, 2},
	{"field_of_unannotated_err_payload", `struct P[T] { b: T }
function mk(): Result[i32, P[string]] { return Err(P { b: "abc" }); }
function main(): i32 {
    let r = mk();
    match (r) {
        Ok(v) => { return v; },
        Err(p) => { let q = P { b: p.b }; return q.b.len(); }
    }
}`, 3},
	{"field_of_unannotated_some_payload", `struct P[T] { b: T }
function main(): i32 {
    let o = Some(P { b: "xy" });
    match (o) {
        Some(p) => { let q = P { b: p.b }; return q.b.len(); },
        None => { return 9; }
    }
}`, 2},
	{"field_of_enum_payload", `struct P[T] { b: T }
enum E { A(P[string]), B }
function main(): i32 {
    let e: E = A(P { b: "xy" });
    match (e) {
        A(p) => { let q = P { b: p.b }; return q.b.len(); },
        B => { return 9; }
    }
}`, 2},
	{"field_of_generic_enum_payload", `struct P[T] { b: T }
enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }
function main(): i32 {
    let t: Tree[string] = Leaf("xyz");
    match (t) {
        Leaf(v) => { let q = P { b: v }; return q.b.len(); },
        Node(l, r) => { return 9; }
    }
}`, 3},
	{"field_of_option_payload", `struct P[T] { b: T }
function main(): i32 {
    let o: Option[P[string]] = Some(P { b: "xy" });
    match (o) {
        Some(p) => { let q = P { b: p.b }; return q.b.len(); },
        None => { return 9; }
    }
}`, 2},
	{"literal_in_array_argument", `struct Same[T] { a: T, b: T }
function take(xs: Same[i64][]): i32 {
    let s: i64 = xs[0].a + xs[0].b + xs[1].b;
    return (s - 8589934592) as i32;
}
function main(): i32 {
    let y: i64 = 4294967296;
    return take([Same { a: 3, b: y }, Same { a: 1, b: 4294967296 }]);
}`, 3},
	{"coexist_with_flat", `struct Box[T] { v: T }
function main(): i32 {
    let a: Box[Box[i32]] = Box { v: Box { v: 3 } };
    let c: Box[i32] = Box { v: 4 };
    return a.v.v + c.v;
}`, 7},
}

func TestSelfHostNestedGenericStructIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range nestedGenericStructIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("driver produced no asm")
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

func TestSelfHostNestedGenericStructWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-generic-struct wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range nestedGenericStructIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
