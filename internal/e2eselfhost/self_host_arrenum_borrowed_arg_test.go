package e2eselfhost

import (
	"testing"
)

// --- Passing an enum-array local to a BORROWING callee ------------------------
//
// `rd(xs, i)` where `rd` only reads `src.len()`. The arrenum escape gate admits
// exactly one use of the local — `xs.len()` — and read every other position,
// argument positions included, as an escape. So handing the array to a callee
// that touches nothing cost it its element walk, and the exit sweep emitted a
// bare buffer dec where the counted walk was owed: 4 allocs / 2 frees against
// native's 4/4, the whole payload stranded.
//
// The binding source is irrelevant (a literal leaks exactly as a producer call
// does) and so is the loop — this is purely the argument position. That matters
// because the construction matrix's `enum_arr__param` cell was read as a
// question about the CALLEE's param slot; it is not. The leak is the caller's,
// and it is a constant two objects however many times the callee runs.
//
// THE BOX FLAG IS NOT ENOUGH, and asking it would be a double free.
// `borrowable_params_of` already proves "the callee never keeps this param",
// which licenses a box-only release. An element walk is a DEEP free: it frees
// every element box. A callee can be box-borrowable while still handing an
// element out — `H { e: src[0], n: i }` — and the caller's walk would then
// dangle it. That is the same distinction the "TUPB:" tier draws for rc-tuples,
// so this adds the array-of-boxes sibling, "ELB:", on the same registry: flag
// '1' iff the box flag is '1', the param is an array, and no ELEMENT escapes the
// callee under an EMPTY registry (registry-independent, so the interproc
// fixpoint cannot oscillate).
//
// `element_handed_out` is what proves that stronger question load-bearing.
// Dropping the element check and keeping the box flag puts it at self-host
// exit 99 — an rc underflow — while native and interp both exit 25, at a flat
// 1400 allocs / 1400 frees, live_bytes 0. The census reads perfect. Note the
// same edit makes three of the refused cases below LOOK clean (4/4 instead of
// 4/2), so a census-only reading scores the broken compiler higher than this
// one; only the wrong-answer probe separates them.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.
//
// The struct-array twin (`Inner[]`, same shape, same cause) is not fixed here:
// it has its own escape walker and follows as its own slice, the way the
// arrstruct and arrenum halves of every earlier slice did.

const arrenumBorrowDecl = `enum E { A(i32[]), B }
function mkv(i: i32): E[] { var o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
function seed(): i32 { return 7; }
`

// arrenumBorrowMain keeps `keep` genuinely live across a loop. Both halves of
// that matter: a constant producer argument (`mkv(7)`) instead of `mkv(seed())`
// makes the local DEAD and moves its release to a precise box-only site, which
// leaks for an unrelated reason and reads exactly like this bug — the #7364
// const-fold trap, which cost real time here.
func arrenumBorrowMain(src, use string) string {
	return `
function main(): i32 {
    var keep: E[] = ` + src + `;
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + ` + use + `; r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
}

func arrenumBorrowCases() []arrenumShareCase {
	producer := "mkv(seed())"
	literal := "[E.A([seed(), 8])]"
	mk := func(decls, src, use string) string {
		return arrenumBorrowDecl + decls + arrenumBorrowMain(src, use)
	}
	return []arrenumShareCase{
		{
			// The repro: a callee that only reads the header. 4/2 before.
			name: "borrowed_arg",
			src: mk(`function rd(src: E[], i: i32): i32 { return (src.len() + i) % 101; }`,
				producer, "rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			// The same, literal-bound — the binding source is not the axis.
			name: "borrowed_arg_literal",
			src: mk(`function rd(src: E[], i: i32): i32 { return (src.len() + i) % 101; }`,
				literal, "rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			// Control: never passed anywhere. Clean before and after.
			name: "not_passed",
			src:  mk(``, producer, "keep.len() + r"),
			want: 6, balance: true,
		},
		{
			// REFUSED: the callee STORES the param in a struct field, so the
			// caller's walk would free a buffer the holder still owns. This is
			// the construction matrix's own `enum_arr__param` shape, and it
			// stays the leak it was — that cell needs the store to retain,
			// which is a different slice.
			name: "callee_stores_field",
			src: mk(`struct P { f: E[], n: i32 }
function rd(src: E[], i: i32): i32 { var p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }`,
				producer, "rd(keep, r)"),
			want: 6,
		},
		{
			// REFUSED by the BOX flag, before this tier is consulted at all.
			name: "callee_returns_param",
			src: mk(`function rd(src: E[], i: i32): E[] { return src; }`,
				producer, "rd(keep, r).len()"),
			want: 3,
		},
		{
			// REFUSED: an element extraction. Sound to admit in this exact
			// shape (the extracted box dies inside the callee), but the class's
			// element rule is deliberately `len()`-only — widening it needs the
			// arm-binding analysis, and the floor is a leak either way.
			name: "callee_extracts_element",
			src: mk(`function rd(src: E[], i: i32): i32 { var e: E = src[0]; return (match (e) { E.A(xs) => xs.len(), E.B => 0 }) + i; }`,
				producer, "rd(keep, r)"),
			want: 9,
		},
		{
			// REFUSED: the element is pushed into another container.
			name: "callee_appends_element",
			src: mk(`function rd(src: E[], i: i32): i32 { var o: E[] = []; o = o.append(src[0]); return o.len() + i; }`,
				producer, "rd(keep, r)"),
			want: 6,
		},
		{
			// The case the box flag alone gets WRONG, and the reason this tier
			// asks about elements. `grab` never keeps the array — it is
			// box-borrowable — but hands an ELEMENT out inside the struct it
			// returns, and that element outlives the array. Drop the element
			// check and the self-host exits 99 here while native and interp
			// exit 25, with allocs == frees and live_bytes 0 throughout.
			name: "element_handed_out",
			src: `enum E { A(i32[]), B }
struct H { e: E, n: i32 }
function mkv(i: i32): E[] { var o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
function grab(src: E[], i: i32): H { return H { e: src[0], n: i }; }
function f(i: i32): H { var keep: E[] = mkv(i); return grab(keep, i); }
function churn(i: i32): i32 {
    var a: i32[] = [i, i + 1, i + 2, i + 3];
    var b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    var h: H = f(i);
    var junk: i32 = churn(i * 7 + 3);
    var v: i32 = 0;
    match (h.e) { E.A(xs) => { v = xs[0] + xs[1]; }, E.B => { v = 0 - 1; } }
    if (v != i + i + 1) { return 0 - 1; }
    return v % 101;
}
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0; var bad: i32 = 0;
    while (i < 200) { var r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 25,
		},
	}
}

// TestSelfHostArrEnumBorrowedArgX86_64 — an enum-array local handed to a callee
// that only reads its header keeps its element walk, and every callee that could
// let an element outlive the call keeps refusing it.
func TestSelfHostArrEnumBorrowedArgX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrenumBorrowCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrenumborrow_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = the payload "+
					"read back wrong; 139 = it read freed memory)", tc.name, exit, tc.want)
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
		})
	}
}
