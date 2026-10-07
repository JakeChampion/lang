package e2ecompiler

import (
	"strings"
	"testing"
)

// --- An unmatched Option[string] / Result[string, _] (#6360) -----------------
//
// A local that is NEITHER reassigned NOR consumed by a match must still be
// released. Bytes left behind over 100 rounds, with the payload varied and
// everything else held fixed:
//
//	Option[i32[]]          0
//	Result[i32[], i32]     0
//	Result[i32[], string]  6400     Err strings, still open — see SCOPE
//	Option[string]         22400    this file
//
// ×2.0 per doubling. The string release is `__fern_str_free` rather than the
// flat rc-dec: a string box carries a separate data buffer and a different
// block class, so the array dec would free it wrongly.
//
// FRESHNESS IS ESSENTIAL HERE. An Option stores its payload uncounted and a
// string assignment is a borrow, so an aliased payload would be released under
// a live reference. Admission therefore demands a literal or a
// syntactically-fresh producer.
//
// THE EXIT SWEEP ALONE IS NOT ENOUGH. A loop-declared `let v` re-stores to the
// SAME slot each iteration, so a function-exit sweep releases only the final
// value and every earlier iteration still leaks (22400 improves only to
// 18400). The store is where the previous value has to go.
//
// SCOPE: `Result[i32[], string]` strands its Err strings (6400) and stays open:
// an Err payload is never reached — stranded, not dangled — and releasing it
// needs its own whole-body verdict.

func TestSelfHostUnmatchedOptStrReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		want := interpExit(t, interpBin, src)
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != want {
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — the payload free "+
				"reached a live string", name, exit, want)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary", name)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("%s: parse %q: %v", name, summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
		}
		return allocs, frees, live
	}

	// Reclaimed to an exact balance.
	for _, tc := range []struct{ name, src string }{
		{
			// The main row: 22400 -> 0.
			// Loop-declared, so it also covers the per-iteration store path that the
			// exit sweep alone cannot reach.
			name: "option_string_from_a_call",
			src: `function mk(i: i32): Option[string] {
    if (i % 3 == 0) { return None; }
    return Some("v" + "x");
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = mk(i);
        acc = acc + i;
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
		},
		{
			// Inline constructor rather than a call — admitted on the payload being a
			// fresh concat, not on registry membership.
			name: "option_string_inline_concat",
			src: `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = Some("v" + "x");
        acc = acc + i;
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
		},
		{
			// A bare literal payload: `__fern_str_free`'s heap-base guard no-ops on
			// .rodata data, so the box is released and the data is left alone.
			name: "option_string_literal",
			src: `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = Some("literal");
        acc = acc + i;
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
		},
		{
			// `Result[string, i32]` — success payload string, scalar Err.
			name: "result_string_ok_scalar_err",
			src: `function mk(i: i32): Result[string, i32] {
    if (i % 3 == 0) { return Err(i); }
    return Ok("v" + "x");
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[string, i32] = mk(i);
        acc = acc + i;
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
		},
		{
			// The ARRAY row #6463 landed, kept here as the control: this class must
			// not disturb it, and its credit is a different tag.
			name: "option_array_control",
			src: `function mk(i: i32): Option[i32[]] {
    if (i % 3 == 0) { return None; }
    return Some([i, i + 1]);
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[i32[]] = mk(i);
        acc = acc + i;
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, "uos_"+tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance. The "+
					"loop-declared shapes leak every iteration but the last if the store-side "+
					"reclaim is lost, which reads as partial progress rather than a regression",
					allocs, frees, live)
			}
		})
	}

	// Each keeps a live reference a premature release would dangle; the exit
	// agreement is the guard, and each balances on the typed lowering.
	// The first two churn same-shaped strings before the aliased read, because
	// otherwise the freed box is not recycled and the probe exits correctly with the
	// bug present.
	for _, tc := range []struct{ name, src string }{
		{
			// Producer hands back its ARGUMENT — flagged "a", so the payload aliases
			// the caller's box.
			name: "aliased_producer_payload",
			src: `function wrap(s: string): Option[string] { return Some(s); }
function round(r: i32): i32 {
    let shared: string = "ab" + "cd";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = wrap(shared);
        acc = acc + i;
        i = i + 1;
    }
    let junk: string = "";
    let c: i32 = 0;
    while (c < 6) { junk = "zz" + "zz"; c = c + 1; }
    let sum: i32 = 0;
    let k: i32 = 0;
    while (k < shared.len()) { sum = sum + (shared[k] as i32); k = k + 1; }
    return acc + sum + junk.len() + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 251;
}`,
		},
		{
			// Inline ctor over a bare local — the same alias, reached without a
			// producer at all.
			name: "inline_ctor_over_a_bare_local",
			src: `function round(r: i32): i32 {
    let base: string = "ab" + "cd";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = Some(base);
        acc = acc + i;
        i = i + 1;
    }
    let junk: string = "";
    let c: i32 = 0;
    while (c < 6) { junk = "zz" + "zz"; c = c + 1; }
    let sum: i32 = 0;
    let k: i32 = 0;
    while (k < base.len()) { sum = sum + (base[k] as i32); k = k + 1; }
    return acc + sum + junk.len() + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 251;
}`,
		},
		{
			// Not actually dead: the box is aliased outward and matched after the
			// loop, so it must stay out of the credit entirely.
			name: "box_read_after_the_loop",
			src: `function mk(i: i32): Option[string] { return Some("v" + "x"); }
function round(r: i32): i32 {
    let acc: i32 = 0;
    let keep: Option[string] = None;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = mk(i);
        keep = v;
        acc = acc + i;
        i = i + 1;
    }
    match (keep) { Some(s) => { acc = acc + s.len(); }, None => {} }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, "uosh_"+tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance; the exit "+
					"agreement is what says nothing still reachable was released",
					allocs, frees, live)
			}
		})
	}
}
