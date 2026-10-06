package e2ecompiler

import (
	"strings"
	"testing"
)

// Dup-at-extract for a tuple destructure (#7682).
//
// `let (a, b) = p` lowered `op_tuple_get` → `op_store_local` → `mark_arr`
// without any retain: the marks make the slot one the scope-exit sweep
// RELEASES, so the binding gave back a reference it never took. Wherever the
// source tuple also carried a reclaim credit the same buffer was decremented
// twice — the first dec frees it, the second underflows into the quarantined
// block. Both oracles were correct; the self-host corrupted memory.
//
// THE CENSUS CANNOT SEE THIS. Every failing row below balanced at
// `allocs == frees`, `live_bytes 0`, and returned an answer that differs from
// the oracles' only through the `__rc_underflow_count()` guard. So each row gates on
// the EXIT CODE (the guard returns 99) rather than on bytes, per #7432's gate
// note, and re-runs under FERN_SANITIZE=1 where the parent reports
// `use-after-free (touched a quarantined block)` and exits 124.
//
// Every `want` is the answer native AND interp produce for that program.
//
// The scope is every element kind a tuple's own sweep can release:
// `is_leaksafe_array_field` (shallow `__fern_rc_dec`) and `string[]`, whose
// deep `__fern_str_arr_free` is rc-gated so the binding's shallow dec runs
// first and the element walk happens at rc 1. The `string[]` rows below all
// over-released before the retain reached them, including on element forms the
// admission has taken since long before it learned the fresh-string registry.
// A struct / enum array element stays out: no tuple position of that type is
// sweep-credited at all, so it can only leak.

func tupleDestructureRetainCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The issue's minimal repro. Fires on a single round; the loop is
			// only here to make a byte rate visible if the polarity ever flips.
			name: "i32arr_bind_read",
			src: `function round(i: i32): i32 { let p: (i32, i32[]) = (i, [i, i + 1]); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			// The 8-byte-stride element kinds use a different mark
			// (mark_f64arr / mark_i64arr) but the same shallow dec, so they
			// over-released identically and must be covered by the same gate.
			name: "f64arr_bind_read",
			src: `function round(i: i32): i32 { let p: (i32, f64[]) = (i, [1.5, 2.5]); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			name: "i64arr_bind_read",
			src: `function round(i: i32): i32 { let p: (i32, i64[]) = (i, [1i64, 2i64]); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			// A BARE-IDENT element source: the tuple earns no "TUPRC:" literal
			// credit here (tuple_lit_has_rc_child has no ExprIdent arm), yet the
			// construction alias-incs the element — so this over-released for a
			// different reason than the literal row and has to be held
			// separately.
			name: "bare_ident_elem_source",
			src: `function round(i: i32): i32 { let xs: i32[] = [i, i + 1]; let p: (i32, i32[]) = (i, xs); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			// The MOVE path: the destructured element is returned, so the exit
			// sweep elides the slot. This is the row that proves the retain is
			// matched rather than merely cancelling a dec — an unmatched retain
			// here would leak instead of balancing, and the caller's binding is
			// what spends the reference handed out.
			name: "moved_out_by_return",
			src: `function get(i: i32): i32[] { let p: (i32, i32[]) = (i, [i, i + 1]); let (a, b) = p; return b; }
function round(i: i32): i32 { let r: i32[] = get(i); return r.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 6,
		},
		{
			// Handed to a borrowing callee — the third disposal of an extracted
			// element, and the one where a missing retain would strand the
			// argument rather than the binding.
			name: "passed_to_callee",
			src: `function consume(xs: i32[]): i32 { return xs.len(); }
function round(i: i32): i32 { let p: (i32, i32[]) = (i, [i, i + 1]); let (a, b) = p; return consume(b) + a % 3; }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 8,
		},
		{
			// A `string[]` element: the tuple's sweep releases this position
			// with the element-walking __fern_str_arr_free, so the extract owes
			// it a retain exactly as the shallow-dec kinds do. The element
			// sources are registered fresh-string producers, the form the
			// sweep credit reaches only once its admission consults that
			// registry — this row is where the widening surfaced.
			name: "strarr_elem_bind_read",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let p: (i32, string[]) = (i, [w("x"), w("y")]); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			// STRING-LITERAL elements: the syntactic admission the sweep credit
			// has always had, so this shape over-released long before the
			// registry widened it. Held separately for that reason — a fix
			// scoped to the registry form would leave this one corrupting.
			name: "strarr_literal_elems",
			src: `function round(i: i32): i32 { let p: (i32, string[]) = (i, ["x", "y"]); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			// A BARE-IDENT string[] source — the string[] twin of the row
			// above it: the construction alias-incs the buffer, and the
			// extract took no reference of its own, so this over-released on
			// its own account.
			name: "strarr_bare_ident_elem_source",
			src: `function round(i: i32): i32 { let xs: string[] = ["x", "y"]; let p: (i32, string[]) = (i, xs); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			// Handed to a borrowing callee, the string[] twin of
			// passed_to_callee: the callee borrows, so the binding still
			// spends the reference the extract took.
			name: "strarr_passed_to_callee",
			src: `function w(a: string): string { return a + "!"; }
function consume(xs: string[]): i32 { return xs.len(); }
function round(i: i32): i32 { let p: (i32, string[]) = (i, [w("x"), w("y")]); let (a, b) = p; return consume(b) + a % 3; }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 8,
		},
		{
			// The MOVE path: `b` is returned, so the caller owns the
			// string[] and its element boxes; the exit guards the values
			// read back.
			name: "strarr_moved_out_by_return",
			src: `function w(a: string): string { return a + "!"; }
function get(i: i32): string[] { let p: (i32, string[]) = (i, [w("x"), w("y")]); let (a, b) = p; return b; }
function round(i: i32): i32 { let r: string[] = get(i); return r.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 6,
		},
	}
}

func TestSelfHostTupleDestructureRetainX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleDestructureRetainCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupdestr_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d — 99 means __rc_underflow_count() fired, which is this issue's defect: "+
					"the destructured element was released once more than it was retained. The census below is "+
					"balanced either way, so the exit code is the whole signal.\n%s",
					tc.name, exit, tc.want, leakSummaryLine(stderr))
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "tupdestr_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "use-after-free") || strings.Contains(sanErr, "rc over-release") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}
