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

// cbeMixedErrFreshPathSrc is cbeMixedErrPathSrc with the Err payload COMPUTED
// rather than written as a literal. Since #7080 a string literal is static data
// and allocates nothing, so the literal program can no longer strand a payload —
// there is no payload box to strand. This twin keeps the stranding observable,
// which is the property the case below exists to pin.
const cbeMixedErrFreshPathSrc = `function mk(i: i32): Result[i32, string] {
    if (i % 2 == 0) { return Err("neg" + "!"); }
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

// The same function-scoped shape bound from a CALL rather than an inline
// constructor. The annotation gate always accepted it; the init gate took only
// literal constructors, so a producer call fell through both this credit and
// the match analysis and reclaimed nowhere. Admitting an OPTFRESH-registered
// callee is the same freshness proof rcpayload_option_call_ptype uses on the
// match-consumed side.
const cbeOptArrCallNoMatchSrc = `function mk(i: i32): Result[i32[], string] {
    if (i < 0) { return Err("neg"); }
    return Ok([i, i + 1, i + 2]);
}
function round(r: i32): i32 {
    var v: Result[i32[], string] = mk(r);
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

// An OPTFRESH producer whose Ok payload is its own PARAMETER, so the payload
// dec lands on a buffer the caller also holds. The caller's `a` reaches `mk`
// only as an argument, which the caller-side escape analysis reads as an
// escape, so `a` carries no second credit and the two frees cannot collide.
// This is the case that would double-free if that were wrong, so it asserts an
// exact balance rather than just live_bytes==0. Function-scoped on purpose: a
// loop-scoped binding carries the separate 8800 block-scope residue, which
// would mask the direction of any imbalance here.
const cbeOptArrAliasedPayloadSrc = `function mk(xs: i32[]): Result[i32[], string] {
    if (xs.len() == 0) { return Err("empty"); }
    return Ok(xs);
}
function round(r: i32): i32 {
    var a: i32[] = [r, r + 1, r + 2];
    var v: Result[i32[], string] = mk(a);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { acc = acc + a[1] + 3; i = i + 1; }
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
		// Closed by dropping the scalar-Err requirement from
		// rcpayload_option_call_ptype: the tagged drop frees the payload only
		// under tag==0, so a non-scalar Err is stranded rather than dangled,
		// and refusing the shape leaked the box as well.
		{"rcpayload_from_call_with_match", cbeRcPayloadCallMatchSrc, 72},
		// Closed by admitting an OPTFRESH-registered call in the unmatched
		// credit's init gate, which previously took literal constructors only.
		{"optarr_from_call_no_match", cbeOptArrCallNoMatchSrc, 38},
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
		// This asserted exactly 200 stranded until #7080. The 200 were boxes for
		// the `Err("neg")` payload, one per construction; a literal is static data
		// now, so there is no payload box to strand and the balance is exact. That
		// is the "drive allocs == frees" outcome the comment above predicted — by a
		// different route than it guessed, since the free did not become deep.
		// cbeMixedErrFreshPathSrc keeps the stranding case, on a payload that
		// really allocates.
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d live=%d — want an exact balance. The Err payload is a literal, "+
				"so it allocates nothing and there is nothing for the shallow free to leave behind",
				allocs, frees, live)
		}
	})

	// The stranding case, on a payload the compiler must actually allocate. The
	// shallow free releases every Result box and never reaches into the payload,
	// so the payload's allocations survive — which is what licenses the widening.
	// An exit code that moves means the free DID reach a payload, the one thing it
	// must never do.
	t.Run("mixed_result_fresh_err_path_strands_only_the_payload", func(t *testing.T) {
		allocs, frees, live := counts(t, "mixed_result_fresh_err_path", cbeMixedErrFreshPathSrc, 72)
		if frees != 400 {
			t.Errorf("frees=%d, want 400 — every box must be released regardless of which arm ran "+
				"(allocs=%d live=%d)", frees, allocs, live)
		}
		if allocs <= frees {
			t.Errorf("allocs=%d frees=%d live=%d — a computed Err payload must still be stranded by the "+
				"shallow free; an exact balance here means the probe stopped allocating one",
				allocs, frees, live)
		}
	})

	// The aliased-payload producer. An imbalance in EITHER direction is a real
	// defect: frees > allocs means the payload dec collided with a caller-side
	// credit, frees < allocs means the widening lost the box.
	t.Run("optarr_aliased_payload_balances", func(t *testing.T) {
		allocs, frees, live := counts(t, "optarr_aliased_payload", cbeOptArrAliasedPayloadSrc, 39)
		if allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live=%d — want an exact balance. The caller's buffer reaches "+
				"mk only as an argument, so it carries no credit for the payload dec to collide with",
				allocs, frees, live)
		}
	})

	// The same two shapes declared INSIDE the loop, 35200 -> 8800 -> 0. The
	// 8800 was the FINAL iteration's box+payload: `slot_is_reclaimable_optarr`
	// looked its credit up under the slot's VERBATIM name, and a block-scoped
	// slot has been renamed `retired: <name>` by the time the exit sweep runs.
	// Routing that lookup through `reclaim_slot_name` — which its
	// arr-of-arr sibling already used — is the whole fix.
	//
	// These assert an exact balance rather than live_bytes==0 alone: this is the
	// operation that segfaulted gen1 twice (#6285 / #6375), so an over-release
	// here matters as much as a leak.
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"rcpayload_direct_no_match", cbeRcPayloadDirectNoMatchSrc},
		{"optarr_loop_scope", cbeOptArrLoopScopeSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src, 38)
			if allocs != frees || live != 0 {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance. 8800 means the "+
					"exit sweep is missing the final iteration's pair again (the retired slot name); "+
					"frees > allocs means it now releases a slot something else still owns",
					tc.name, allocs, frees, live)
			}
		})
	}
}
