package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupStructFieldReclaimCases pin the #4365 struct-field read THROUGH a tuple element of
// an array-of-tuples / option-of-tuple: `xs[i].1.y` / `match (o) { Some(g) => g.1.y }`
// where the tuple's element is a reclaim-struct P (P sole-owning an rc-array field).
//
// Two gaps were closed together:
//  1. IR-lowering: a field access whose object is a tuple-projection of an array element
//     (`xs[i].1.y`, `xs[i].1.xs[0]`) had no tag in expr_tuple_elem_tag (its ExprIndex
//     object hit the `_ => ""` fall-through), so expr_struct_type returned "" and
//     lower_expr's field read called s.fail() — bailing the WHOLE function to the AST
//     fallback (undefined __heap_bump_bytes at link). expr_tuple_elem_tag now resolves an
//     `arr[i].N` element from the (tuple)[] slot's recorded element tuple type (arrarr_elem).
//  2. Reclaim coverage: with the read on the IR path, the ARRTUP / OPTTUP escape checkers
//     (arrtup_elem_esc_expr / opttup_arm_expr_escapes) still flagged a struct-field read
//     `…N.field` as an escape (the tuple element is non-scalar), disqualifying the reclaim →
//     leak. Both walkers now take `structs` and treat a struct SCALAR-field read, an indexed
//     struct ARRAY-field read (`…N.field[j]`), and their `.len()` as borrows; a WHOLE struct
//     element or a bare struct array-field extraction still escapes (leak-safe).
//
// Lowers through op_tuple_get / op_struct_get / __struct_drop_<P> / __fern_rc_dec, all
// backend-complete, so x86-64 / arm64 / wasm share the case table.
var tupStructFieldReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// ARRTUP `(i32, P)[]`, struct scalar-field read `xs[i].1.y` — reclaims (was: AST bail).
	{"arrtupstruct-churn", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })]; acc = (acc + xs[0].0) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var ys: (i32, P)[] = [(j, P { xs: [j, j + 1], y: j })]; acc = (acc + ys[0].1.y) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ARRTUP full borrow set: scalar tuple field, struct scalar field, indexed struct
	// array field, struct array-field .len() — all borrows, still reclaims.
	{"arrtupstruct-borrow-full", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) { var xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })]; acc = (acc + xs[0].0 + xs[0].1.y + xs[0].1.xs[0] + xs[0].1.xs.len()) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var ys: (i32, P)[] = [(j, P { xs: [j, j + 1], y: j })]; acc = (acc + ys[0].1.xs[1]) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ARRTUP escape-store negative: `keep = xs[0].1` extracts the WHOLE struct element —
	// un-credited (leak-safe), never over-released.
	{"arrtupstruct-escape-store-safe", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var keep: P = P { xs: [0, 0], y: 0 };
    var i: i32 = 0;
    while (i < 50) { var xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })]; keep = xs[0].1; i = i + 1; }
    var acc: i32 = keep.xs[0] + keep.y;
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// ARRTUP escape-arrfield negative: `keep = xs[0].1.xs` extracts the struct's ARRAY
	// field bare — un-credited (leak-safe), never over-released.
	{"arrtupstruct-escape-arrfield-safe", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var keep: i32[] = [0, 0];
    var i: i32 = 0;
    while (i < 50) { var xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })]; keep = xs[0].1.xs; i = i + 1; }
    var acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// OPTTUP `Option[(i32, P)]`, struct scalar-field read `g.1.y` — reclaims.
	{"opttupstruct-churn", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { var o: Option[(i32, P)] = Some((i, P { xs: [i, i + 1], y: i })); match (o) { Some(g) => { acc = (acc + g.0) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[(i32, P)] = Some((j, P { xs: [j, j + 1], y: j })); match (o2) { Some(g) => { acc = (acc + g.1.y) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// OPTTUP full borrow set (scalar + struct scalar + indexed struct array + .len()).
	{"opttupstruct-borrow-full", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) { var o: Option[(i32, P)] = Some((i, P { xs: [i, i + 1], y: i })); match (o) { Some(g) => { acc = (acc + g.0 + g.1.y + g.1.xs[0] + g.1.xs.len()) % 251; }, None => {} } i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var o2: Option[(i32, P)] = Some((j, P { xs: [j, j + 1], y: j })); match (o2) { Some(g) => { acc = (acc + g.1.xs[1]) % 251; }, None => {} } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// OPTTUP escape-store negative: `keep = g.1` extracts the whole struct — leak-safe.
	{"opttupstruct-escape-store-safe", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    var keep: P = P { xs: [0, 0], y: 0 };
    var i: i32 = 0;
    while (i < 50) { var o: Option[(i32, P)] = Some((i, P { xs: [i, i + 1], y: i })); match (o) { Some(g) => { keep = g.1; }, None => {} } i = i + 1; }
    var acc: i32 = keep.xs[0] + keep.y;
    if (acc < 0) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// REGRESSION: the plain `(i32, i32[])[]` array-field reclaim (no struct element) still
	// reclaims after the escape checker gained struct-awareness.
	{"arrtup-plain-array-regression", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) { var xs: (i32, i32[])[] = [(i, [i, i + 1])]; acc = (acc + xs[0].0 + xs[0].1[0] + xs[0].1.len()) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var ys: (i32, i32[])[] = [(j, [j, j + 1])]; acc = (acc + ys[0].1[1]) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
}

// TestSelfHostTupStructFieldReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostTupStructFieldReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range tupStructFieldReclaimCases {
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
