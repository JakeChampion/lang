package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Call-bound scalar enum locals reclaim (#6360) --------------------------
//
// `consumed_scalar_enum_frees` admitted an init only when it was a DIRECT
// constructor — `fresh_scalar_option_init` matches the callee by name (Some /
// Ok / Err / None). A `var v: Option[i32] = mk(i)` binding was therefore never
// even a candidate, so no free was emitted and the box leaked on every
// iteration: `frees=0`, one box per round, while the byte-identical shape with
// the constructor written inline was flat at 0.
//
// The freshness proof already existed and was going unused. opt_fresh_ret_fns_of
// registers every free function whose Option/Result return is always a direct
// constructor, and lower_func seeds it as "OPTFRESH:<name>" — but only
// lower_try's `?`-edge consulted it. The user-enum sibling
// (collect_fresh_rcenum_names via rcenum_call_init_owner) has admitted call
// inits since #4355 slice 5; this is the same admission for Option / Result.
//
// The gate checks the SUCCESS payload only. It used to require EVERY variant's
// payload to be scalar, which excluded `Result[T, string]` — the idiomatic error
// type, and so the common case: measured 16000 bytes over 100 rounds at #6392,
// `frees=0`, the box never released at all.
//
// The reason given for that exclusion was that a shallow free "would leak the
// Err arm's string payload", which is true but is not a soundness argument. The
// free is one __fern_rc_dec on the box and never reaches offset 8, so it cannot
// dangle a payload however the box is shaped — an unreleased payload is a LEAK.
// Refusing the shape leaked the box AS WELL as the payload, so the strict gate
// was strictly worse on every count.
//
// Both directions are still pinned, and the Err-path case below is what makes
// the widening honest rather than a green light: it takes the Err arm on half
// its iterations and asserts the exact split — every box freed, and precisely
// the Err payloads stranded, nothing more.

const cbeOptCallSrc = `function mk(i: i32): Option[i32] {
    if (i < 0) { return None; }
    return Some(i);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[i32] = mk(i);
        match (v) { Some(x) => { acc = acc + x; }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeResultScalarSrc = `function mk(i: i32): Result[i32, i32] {
    if (i < 0) { return Err(0); }
    return Ok(i);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32, i32] = mk(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// The direct-ctor form, which already reclaimed. A regression guard: the new
// call-init admission must not disturb the path it sits beside.
const cbeDirectSrc = `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32, string] = Ok(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// Err carries a string, so the variant a call returns is not statically known.
// This shape used to be REFUSED and leaked its box on every iteration; it is now
// admitted, and on this source the Err arm is never taken, so nothing is
// stranded at all.
const cbeMixedResultSrc = `function mk(i: i32): Result[i32, string] {
    if (i < 0) { return Err("neg"); }
    return Ok(i);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32, string] = mk(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// The Err arm actually TAKEN, on the even `i`s — the case that distinguishes
// "the box is freed and only the Err payload is stranded" from "the payload is
// being freed too, by a shallow dec that reached offset 8".
const cbeMixedErrPathSrc = `function mk(i: i32): Result[i32, string] {
    if (i % 2 == 0) { return Err("neg"); }
    return Ok(i);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32, string] = mk(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// --- rc-PAYLOAD Option/Result: reclaimed by the consuming match, nothing else ---
//
// The three probes below isolate what #6360's own summary got wrong. That issue
// concluded "call-binding is the trigger" and "the match is irrelevant", having
// varied only the call-bound rows — where both happen to hold. Varying the match
// on the DIRECT row shows the opposite: the consuming match is the entire reclaim
// mechanism for an rc-payload Option/Result local, and call-binding is one of two
// independent ways to fall outside it.
//
// Measured at 09b3efe2, 100 rounds x 4 iterations, exit codes matching native on
// every row (so these are leaks, not miscompiles):
//
//	direct ctor + match   800/800   0        <- the mechanism
//	direct ctor, NO match 800/0     35200
//	from a call + match   800/0     35200
//
// Neither leaking shape is covered by anything: consumed_rcpayload_option_frees
// needs a sole consuming match AND a statically known variant (a call's variant
// is not), and collect_fresh_optarr_names ("OPTARR:") deliberately requires
// REASSIGNMENT, as the complement of the match analyses. The uncovered quadrant
// is the non-reassigned local that no match consumes.
const cbeRcPayloadDirectMatchSrc = `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32[], string] = Ok([i, i + 1, i + 2]);
        match (v) { Ok(a) => { acc = acc + a[0]; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeRcPayloadDirectNoMatchSrc = `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32[], string] = Ok([i, i + 1, i + 2]);
        acc = acc + 1;
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeRcPayloadCallMatchSrc = `function mk(i: i32): Result[i32[], string] {
    if (i < 0) { return Err("neg"); }
    return Ok([i, i + 1, i + 2]);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32[], string] = mk(i);
        match (v) { Ok(a) => { acc = acc + a[0]; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// --- the Option half, CLOSED by the unmatched-OPTARR credit -----------------
//
// `Option[<flat scalar array>]`, non-reassigned, no consuming match: neither
// collect_fresh_optarr_names (requires reassignment) nor
// consumed_rcpayload_option_frees (requires a sole consuming match) looked at
// it, so it was reclaimed nowhere. collect_unmatched_optarr_names credits it
// under the same "OPTARR:" tag, which routes it through the existing exit
// sweep and entry-zeroing.
//
// Function-scoped, single bind: 200/200, 0 — fully closed.
const cbeOptArrFnScopeSrc = `function round(r: i32): i32 {
    var v: Option[i32[]] = Some([r, r + 1, r + 2]);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { acc = acc + 1; i = i + 1; }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// The same shape declared INSIDE the loop: 35200 -> 8800. Six of the eight
// objects a call allocates are now released; the pair the FINAL iteration
// leaves live is not, because the exit sweep does not reach the retired slot
// name. That is the block-scoped-slot class, which is the one that segfaults
// gen1 when granted more (#6285 / #6375) — a distinct follow-up, not this
// credit's job. Pinned at its measured value so the improvement cannot silently
// regress and the remainder cannot be silently forgotten.
const cbeOptArrLoopScopeSrc = `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[i32[]] = Some([i, i + 1, i + 2]);
        acc = acc + 1;
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

func TestSelfHostCallBoundEnumReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	counts := func(t *testing.T, name, src string, wantExit int) (int64, int64, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != wantExit {
			t.Fatalf("%s exited %d, want %d", name, exit, wantExit)
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

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"option_scalar_from_call", cbeOptCallSrc, 72},
		{"result_all_scalar_from_call", cbeResultScalarSrc, 72},
		{"result_direct_ctor", cbeDirectSrc, 72},
		// The control for the rc-payload family: the same payload kind that
		// leaks in the two cases below reclaims fully when a match consumes it.
		{"rcpayload_direct_with_match", cbeRcPayloadDirectMatchSrc, 72},
		{"optarr_fnscope_no_match", cbeOptArrFnScopeSrc, 38},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src, tc.want)
			if live != 0 {
				t.Errorf("%s: live_bytes=%d, want 0 — one unfreed box per round (allocs=%d frees=%d)",
					tc.name, live, allocs, frees)
			}
		})
	}

	// The uncovered quadrant, pinned at its measured value rather than left
	// undescribed. These assert the LEAK: a fix flips the test, which is the
	// point — it forces the row to be re-measured and delisted deliberately,
	// the way #6291 closing ARRSTRUCT/ARRTUP incidentally went unnoticed for
	// three issues because nothing pinned it.
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"rcpayload_from_call_with_match", cbeRcPayloadCallMatchSrc, 72},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src, tc.want)
			if frees != 0 || live != 35200 {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want frees=0 live_bytes=35200.\n"+
					"If this now reclaims, that is the #6360 rc-payload gap CLOSING: delete this case, "+
					"move the shape into the live_bytes==0 table above, and update #6360.",
					tc.name, allocs, frees, live)
			}
		})
	}

	// A mixed Result whose Err arm is never taken: the box is reclaimed and there
	// is no payload to strand, so this balances exactly like the all-scalar case.
	// It leaked 16000 at #6392.
	t.Run("mixed_result_from_call_reclaims_the_box", func(t *testing.T) {
		allocs, frees, live := counts(t, "mixed_result_from_call", cbeMixedResultSrc, 72)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live=%d — want an exact balance. The Err arm is unreachable "+
				"here (mk never returns Err), so admitting this shape strands nothing", allocs, frees, live)
		}
	})

	// The Err arm TAKEN, on half the iterations. This is what licenses the
	// widening, so it asserts the split exactly rather than just "some frees":
	// 100 rounds x 4 iterations allocates 400 boxes and, on the two even `i`s per
	// round, 200 Err strings. Every box must be freed and precisely the 200
	// strings stranded.
	//
	// A change that makes the free variant-aware or deep should drive
	// allocs == frees here and will fail this case — that is the intended
	// signal, not a regression. A change that pushes frees BELOW 400 has lost
	// the box credit and is a real regression. Either way the exit code, which
	// is `fern -interp`'s, must not move: a wrong answer means the shallow free
	// reached a payload, which is the one thing it must never do.
	t.Run("mixed_result_err_path_strands_only_the_payload", func(t *testing.T) {
		allocs, frees, live := counts(t, "mixed_result_err_path", cbeMixedErrPathSrc, 72)
		if frees != 400 {
			t.Errorf("frees=%d, want 400 — every box must be released regardless of which arm ran "+
				"(allocs=%d live=%d)", frees, allocs, live)
		}
		if allocs-frees != 200 {
			t.Errorf("allocs-frees=%d, want exactly 200 — the stranded set must be the Err strings and "+
				"nothing else (allocs=%d frees=%d live=%d)", allocs-frees, allocs, frees, live)
		}
	})

	// The same Option shape declared INSIDE the loop: 35200 -> 8800. Six of the
	// eight objects a call allocates are released; the pair the FINAL iteration
	// leaves live is not, because the exit sweep does not reach the retired slot
	// name. That is the block-scoped-slot class — the one that segfaults gen1
	// when granted more (#6285 / #6375) — and a distinct follow-up.
	// Result[<flat scalar array>, _] declared inside the loop: the same partial
	// close the Option spelling gets, via unmatched_optarr_ann_is. The Err
	// payload is stranded by construction (emit_optarr_deep_free frees the
	// payload only on tag==0, and tag 0 is Ok), which is the trade
	// mixed_result_err_path_strands_only_the_payload already pins.
	t.Run("rcpayload_direct_no_match_partial", func(t *testing.T) {
		allocs, frees, live := counts(t, "rcpayload_direct_no_match", cbeRcPayloadDirectNoMatchSrc, 38)
		if frees != 600 || live != 8800 {
			t.Errorf("rcpayload_direct_no_match: allocs=%d frees=%d live_bytes=%d — want frees=600 live_bytes=8800 "+
				"(was 800/0/35200 before the credit widened to Result)", allocs, frees, live)
		}
	})

	t.Run("optarr_loop_scope_partial", func(t *testing.T) {
		allocs, frees, live := counts(t, "optarr_loop_scope_partial", cbeOptArrLoopScopeSrc, 38)
		if frees != 600 || live != 8800 {
			t.Errorf("optarr_loop_scope_partial: allocs=%d frees=%d live_bytes=%d — want frees=600 live_bytes=8800.\n"+
				"Was 800/0/35200 before the unmatched-OPTARR credit. If live_bytes is now 0 the block-scoped "+
				"remainder has closed too: move this into the live_bytes==0 table and update #6360.",
				allocs, frees, live)
		}
	})
}
