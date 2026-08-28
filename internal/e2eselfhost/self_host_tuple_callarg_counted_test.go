package e2eselfhost

import (
	"strings"
	"testing"
)

// The "TCNT:" counted tier: a tuple param the callee STORES into a struct
// literal it returns. Not a borrow — the callee keeps a reference — but a
// counted one, since the counted-store trio retains it at construction and
// gives it back in the holder's field drop. The caller therefore keeps its own
// deep free, and the two net to one owner.
//
// Admission requires the callee's RESULT to route field reclaim, which is why
// this tier could not exist before the struct-field store landed: nothing gave
// a tuple field a releaser, so `Hold` did not route.
//
// Both escape scans have to admit it, and they learn it differently:
// expr_unsafe_for's call arm already reads the merged "CNT:" key, so the tier
// alone tells it; rctuple_esc_expr's call arm reads only "TUPB:" and needed the
// routing edit. Knocking that edit out returns the matrix row to leak.
//
// Exits confirmed on BOTH oracles (bin/fern -interp and native x86-64).

func tupleCallargCountedCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The matrix cell tuple_mixed__callarg__stored_struct. rc 1 ->
			// callee's construction retain 2 -> caller's struct loop takes the
			// box-only dec 1 -> the rc-tuple loop walks and decs 0.
			name: "stored_struct_balances",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function keepit(t: (i32, i32[])): Hold { return Hold { t: t, n: 1 }; }
function round(i: i32): i32 {
    var keep: (i32, i32[]) = (i, [i, i + 1]);
    var h: Hold = keepit(keep);
    return h.n + keep.0;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 70, balance: true,
		},
		{
			// Two holders through two calls: two retains, so the box is at 3
			// and only the last owner walks the children.
			name: "two_calls_balance",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function keepit(t: (i32, i32[])): Hold { return Hold { t: t, n: 1 }; }
function round(i: i32): i32 {
    var keep: (i32, i32[]) = (i, [i, i + 1]);
    var h1: Hold = keepit(keep);
    var h2: Hold = keepit(keep);
    return h1.n + h2.n + keep.0;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 4, balance: true,
		},
		{
			// Reads through both owners after the call — the holder's field
			// and the source local must see one live box.
			name: "read_through_both_owners",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function keepit(t: (i32, i32[])): Hold { return Hold { t: t, n: 1 }; }
function round(i: i32): i32 {
    var keep: (i32, i32[]) = (i, [i, i + 1]);
    var h: Hold = keepit(keep);
    return h.n + h.t.1.len() + keep.1.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 2, balance: true,
		},
		{
			// THE GUARD. A second callee hands an ELEMENT out (`return t.1`),
			// which is not in arrparam_use_ok's credited vocabulary, so the
			// param loses the tier and the caller keeps its refusal. Crediting
			// it would free the element under the returned reference — the
			// class the "TUPB:" payload tier exists to refuse. Pinned by frees
			// so a silent widening moves a number, not just a verdict.
			name: "element_handout_stays_refused",
			src: `struct Hold { t: (i32, i32[]), n: i32 }
function keepit(t: (i32, i32[])): Hold { return Hold { t: t, n: t.1.len() }; }
function grab(t: (i32, i32[])): i32[] { return t.1; }
function round(i: i32): i32 {
    var keep: (i32, i32[]) = (i, [i, i + 1]);
    var h: Hold = keepit(keep);
    var e: i32[] = grab(keep);
    return h.n + e.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 68, wantFrees: 100,
		},
	}
}

func TestSelfHostTupleCallargCountedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleCallargCountedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupcallcnt_"+tc.name, asm)
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

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "tupcallcnt_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}
