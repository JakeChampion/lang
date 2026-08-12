package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// selfrefStructIRCases widen the self-host IR subset to SELF-REFERENTIAL (and
// mutually-recursive) structs — `struct Node { v: i32, next: Node[] }` and the
// like, the shape behind linked lists / trees / ASTs.
//
// The leak-safety gate (irlower.decl_is_leaksafe_d) walks a struct's field type
// graph to decide whether it can ride the IR path in leak mode (no RC; the boxes
// leak with the struct, matching the AST path's exit codes). It used a depth cap
// to avoid looping on cyclic type graphs, which also rejected legitimate
// self-referential structs — a `next: Node[]` field recurses into Node forever
// until the cap trips, bailing the whole module to the AST emitter. The gate now
// threads a `visiting` set and treats a back-edge to a struct already on the
// proof path as leak-safe (a leak-only back-pointer introduces no unsafe field),
// so self-referential and mutually-recursive structs route the IR path while the
// outer struct's own fields are still each validated.
//
// Each case is routing-pinned to "ir", oracle-checked against the interpreter,
// and returns a value <= 120 (cf. the wasmtime exit-code gap #2908).
var selfrefStructIRCases = []struct {
	name string
	main string
}{
	// Bind a self-referential struct, read a scalar field.
	{"bind-scalar", `struct Node { v: i32, next: Node[] }
function main(): i32 { var n = Node { v: 5, next: [] }; return n.v; }`},
	// Empty self-referential array field length.
	{"empty-next-len", `struct Node { v: i32, next: Node[] }
function main(): i32 { var n = Node { v: 5, next: [] }; return n.next.len(); }`},
	// One child: array length + element scalar field read.
	{"one-child-len", `struct Node { v: i32, next: Node[] }
function main(): i32 { var leaf = Node { v: 1, next: [] }; var n = Node { v: 5, next: [leaf] }; return n.next.len(); }`},
	{"one-child-field", `struct Node { v: i32, next: Node[] }
function main(): i32 { var leaf = Node { v: 7, next: [] }; var n = Node { v: 5, next: [leaf] }; return n.next[0].v; }`},
	// Several children: sum element scalar fields in a loop.
	{"children-sum", `struct Node { v: i32, next: Node[] }
function main(): i32 {
    var a = Node { v: 10, next: [] };
    var b = Node { v: 20, next: [] };
    var c = Node { v: 30, next: [] };
    var root = Node { v: 1, next: [a, b, c] };
    var s = 0;
    for ch in root.next { s = s + ch.v; }
    return s + root.v;
}`},
	// Self-referential struct as a function parameter + return.
	{"as-param", `struct Node { v: i32, next: Node[] }
function head_val(n: Node): i32 { return n.v; }
function main(): i32 { var n = Node { v: 42, next: [] }; return head_val(n); }`},
	// Mutually-recursive structs: A holds B[], B holds A[].
	{"mutual-rec", `struct A { tag: i32, bs: B[] }
struct B { val: i32, peers: A[] }
function main(): i32 {
    var b = B { val: 9, peers: [] };
    var a = A { tag: 3, bs: [b] };
    return a.bs[0].val + a.tag;
}`},
}

// TestSelfHostSelfrefStructIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, routing pinned to "ir".
func TestSelfHostSelfrefStructIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range selfrefStructIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostSelfrefStructIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostSelfrefStructIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host self-ref-struct wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range selfrefStructIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "selfref_struct_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("self-ref-struct wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
