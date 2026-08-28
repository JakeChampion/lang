package e2eselfhost

import (
	"strings"
	"testing"
)

// The counted tuple struct-field store: `S { t: k, … }` where `t` is a direct
// deep-droppable tuple field. Three halves, all gated on
// struct_has_deep_tuple_field so none can widen without the others (#7253):
//
//	retain  — lower_expr_struct_lit's tuple arm incs a non-literal field value;
//	release — emit_struct_tuple_field_drops walks the children under
//	          __fern_rc_is_unique and decs the box once per owner;
//	credit  — rctuple_counted_field_share forgives the store in the TUPRC gate,
//	          so the SOURCE keeps its own deep free.
//
// two_holders_balances is why all three landed together. With the release half
// alone the matrix row still read clean and the sanitizer stayed silent — but
// the box was MOVED, so the second holder's is_unique read a header the first
// holder had already freed, and the totals only levelled because that read
// happened to return false. Reading the emitted asm is what caught it: two drop
// sequences, no inc. A future knockout of the retain reproduces it, so this case
// must exercise two live holders of one box, not just one.
//
// Exits confirmed on BOTH oracles (bin/fern -interp and native x86-64), never
// read off the self-host under test.

func tupleStructFieldStoreCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The matrix cell tuple_mixed__structfield__local_store: one
			// holder, source read after the store. rc 1 -> store inc 2 ->
			// source's deep free 1 -> the holder's field drop 0.
			name: "local_store_balances",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function round(i: i32): i32 {
    var k: (i32, i32[]) = (i, [i, i + 1]);
    var h: Hold = Hold { t: k, n: i };
    return h.n + k.0;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 23, balance: true,
		},
		{
			// TWO holders of one box — the shape that exposed the move. Only
			// the owner that finds rc 1 walks the children; all three decs
			// land, so the box reaches zero exactly once and no holder ever
			// reads a freed header.
			name: "two_holders_balances",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function round(i: i32): i32 {
    var k: (i32, i32[]) = (i, [i, i + 1]);
    var h1: Hold = Hold { t: k, n: i };
    var h2: Hold = Hold { t: k, n: i + 1 };
    return h1.n + h2.n + k.0;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 10, balance: true,
		},
		{
			// Reads through BOTH owners after the store: the field read
			// `h.t.1` and the source read `k.1` must see the same live box.
			name: "read_through_both_owners",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function round(i: i32): i32 {
    var k: (i32, i32[]) = (i, [i, i + 1]);
    var h: Hold = Hold { t: k, n: i };
    var a: i32 = h.t.1.len();
    var b: i32 = k.1.len();
    return a + b + h.n;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 38, balance: true,
		},
		{
			// A FRESH tuple literal in the field takes no inc — the struct
			// sole-owns it and the field drop reclaims it outright. The arm
			// that must NOT fire; an inc here would pin the box forever.
			name: "fresh_literal_field_balances",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function round(i: i32): i32 {
    var h: Hold = Hold { t: (i, [i, i + 1]), n: i };
    return h.n + h.t.0;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 23, balance: true,
		},
		{
			// The struct ESCAPES the frame that built it. The literal is the
			// return value, so the store is a MOVE (moved_ident_at) and
			// rctuple_counted_field_share refuses it: the source must not be
			// credited a free for a box whose ownership left the frame — the
			// shape that once segfaulted the arm64 stage-2 for the array
			// sibling. Balances because the caller's own drop does the work.
			name: "returned_holder_balances",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function mk(i: i32): Hold {
    var k: (i32, i32[]) = (i, [i, i + 1]);
    return Hold { t: k, n: i };
}
function round(i: i32): i32 {
    var h: Hold = mk(i);
    return h.n + h.t.0;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 23, balance: true,
		},
		{
			// Adversarial, stays REFUSED: the holders are built inside an
			// ARRAY literal, which rctuple_counted_field_share never reaches
			// (it matches a struct literal in a value position, not one
			// nested in a container). The source keeps its escape verdict and
			// the tuple leaks — conservative, and pinned by frees so a silent
			// widening moves a number rather than only a verdict.
			name: "array_of_holders_stays_refused",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function round(i: i32): i32 {
    var k: (i32, i32[]) = (i, [i, i + 1]);
    var hs: Hold[] = [Hold { t: k, n: i }, Hold { t: k, n: i + 1 }];
    return hs.len() + hs[0].n;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 4, wantFrees: 300,
		},
	}
}

func TestSelfHostTupleStructFieldStoreX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleStructFieldStoreCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupsfs_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = read freed memory)", tc.name, exit, tc.want)
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
			} else if frees != tc.wantFrees {
				t.Errorf("%s: %s — refused row's frees moved (want %d)", tc.name, summary, tc.wantFrees)
			}

			// Sanitize leg. Free-safety is what this pair can get wrong: a
			// retain that does not fire leaves the second owner reading a
			// freed header, which is a use-after-free even when the totals
			// happen to level.
			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "tupsfs_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}

			if tc.balance {
				// Nothing here is plan-routed, so FERN_SELFHOST_RC_PLAN=0
				// must change nothing.
				offAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1", "FERN_SELFHOST_RC_PLAN=0"})
				offBin := buildBin(t, gcc, dir, "tupsfs_off_"+tc.name, offAsm)
				offErr, offExit := hevRun(t, runner, offBin)
				if offExit != tc.want {
					t.Fatalf("%s plan-off exited %d, want %d", tc.name, offExit, tc.want)
				}
				var oa, of, ol int64
				if _, err := fmtSscan(leakSummaryLine(offErr), &oa, &of, &ol); err != nil {
					t.Fatalf("%s plan-off: parse: %v", tc.name, err)
				}
				if ol != 0 || oa != of {
					t.Errorf("%s plan-off: %s — must balance without the plan", tc.name, leakSummaryLine(offErr))
				}
			}
		})
	}
}
