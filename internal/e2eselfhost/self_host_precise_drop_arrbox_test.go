package e2eselfhost

import (
	"testing"
)

// --- The precise drop must not take an array of BOXES down the scalar path ----
//
// `var keep: E[] = mkv(7);` leaked its element boxes where the byte-identical
// `mkv(seed())` did not — 104 allocs / 102 frees against native's 104/104, the
// underflow guard 0 on both. Two programs one token apart (#7610).
//
// The axis is NOT const-folding in general. `mkv(1 + 6)` is clean, `mkv(q)` for
// a `q` bound to 7 is clean, and even `mkv(7)` is clean when `keep` is read
// after the loop. What the bare literal with no later use buys is PRECISE-DROP
// eligibility, and that path was the defect:
//
//	call __fn___fern_arr_dec       box-only
//	xorl %eax, %eax
//	movq %rax, -8(%rbp)            slot ZEROED
//
// The zeroing is the mechanism. The box-only dec frees the buffer and the
// nulled slot then hides the elements from the exit sweep, which finds nothing
// to walk and has no second owner to reach them.
//
// The precise-drop branch reclaims a scalar-element array slot early and
// excluded only `strarr` and `arrarr`, though its own comment states the rule
// that covers all five: a buffer holding POINTERS needs an element walk. The
// exit sweep already deep-frees arrtup, arrstruct and arrenum slots in three
// dedicated loops, each commented "Excluded from the plain is_arr sweep above".
// The exclusion set is exactly that sweep's deep-free set, so the fix asks the
// sweep's own predicates rather than adding a fourth spelling of the question.
//
// THIS SHAPE READS EXACTLY LIKE WHATEVER RC BUG IS BEING WORKED AT THE TIME,
// which is why it costs time and why it is pinned here rather than left to the
// matrix. Two suites warn about it in their headers and both cited #7364 — a
// closed, unrelated defect — so it had no issue of its own until #7610.
//
// Every want was confirmed against the native x86-64 backend, which is clean on
// all six. The struct-array twin was pinned at its LEAKING value when this suite
// was written — its first cause was still open, the "DCNT:" tier being enum-only
// — with a guard that fails if the row starts balancing. That guard fired one
// slice later, when the tier grew its struct arm; the row balances now, and the
// guard stays because the same reasoning applies to whatever is pinned next.

const preciseDropArrBoxDecl = `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
function mkv(i: i32): E[] { var o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
function seed(): i32 { return 7; }
function rd(src: E[], i: i32): i32 { var p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }
`

func preciseDropArrBoxMain(init, tail string) string {
	return `
function main(): i32 {
    ` + init + `
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + rd(keep, r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    ` + tail + `
}`
}

func preciseDropArrBoxCases() []arrenumShareCase {
	mk := func(init, tail string) string {
		return preciseDropArrBoxDecl + preciseDropArrBoxMain(init, tail)
	}
	ret := "return t % 97;"
	return []arrenumShareCase{
		{
			// The repro: bare integer literal, no use of `keep` after the loop.
			// 104/102 before, 104/104 now.
			name: "literal_arg_precise_drop",
			src:  mk("var keep: E[] = mkv(7);", ret),
			want: 6, balance: true,
		},
		{
			// A CALL argument: never precise-dropped, clean before and after.
			// This is the control that proves the literal is the axis.
			name: "call_arg_unchanged",
			src:  mk("var keep: E[] = mkv(seed());", ret),
			want: 6, balance: true,
		},
		{
			// A constant EXPRESSION, not a bare literal — also clean before, so
			// "const-fold" is the wrong name for the axis.
			name: "const_expr_arg_unchanged",
			src:  mk("var keep: E[] = mkv(1 + 6);", ret),
			want: 6, balance: true,
		},
		{
			// The literal routed through a local: clean before.
			name: "var_arg_unchanged",
			src:  mk("var q: i32 = 7; var keep: E[] = mkv(q);", ret),
			want: 6, balance: true,
		},
		{
			// The same literal, but `keep` is live past the loop, so it is not
			// precise-drop eligible: clean before. Isolates eligibility from
			// the argument shape.
			name: "literal_arg_kept_live",
			src:  mk("var keep: E[] = mkv(7);", "return (t + keep.len()) % 97;"),
			want: 7, balance: true,
		},
		{
			// The struct-array twin. Pinned LEAKING when this suite was written,
			// because `struct_arr__param` had no counted-param tier and the
			// precise-drop fix alone could not close it; the stale-pin guard
			// below then caught it the moment the "DCNT:" tier grew its struct
			// arm, which is what that guard exists for. Balances now.
			name: "struct_array_twin",
			src: `struct Inner { xs: i32[], k: i32 }
struct P { f: Inner[], n: i32 }
function mkv(i: i32): Inner[] { var o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1], k: i }); return o; }
function rd(src: Inner[], i: i32): i32 { var p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }
` + preciseDropArrBoxMain("var keep: Inner[] = mkv(7);", ret),
			want: 6, balance: true,
		},
	}
}

// TestSelfHostPreciseDropArrBoxX86_64 — an array-of-boxes local that qualifies
// for the precise drop keeps its element walk instead of being box-only dec'd
// and zeroed out from under the exit sweep.
func TestSelfHostPreciseDropArrBoxX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range preciseDropArrBoxCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "precisedroparrbox_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = it read freed memory)",
					tc.name, exit, tc.want)
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
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
			if !tc.balance && live == 0 && allocs == frees {
				t.Errorf("%s: %s — pinned as LEAKING; if this now balances the pin "+
					"is stale and the row belongs to whatever closed it", tc.name, summary)
			}
		})
	}
}
