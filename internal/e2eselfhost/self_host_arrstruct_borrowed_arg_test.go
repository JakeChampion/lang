package e2eselfhost

import (
	"testing"
)

// --- Passing an array-of-STRUCTS local to a borrowing callee -----------------
//
// The struct-array twin of the arrenum borrowed-argument fix (#7573). Same
// shape, same cause, different escape walker: `rd(xs, i)` where `rd` only reads
// `src.len()` cost the caller's `Inner[]` its element walk. 4 allocs / 2 frees
// against native's 4/4, and identically for a literal-bound source — the binding
// source is not the axis, the argument position is.
//
// WHICH gate refused it was measured, not read. The credit expression has four
// (arrstruct_unsafe_for, arrarr_row_escapes, arrstruct_share_holder_respread,
// arrstruct_elem_payload_escapes); a trace at each said PAYESC — the ELEMENT
// gate — while arrstruct_unsafe_for admitted the very same call. That asymmetry
// is the whole point: the box flag it consults says the callee never keeps the
// ARRAY, which licenses a box-only release, but this class's release walks the
// element boxes. So the element gate consults the "ELB:" tier instead, exactly
// as arrenum_esc_expr now does.
//
// The tier needed no work: arrenum_elem_borrow_flags already gates on the param
// type ending in "[]" and on the escape rule under an empty registry, neither of
// which is enum-specific.
//
// ON THE LOAD-BEARING CHECK, and this is worth stating precisely rather than
// copying the enum slice's claim. Disabling the tier's element check puts the
// arrenum probe at exit 99 (#7573). It does NOT break `element_handed_out` here:
// that case still exits 25 at a clean 1400/1400. The two releases differ —
// emit_enum_variant_drops FREES an element box and zeroes the slot, while this
// class's __struct_drop_<T> DECS it — so a handed-out element survives the
// struct-array walk where it would not survive the enum one.
//
// The check is kept anyway, and the cases below pin the refusals, for two
// reasons: both consumers ask one shared tier, and having them ask DIFFERENT
// questions of it is a divergence hazard rather than a saving; and refusing is
// the leak-safe direction, which is the established floor for this class. If a
// later slice wants the struct-array side widened, it needs its own proof that
// the dec is enough — not this one's silence.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

const arrstructBorrowDecl = `struct Inner { xs: i32[], k: i32 }
struct H2 { e: Inner, n: i32 }
function mkv(i: i32): Inner[] { var o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1], k: i }); return o; }
function seed(): i32 { return 7; }
`

func arrstructBorrowMain(src, use string) string {
	return `
function main(): i32 {
    var keep: Inner[] = ` + src + `;
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + ` + use + `; r = r + 1; }
    t = t + keep.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
}

func arrstructBorrowCases() []arrenumShareCase {
	producer := "mkv(seed())"
	literal := "[Inner { xs: [seed(), 8], k: 7 }]"
	return []arrenumShareCase{
		{
			// The repro: 4/2 before.
			name: "borrowed_arg",
			src: arrstructBorrowDecl + `function rd(src: Inner[], i: i32): i32 { return (src.len() + i) % 101; }` +
				arrstructBorrowMain(producer, "rd(keep, r)"),
			want: 7, balance: true,
		},
		{
			// Literal-bound: the binding source is not the axis. 3/1 before.
			name: "borrowed_arg_literal",
			src: arrstructBorrowDecl + `function rd(src: Inner[], i: i32): i32 { return (src.len() + i) % 101; }` +
				arrstructBorrowMain(literal, "rd(keep, r)"),
			want: 7, balance: true,
		},
		{
			// REFUSED by the BOX flag, before this tier is consulted.
			name: "callee_returns_param",
			src: arrstructBorrowDecl + `function rd(src: Inner[], i: i32): Inner[] { return src; }` +
				arrstructBorrowMain(producer, "rd(keep, r).len()"),
			want: 4,
		},
		{
			// REFUSED: an element extraction. The class's element rule is
			// deliberately len()-only; the floor is a leak either way.
			name: "callee_extracts_element",
			src: arrstructBorrowDecl + `function rd(src: Inner[], i: i32): i32 { var e: Inner = src[0]; return e.k + i; }` +
				arrstructBorrowMain(producer, "rd(keep, r)"),
			want: 25,
		},
		{
			// REFUSED: the element is pushed into another container.
			name: "callee_appends_element",
			src: arrstructBorrowDecl + `function rd(src: Inner[], i: i32): i32 { var o: Inner[] = []; o = o.append(src[0]); return o.len() + i; }` +
				arrstructBorrowMain(producer, "rd(keep, r)"),
			want: 7,
		},
		{
			// The element handed out inside a returned struct — the shape that
			// breaks the ENUM side when the tier's element check is removed.
			// Here it stays correct either way (see the header); the case pins
			// the refusal and reads the payload back after churn regardless.
			name: "element_handed_out",
			src: `struct Inner { xs: i32[], k: i32 }
struct H2 { e: Inner, n: i32 }
function mkv(i: i32): Inner[] { var o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1], k: i }); return o; }
function grab(src: Inner[], i: i32): H2 { return H2 { e: src[0], n: i }; }
function f(i: i32): H2 { var keep: Inner[] = mkv(i); return grab(keep, i); }
function churn(i: i32): i32 {
    var a: i32[] = [i, i + 1, i + 2, i + 3];
    var b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    var h: H2 = f(i);
    var junk: i32 = churn(i * 7 + 3);
    var v: i32 = h.e.xs[0] + h.e.xs[1];
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

// TestSelfHostArrStructBorrowedArgX86_64 — an array-of-structs local handed to a
// callee that only reads its header keeps its element walk, and every callee
// that could let an element outlive the call keeps refusing it.
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
			if frees > allocs {
				t.Errorf("%s: %s — more frees than allocs is a double free", tc.name, summary)
			}
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
