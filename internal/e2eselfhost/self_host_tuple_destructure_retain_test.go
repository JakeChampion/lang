package e2eselfhost

import (
	"strings"
	"testing"
)

// Dup-at-extract for a tuple destructure (#7682).
//
// `var (a, b) = p` lowered `op_tuple_get` → `op_store_local` → `mark_arr`
// without any retain: the marks make the slot one the scope-exit sweep
// RELEASES, so the binding gave back a reference it never took. Wherever the
// source tuple also carried a reclaim credit the same buffer was decremented
// twice — the first dec frees it, the second underflows into the quarantined
// block. Both oracles were correct; the self-host corrupted memory.
//
// THE CENSUS CANNOT SEE THIS. Every failing row below balanced at
// `allocs == frees`, `live_bytes 0`, and returned an answer that differs from
// the oracles' only through the `__rc_underflow()` guard. So each row gates on
// the EXIT CODE (the guard returns 99) rather than on bytes, per #7432's gate
// note, and re-runs under FERN_SANITIZE=1 where the parent reports
// `use-after-free (touched a quarantined block)` and exits 124.
//
// Every `want` is the answer native AND interp produce for that program.
//
// The scope is `is_leaksafe_array_field` — exactly the set the sweep releases
// with a shallow `__fern_rc_dec`, and exactly the set measured to over-release.
// The deeper-release kinds are pinned unchanged by the last row: a `string[]`
// element LEAKS rather than over-releases, so retaining one would deepen a leak
// instead of closing a double-dec.

func tupleDestructureRetainCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The issue's minimal repro. Fires on a single round; the loop is
			// only here to make a byte rate visible if the polarity ever flips.
			name: "i32arr_bind_read",
			src: `function round(i: i32): i32 { var p: (i32, i32[]) = (i, [i, i + 1]); var (a, b) = p; return a + b.len(); }
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return t % 97;
}`,
			want: 9, balance: true,
		},
		{
			// The 8-byte-stride element kinds ride a different mark
			// (mark_f64arr / mark_i64arr) but the same shallow dec, so they
			// over-released identically and must be covered by the same gate.
			name: "f64arr_bind_read",
			src: `function round(i: i32): i32 { var p: (i32, f64[]) = (i, [1.5, 2.5]); var (a, b) = p; return a + b.len(); }
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return t % 97;
}`,
			want: 9, balance: true,
		},
		{
			name: "i64arr_bind_read",
			src: `function round(i: i32): i32 { var p: (i32, i64[]) = (i, [1i64, 2i64]); var (a, b) = p; return a + b.len(); }
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return t % 97;
}`,
			want: 9, balance: true,
		},
		{
			// A BARE-IDENT element source: the tuple earns no "TUPRC:" literal
			// credit here (tuple_lit_has_rc_child has no ExprIdent arm), yet the
			// construction alias-incs the element — so this over-released for a
			// different reason than the literal row and has to be held
			// separately.
			name: "bare_ident_elem_source",
			src: `function round(i: i32): i32 { var xs: i32[] = [i, i + 1]; var p: (i32, i32[]) = (i, xs); var (a, b) = p; return a + b.len(); }
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return t % 97;
}`,
			want: 9, balance: true,
		},
		{
			// The MOVE path: the destructured element is returned, so the exit
			// sweep elides the slot. This is the row that proves the retain is
			// matched rather than merely cancelling a dec — an unmatched retain
			// here would leak instead of balancing, and the caller's binding is
			// what spends the reference handed out.
			name: "moved_out_by_return",
			src: `function get(i: i32): i32[] { var p: (i32, i32[]) = (i, [i, i + 1]); var (a, b) = p; return b; }
function round(i: i32): i32 { var r: i32[] = get(i); return r.len(); }
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return t % 97;
}`,
			want: 6, balance: true,
		},
		{
			// Handed to a borrowing callee — the third disposal of an extracted
			// element, and the one where a missing retain would strand the
			// argument rather than the binding.
			name: "passed_to_callee",
			src: `function consume(xs: i32[]): i32 { return xs.len(); }
function round(i: i32): i32 { var p: (i32, i32[]) = (i, [i, i + 1]); var (a, b) = p; return consume(b) + a % 3; }
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return t % 97;
}`,
			want: 8, balance: true,
		},
		{
			// UNCHANGED, and the guard on the scope: a `string[]` element is
			// released by the element-walking __fern_str_arr_free, not the
			// shallow dec, so it LEAKS rather than over-releasing and is
			// deliberately outside is_leaksafe_array_field. Retaining it would
			// deepen the leak. Its frees are pinned so a widening into the
			// deeper-release kinds moves a number here rather than passing
			// silently.
			name: "strarr_elem_unchanged_leak",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var p: (i32, string[]) = (i, [w("x"), w("y")]); var (a, b) = p; return a + b.len(); }
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return t % 97;
}`,
			want: 9, balance: false, wantFrees: 100,
		},
	}
}

func TestSelfHostTupleDestructureRetainX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleDestructureRetainCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupdestr_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d — 99 means __rc_underflow() fired, which is this issue's defect: "+
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
			if tc.balance {
				if live != 0 || allocs != frees {
					t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
				}
			} else {
				if live == 0 {
					t.Errorf("%s: %s — a deeper-release element kind came back clean; if the retain was widened to "+
						"cover it, that is its own measured increment and this pin needs re-measuring", tc.name, summary)
				}
				if frees != tc.wantFrees {
					t.Errorf("%s: frees=%d, want %d — a moved count on an unchanged row is a silent widening", tc.name, frees, tc.wantFrees)
				}
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
