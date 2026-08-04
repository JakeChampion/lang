package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupStructTypePathCases pin the #4365 TYPE-driven struct-element reclaim: the
// OPTTUP / ARRTUP classes reclaim an Option[<tuple-with-array>] / (<tuple-with-array>)[]
// whose element tuple carries a fresh scalar ARRAY, driven off the TYPE annotation
// (emit_tuple_type_child_drops). This slice extends that type-driven drop to a
// reclaim-STRUCT element position: `Option[(i32, P)]` / `(i32, P)[]` (P sole-owns an
// rc-array field) now deep-drops the struct per iteration — emit_tuple_type_child_drops
// gains a struct arm (__struct_drop_<P> — decs the struct's rc-array fields, balanced by
// the construction alias-inc — then the struct box dec), and admission
// (tuple_field_deep_droppable / tuple_arg_payload_fresh) admits a fresh struct-literal
// position. Both leaked the struct's field buffers + struct box every iteration before
// (native bounds it); the register/wasm backends share the drop via op_tuple_get /
// __struct_drop_<P> / __fern_rc_dec.
//
// SCOPE: this is the reclaim MECHANISM. The element must be consumed SCALAR-only (the
// consuming match reads `g.0` / `xs[i].0`, never the struct): the existing OPTTUP /
// ARRTUP escape checkers (opttup_arm_expr_escapes / arrtup_elem_esc_expr) reject any
// struct-field borrow (`g.1.y`) as an escape, so such a consumer is left leak-safe (not
// credited) — widening those checkers to admit struct-field borrows is a follow-up.
// A WHOLE struct extraction (`keep = g.1`) is likewise leak-safe (never over-released).
var tupStructTypePathCases = []struct {
	name string
	src  string
	want int
}{
	// OPTTUP struct element, scalar-consumed: reclaims the struct + option box each iter.
	{"opttup-struct-scalar-churn", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var o: Option[(i32, P)] = Some((i, P { xs: [i, i + 1], y: i })); match (o) { Some(g) => { acc = (acc + g.0) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[(i32, P)] = Some((j, P { xs: [j, j + 1], y: j })); match (o2) { Some(g) => { acc = (acc + g.0) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ARRTUP struct element, scalar-consumed: reclaims each element's struct + the buffer.
	{"arrtup-struct-scalar-churn", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })]; acc = (acc + xs[0].0) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var ys: (i32, P)[] = [(j, P { xs: [j, j + 1], y: j })]; acc = (acc + ys[0].0) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// OPTTUP struct WHOLE-EXTRACT: `keep = g.1` moves the struct out — leak-safe (the
	// option is left uncredited), never over-released.
	{"opttup-struct-escape-store-safe", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var keep: P = P { xs: [0, 0], y: 0 };
    var i: i32 = 0;
    while (i < 50) {
        var o: Option[(i32, P)] = Some((i, P { xs: [i, i + 1], y: i }));
        match (o) { Some(g) => { keep = g.1; }, None => {} }
        i = i + 1;
    }
    var acc: i32 = keep.xs[0] + keep.y;
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// ARRTUP struct WHOLE-EXTRACT: `keep = xs[0].1` — leak-safe, never over-released.
	{"arrtup-struct-escape-store-safe", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var keep: P = P { xs: [0, 0], y: 0 };
    var i: i32 = 0;
    while (i < 50) {
        var xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })];
        keep = xs[0].1;
        i = i + 1;
    }
    var acc: i32 = keep.xs[0] + keep.y;
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// REGRESSION: the pre-existing plain array-element OPTTUP still reclaims (the shared
	// admission predicates now thread structs, but the array path is unchanged).
	{"opttup-arrtuple-regression", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) { var o: Option[(i32, i32[])] = Some((i, [i, i + 1])); match (o) { Some(g) => { acc = (acc + g.0 + g.1[0]) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[(i32, i32[])] = Some((j, [j, j + 1])); match (o2) { Some(g) => { acc = (acc + g.1[1]) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
}

// TestSelfHostTupStructTypePathReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostTupStructTypePathReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range tupStructTypePathCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
