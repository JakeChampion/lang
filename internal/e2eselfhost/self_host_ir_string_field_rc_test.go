package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostIRStringFieldRcX86_64 guards against the IR string-field rc
// corruption: IR string boxes are header-less (const_str allocates
// [data@0, len@8] via __fern_alloc — "not RC'd"; strings leak on every IR
// backend), so emitting __fern_rc_inc / __fern_rc_dec on a string struct-field
// value writes [box-8] — the PREVIOUS heap block's word — silently corrupting
// it. This surfaced when the whole parser was routed through the IR path:
// resolve_labels_module rebuilds a `FuncDecl[]` by constructing FuncDecl
// literals whose non-fresh `string` fields (e.g. a field-copied receiver_name)
// were alias-inc'd, and the rc_inc clobbered the adjacent funcs array's length —
// so parse_module returned the wrong function count / crashed.
//
// These programs reconstruct arrays of structs carrying non-fresh `string`
// fields (the parser's AST-node-rebuild shape) and read them back; an over- or
// under-count from a corrupted array header, or a crash, is caught by the exit
// code (oracle-pinned). Routing is the IR path (asm_run → emit_module_ir); the
// size bound rejects an AST-fallback bail.
func TestSelfHostIRStringFieldRcX86_64(t *testing.T) {
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
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		// Reconstruct an array of structs whose `name` (string) field is a
		// non-fresh field-copy — the resolve_labels_module shape. Read the count
		// + a scalar field back: a corrupted array header shows as a wrong count.
		{"rebuild-struct-array-string-field", `struct Node { name: string, kids: i32[], n: i32 }
function rebuild(src: Node[]): Node[] {
	var out: Node[] = [];
	var i: i32 = 0;
	while (i < src.len()) {
		var s: Node = src[i] as i32;
		out = out.append(Node { name: s.name, kids: s.kids, n: s.n });
		i = i + 1;
	}
	return out;
}
function main(): i32 {
	var src: Node[] = [];
	var i: i32 = 0;
	while (i < 6) { src = src.append(Node { name: "x", kids: [], n: i }); i = i + 1; }
	var r: Node[] = rebuild(src);
	var sum: i32 = 0; var j: i32 = 0;
	while (j < r.len()) { sum = sum + r[j].n; j = j + 1; }
	return r.len() + sum;
}`, 6 + 15}, // len 6 + (0+1+2+3+4+5)=15 -> 21

		// Construct a struct with a non-fresh (param) string field next to a
		// freshly-built array, then read the array length back: the string field's
		// (removed) alias-inc must not clobber the array header.
		{"struct-string-field-beside-array", `struct H { tag: string, n: i32 }
function mk(name: string, k: i32): H { return H { tag: name, n: k }; }
function main(): i32 {
	var guard: i32[] = [7, 8, 9];
	var h: H = mk("hello", guard.len());
	return guard.len() * 10 + h.n + h.tag.len();
}`, 3*10 + 3 + 5}, // guard.len 3 *10 + h.n(=guard.len=3) + "hello".len 5 -> 38

		// String field copied through several struct rebuilds (drop path too).
		{"string-field-through-rebuilds", `struct B { s: string, v: i32 }
function copy_b(x: B): B { return B { s: x.s, v: x.v }; }
function main(): i32 {
	var a: B = B { s: "abcd", v: 3 };
	var b: B = copy_b(a);
	var c: B = copy_b(b);
	return c.s.len() * 10 + c.v;
}`, 4*10 + 3}, // "abcd".len 4 *10 + 3 -> 43
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 28000 {
				t.Fatalf("%s: asm is %d bytes — expected compact IR output, not an AST-fallback bail", tc.name, len(asm))
			}
			progBin := buildBin(t, gcc, dir, "sfr_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
