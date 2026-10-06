package e2eselfhost

import (
	"testing"
)

// --- Passing a struct-array local to a BORROWING callee -----------------------
//
// A struct-array local handed to a callee that only reads its header must keep
// its element walk, and a callee that lets an element outlive the call (in a
// returned struct, bare, as a field, appended elsewhere) must not have it freed
// under the caller. Every row balances at live_bytes 0 on the typed lowering;
// the exit codes guard the values read back after churn.
//
// Every want was confirmed against bin/fern -interp and the native x86-64
// backend, never read off the self-host run.

type arrstructBorrowCase struct {
	name string
	src  string
	want int
}

const arrstructBorrowDecl = `struct Inner { xs: i32[] }
function mkv(i: i32): Inner[] { let o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1] }); return o; }
function seed(): i32 { return 7; }
`

// arrstructBorrowMain keeps `keep` genuinely live across a loop. A constant
// producer argument (`mkv(7)`) instead of `mkv(seed())` makes the local DEAD and
// moves its release to a precise box-only site, which leaks for an unrelated
// reason and reads exactly like this bug — the #7364 const-fold trap.
func arrstructBorrowMain(src, use string) string {
	return `
function main(): i32 {
    let keep: Inner[] = ` + src + `;
    let t: i32 = 0; let r: i32 = 0;
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
function mkv(i: i32): Inner[] { let o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1] }); return o; }
` + callee + `
function f(i: i32): ` + ret + ` { let keep: Inner[] = mkv(i); return grab(keep, i); }
function churn(i: i32): i32 {
    let a: i32[] = [i, i + 1, i + 2, i + 3];
    let b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    let g: ` + ret + ` = f(i);
    let junk: i32 = churn(i * 7 + 3);
    let v: i32 = ` + read + `;
    if (v != i + i + 1) { return 0 - 1; }
    return v % 101;
}
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`
}

func arrstructBorrowCases() []arrstructBorrowCase {
	producer := "mkv(seed())"
	literal := "[Inner { xs: id([seed(), 8]) }]"
	mk := func(decls, src, use string) string {
		return arrstructBorrowDecl + decls + arrstructBorrowMain(src, use)
	}
	readOnly := `function rd(src: Inner[], i: i32): i32 { return (src.len() + i) % 101; }`
	return []arrstructBorrowCase{
		{
			// The repro: a callee that only reads the header. 4/2 before.
			name: "borrowed_arg",
			src:  mk(readOnly, producer, "rd(keep, r)"),
			want: 6,
		},
		{
			// The same, literal-bound — the binding source is not the axis. The
			// payload goes through id so it is built on the heap rather than
			// placed as a constant.
			name: "borrowed_arg_literal",
			src:  mk(readOnly+"\nfunction id(xs: i32[]): i32[] { return xs; }", literal, "rd(keep, r)"),
			want: 6,
		},
		{
			// Control: never passed anywhere. Clean before and after.
			name: "not_passed",
			src:  mk(``, producer, "(keep.len() + r) % 101"),
			want: 6,
		},
		{
			// ADMITTED by the extract-then-die widening: `let e = src[0]` with a
			// confined local (the same body_unsafe_for_match_borrow +
			// param_match_binding_escapes pair the box flag trusts for a param
			// name) keeps the "ELB:" flag, because the extracted box dies inside
			// the callee before the caller's element walk frees it. This is NOT
			// the box-flag weakening this row used to guard against — the four
			// handout witnesses below still refuse, and the widening's own
			// grant is what the balance pins now.
			name: "callee_extracts_element",
			src: mk(`function rd(src: Inner[], i: i32): i32 { let e: Inner = src[0]; return e.xs.len() + i; }`,
				producer, "rd(keep, r)"),
			want: 9,
		},
		{
			// The callee hands the array back, so the caller does not sole-
			// own it while the result lives.
			name: "callee_returns_param",
			src: mk(`function rd(src: Inner[], i: i32): Inner[] { return src; }`,
				producer, "rd(keep, r).len()"),
			want: 3,
		},
		{
			// ADMITTED, by a different tier than this suite's — and the row this
			// suite's guard was watching for. "ELB:" refuses it (the callee
			// keeps a reference, so it is not element-safe), but the reference
			// is a COUNTED store, which param_counted_of's "DCNT:" tier proves
			// and borrow_reg_with_counted publishes here under "CNT:".
			//
			// The guard warned that a balance here could instead mean the BOX
			// FLAG had been weakened, and named the check. It was not: every
			// handout shape below still refuses (element_handed_out_in_struct,
			// _bare, element_field_handed_out, element_appended_elsewhere), so
			// does callee_extracts_element, and TestSelfHostStage2FixpointArm64
			// is green with no gen2 segfault. Only the counted-STORE shape moved.
			name: "callee_stores_field",
			src: mk(`struct P { f: Inner[], n: i32 }
function rd(src: Inner[], i: i32): i32 { let p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }`,
				producer, "rd(keep, r)"),
			want: 6,
		},
		// The four handout shapes: something from the array outlives the call.
		// Each reads its payload back after churn, so a release under the live
		// reference shows as exit 100 or 139.
		{
			name: "element_handed_out_in_struct",
			src: arrstructHandout(
				`function grab(src: Inner[], i: i32): H { return H { e: src[0], n: i }; }`,
				"H", "g.e.xs[0] + g.e.xs[1]"),
			want: 25,
		},
		{
			name: "element_handed_out_bare",
			src: arrstructHandout(
				`function grab(src: Inner[], i: i32): Inner { return src[0]; }`,
				"Inner", "g.xs[0] + g.xs[1]"),
			want: 25,
		},
		{
			name: "element_field_handed_out",
			src: arrstructHandout(
				`function grab(src: Inner[], i: i32): i32[] { return src[0].xs; }`,
				"i32[]", "g[0] + g[1]"),
			want: 25,
		},
		{
			name: "element_appended_elsewhere",
			src: arrstructHandout(
				`function grab(src: Inner[], i: i32): Inner[] { let o: Inner[] = []; o = o.append(src[0]); return o; }`,
				"Inner[]", "g[0].xs[0] + g[0].xs[1]"),
			want: 25,
		},
	}
}

// TestSelfHostArrStructBorrowedArgX86_64 — a struct-array local handed to a
// callee, or letting an element outlive a call, reclaims everything and reads
// its values back intact.
func TestSelfHostArrStructBorrowedArgX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
