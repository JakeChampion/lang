package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// retStructFieldIRCases pin the move-on-return of a POINTER-shaped FIELD of a
// local struct on the self-host IR path (#4801). A function `return r.node` over a
// local `var r: P = ...` hands the field out to the caller, but the lowerer's exit
// dec-sweep still reclaimed `r` — emit_struct_field_drops' __struct_drop_<T> then
// DEEP-freed the returned field out from under the caller. It surfaced as a SIGSEGV
// only once a later allocation recycled the freed block: the watbin `wat_parse`
// (`var r = wat_parse_one(...); return r.node;`) returned a dangling SExpr tree, so
// emit_binary's enc_functype allocations recycled a freed tree node and the import
// walk dereferenced an encoder byte (0x7f) as a node pointer — the SIGABRT/exit-134
// the whole watbin/wit/component test family tripped on. The fix keeps the parent
// struct local out of the sweep (returned_moved_arr_slots' ExprFieldAccess arm), so
// the returned field survives; the box + any un-returned heap sibling fields LEAK
// (sound, never over-free), the struct-field sibling of the #3720 enum/array move.
// Each case is routing-pinned to "ir" and value-pinned against the native oracle.
var retStructFieldIRCases = []struct {
	name string
	src  string
	want int
}{
	// The core trigger: get() returns r.node — a nested STRUCT field carrying its
	// own subtree — then main walks the tree ACROSS an allocation that recycles the
	// freed node. Pre-fix: SIGSEGV; the exact watbin wat_parse shape distilled.
	{"tree_struct_field", `struct Node { kind: i32, text: string, items: Node[] }
struct P { node: Node, pos: i32 }
function mk(): P { return P { node: Node { kind: 1, text: "ab", items: [Node { kind: 2, text: "x", items: [] }, Node { kind: 2, text: "y", items: [] }] }, pos: 7 }; }
function get(): Node { var r: P = mk(); return r.node; }
function main(): i32 {
    var t: Node = get();
    var s: i32 = t.text.len();
    var pad: i32[] = [];
    var k: i32 = 0;
    while (k < 40) { pad = pad.append(127); k = k + 1; }
    s = s + t.items.len() + t.items[0].text.len() + t.items[1].text.len() + pad.len();
    return s;
}`, 46},
	// A mutually-recursive parser returning r.node (a struct FIELD, not an enum
	// payload) — the wat_parse_one / wat_parse shape watbin actually uses.
	{"recursive_tree_field", `struct Node { kind: i32, text: string, items: Node[] }
struct PS { node: Node, pos: i32 }
function parse_one(s: string, i: i32): PS {
    if (s[i] == 40) {
        var inner: PS = parse_many(s, i + 1);
        var pos: i32 = inner.pos;
        if (pos < s.len() && s[pos] == 41) { pos = pos + 1; }
        return PS { node: inner.node, pos: pos };
    }
    return PS { node: Node { kind: 2, text: "leaf", items: [] }, pos: i + 1 };
}
function parse_many(s: string, i: i32): PS {
    var items: Node[] = [];
    var pos: i32 = i;
    while (pos < s.len() && s[pos] != 41) {
        var p: PS = parse_one(s, pos);
        items = items.append(p.node);
        pos = p.pos;
    }
    return PS { node: Node { kind: 0, text: "grp", items: items }, pos: pos };
}
function wat_parse(s: string): Node {
    var r: PS = parse_many(s, 0);
    return r.node;
}
function count(t: Node): i32 {
    var c: i32 = 1;
    var k: i32 = 0;
    while (k < t.items.len()) { c = c + count(t.items[k]); k = k + 1; }
    return c;
}
function main(): i32 {
    var root: Node = wat_parse("(ab)c");
    var pad: i32[] = [];
    var k: i32 = 0;
    while (k < 20) { pad = pad.append(1); k = k + 1; }
    return count(root) + pad.len();
}`, 25},
	// A STRING field return `return r.s` — the field is a heap string moved out; the
	// parent leaks (sound). Value-pinned so a mis-applied keep still reads correctly.
	{"string_field", `struct S { s: string, n: i32 }
function mk(): S { return S { s: "hello" + "!", n: 3 }; }
function get(): string { var r: S = mk(); return r.s; }
function main(): i32 {
    var x: string = get();
    var pad: i32[] = [];
    var k: i32 = 0;
    while (k < 30) { pad = pad.append(127); k = k + 1; }
    return x.len() + pad.len();
}`, 36},
	// An ARRAY field return `return r.xs` — the buffer moves out with the parent
	// leaking; walked after a recycling allocation.
	{"array_field", `struct A { xs: i32[], n: i32 }
function mk(): A { return A { xs: [10, 20, 30], n: 2 }; }
function get(): i32[] { var r: A = mk(); return r.xs; }
function main(): i32 {
    var v: i32[] = get();
    var pad: i32[] = [];
    var k: i32 = 0;
    while (k < 25) { pad = pad.append(127); k = k + 1; }
    return v.len() + v[0] + v[2] + pad.len();
}`, 68},
}

// TestSelfHostRetStructFieldIRX86_64 routes each case through the self-host x86-64
// IR driver (pinned to "ir") and asserts the native-oracle exit code.
func TestSelfHostRetStructFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range retStructFieldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "ret_struct_field_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("ret-struct-field %q exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostRetStructFieldWasmIR runs the same cases through the wasm IR backend.
func TestSelfHostRetStructFieldWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host ret-struct-field wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range retStructFieldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "ret_struct_field_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("ret-struct-field wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
