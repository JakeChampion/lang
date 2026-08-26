package e2eselfhost

import (
	"testing"
)

// --- Passing a struct-array local to a BORROWING callee -----------------------
//
// The struct-array twin of self_host_arrenum_borrowed_arg_test.go, and the same
// bug: `rd(keep, r)` where `rd` reads nothing but `src.len()` cost the caller's
// array its whole element walk. 4 allocs / 2 frees against native's 4/4, the
// payload stranded. The argument position is the whole axis — a literal-bound
// local leaks exactly as a producer-bound one does, and the loop is incidental.
//
// arrstruct_elem_esc_expr is where it lives, not arrstruct_unsafe_for: the
// latter already admits the argument through expr_unsafe_for's borrowable-param
// arm, and the two run as separate gates on the same credit, so the element
// walker refusing was enough to sink it. Its ExprCall arm fell through to a
// generic arg loop whose bare-ident leaf reads any mention of the local as an
// escape.
//
// LIKE THE ENUM TWIN, THIS ASKS "ELB:" AND NOT THE PLAIN BOX FLAG — and the
// reason it has to is the one thing this suite CANNOT prove. The box flag says
// the callee never keeps the BOX, which licenses a box-only release; this
// class's release walks the ELEMENTS. Reading emit_arrstruct_deep_free argues
// the weaker flag should do: its per-element step is an rc-GATED field drop
// (unique-only) plus a plain __fern_rc_dec, and a dec cannot over-free a box a
// second owner holds a COUNTED reference to. That last word is the gap. An
// element handed out UNCOUNTED has no such reference and the box flag says
// nothing about it.
//
// Every handout shape below — an element in a returned struct, a bare returned
// element, a returned element FIELD, an element appended into another array —
// balances at live_bytes 0 on the box flag and reads its payload back correctly
// against both oracles. All four look like proof that the weaker question is
// safe. They are not: on the box flag TestSelfHostStage2FixpointArm64 segfaults
// gen2 in sort_wider, float_math and process_assertions, where it is green on
// main and green with "ELB:". The compiler's own sources hold a shape no probe
// here reproduces, so the fixpoint — the instrument the arrenum slice recorded
// as BLIND to its bug, because the compiler had no enum arrays in that shape —
// is the only one that catches this one.
//
// So the four handout cases are kept as REFUSAL witnesses, asserted to leak.
// Each balancing again means the gate has been weakened back to the box flag,
// and this suite will say so before the fixpoint has to.
//
// Every want was confirmed against bin/fern -interp and the native x86-64
// backend, never read off the self-host run.

// arrstructBorrowCase is arrenumShareCase plus the refusal witness: `leaks`
// asserts allocs != frees, so a case that starts balancing fails here rather
// than passing quietly.
type arrstructBorrowCase struct {
	name    string
	src     string
	want    int
	balance bool // assert allocs == frees at live_bytes 0
	leaks   bool // assert allocs != frees — the gate must still be refusing
}

const arrstructBorrowDecl = `struct Inner { xs: i32[] }
function mkv(i: i32): Inner[] { var o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1] }); return o; }
function seed(): i32 { return 7; }
`

// arrstructBorrowMain keeps `keep` genuinely live across a loop. A constant
// producer argument (`mkv(7)`) instead of `mkv(seed())` makes the local DEAD and
// moves its release to a precise box-only site, which leaks for an unrelated
// reason and reads exactly like this bug — the #7364 const-fold trap.
func arrstructBorrowMain(src, use string) string {
	return `
function main(): i32 {
    var keep: Inner[] = ` + src + `;
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + ` + use + `; r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
}

// arrstructHandout wraps a callee that lets something from the array outlive the
// call, with allocation churn between the handout and the read so a freed box is
// reused before the payload is checked. `ret` is the callee's return type, `read`
// pulls the two seeded values back out of the returned `g`.
func arrstructHandout(callee, ret, read string) string {
	return `struct Inner { xs: i32[] }
struct H { e: Inner, n: i32 }
function mkv(i: i32): Inner[] { var o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1] }); return o; }
` + callee + `
function f(i: i32): ` + ret + ` { var keep: Inner[] = mkv(i); return grab(keep, i); }
function churn(i: i32): i32 {
    var a: i32[] = [i, i + 1, i + 2, i + 3];
    var b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    var g: ` + ret + ` = f(i);
    var junk: i32 = churn(i * 7 + 3);
    var v: i32 = ` + read + `;
    if (v != i + i + 1) { return 0 - 1; }
    return v % 101;
}
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0; var bad: i32 = 0;
    while (i < 200) { var r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`
}

func arrstructBorrowCases() []arrstructBorrowCase {
	producer := "mkv(seed())"
	literal := "[Inner { xs: [seed(), 8] }]"
	mk := func(decls, src, use string) string {
		return arrstructBorrowDecl + decls + arrstructBorrowMain(src, use)
	}
	readOnly := `function rd(src: Inner[], i: i32): i32 { return (src.len() + i) % 101; }`
	return []arrstructBorrowCase{
		{
			// The repro: a callee that only reads the header. 4/2 before.
			name: "borrowed_arg",
			src:  mk(readOnly, producer, "rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			// The same, literal-bound — the binding source is not the axis.
			name: "borrowed_arg_literal",
			src:  mk(readOnly, literal, "rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			// Control: never passed anywhere. Clean before and after.
			name: "not_passed",
			src:  mk(``, producer, "(keep.len() + r) % 101"),
			want: 6, balance: true,
		},
		{
			// REFUSED: an element extraction. Sound to admit in this exact shape
			// — the extracted box dies inside the callee — but "ELB:" is the
			// class's own len()-only element rule and does not model that,
			// exactly as on the enum side. The floor is a leak either way.
			name: "callee_extracts_element",
			src: mk(`function rd(src: Inner[], i: i32): i32 { var e: Inner = src[0]; return e.xs.len() + i; }`,
				producer, "rd(keep, r)"),
			want: 9, leaks: true,
		},
		{
			// REFUSED by the BOX flag itself, before the element tier is
			// consulted: the callee hands the ARRAY back, so the caller does not
			// sole-own it.
			name: "callee_returns_param",
			src: mk(`function rd(src: Inner[], i: i32): Inner[] { return src; }`,
				producer, "rd(keep, r).len()"),
			want: 3, leaks: true,
		},
		{
			// REFUSED: the callee STORES the param in a struct field, so the
			// caller's walk would strand a buffer the holder still owns. This is
			// the construction matrix's own `struct_arr__param` cell and stays
			// the leak it was — that cell needs the STORE to retain, a different
			// slice from this one.
			name: "callee_stores_field",
			src: mk(`struct P { f: Inner[], n: i32 }
function rd(src: Inner[], i: i32): i32 { var p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }`,
				producer, "rd(keep, r)"),
			want: 6, leaks: true,
		},
		// The four handout shapes: something from the array outlives the call
		// while the callee stays box-borrowable. Each is where the weaker
		// question would admit and this one refuses. They all read their payload
		// back correctly and they all still leak — that leak is the assertion.
		// See the header: on the box flag every one of them BALANCES and the
		// fixpoint segfaults instead.
		{
			name: "element_handed_out_in_struct",
			src: arrstructHandout(
				`function grab(src: Inner[], i: i32): H { return H { e: src[0], n: i }; }`,
				"H", "g.e.xs[0] + g.e.xs[1]"),
			want: 25, leaks: true,
		},
		{
			name: "element_handed_out_bare",
			src: arrstructHandout(
				`function grab(src: Inner[], i: i32): Inner { return src[0]; }`,
				"Inner", "g.xs[0] + g.xs[1]"),
			want: 25, leaks: true,
		},
		{
			name: "element_field_handed_out",
			src: arrstructHandout(
				`function grab(src: Inner[], i: i32): i32[] { return src[0].xs; }`,
				"i32[]", "g[0] + g[1]"),
			want: 25, leaks: true,
		},
		{
			name: "element_appended_elsewhere",
			src: arrstructHandout(
				`function grab(src: Inner[], i: i32): Inner[] { var o: Inner[] = []; o = o.append(src[0]); return o; }`,
				"Inner[]", "g[0].xs[0] + g[0].xs[1]"),
			want: 25, leaks: true,
		},
	}
}

// TestSelfHostArrStructBorrowedArgX86_64 — a struct-array local handed to a
// callee that only reads its header keeps its element walk, and every shape that
// could let an element outlive the call still refuses it.
func TestSelfHostArrStructBorrowedArgX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrstructBorrowCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrstructborrow_"+tc.name, asm)
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
			if tc.leaks && allocs == frees {
				t.Errorf("%s: %s — balances, so the gate stopped refusing this "+
					"shape. That is the box-flag weakening; check "+
					"TestSelfHostStage2FixpointArm64 for gen2 segfaults", tc.name, summary)
			}
		})
	}
}
