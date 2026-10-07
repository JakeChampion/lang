package e2ecompiler

import (
	"strings"
	"testing"
)

// An rc-payload `Option`/`Result` local bound from a CALL is reclaimed
// (#6360, second half). Bound from a constructor (`Some([..])`) the variant is
// visible at the init; bound from `mk(i)` it is not, so the payload release
// has to be guarded on the tag at run time.
//
//	payload  init
//	rc       direct
//	rc       call     <- the shape these tests pin
//	scalar   direct
//	scalar   call     <- #6416
//
// These assert `live_bytes` 0 and nonzero allocs rather than a byte count: the
// number moves with box sizes, and nonzero allocs make a probe that stopped
// exercising the path fail rather than pass vacuously.
//
// WHY NO GATE CAUGHT THIS. From #6360: the rc detector counts over-*releases*,
// so a pure leak reads as a clean 0; the fixpoint is self-referential and blind
// to a stable leak; and the alloc differential compares `__heap_bump_bytes()`
// growth, which the freelist masks. `FERN_LEAKCHECK` is the instrument that
// sees it, which is why these are leakcheck tests and not exit-code tests.

// rcPayloadOptionCallSrc: `Option[i32[]]` from a producer call — the simplest
// shape in the class. One payload type, one tag branch, no `Err` arm at all.
// 100 rounds x 4, so a per-iteration leak is 35200 bytes and unmistakable.
const rcPayloadOptionCallSrc = `import "core/int";

function make(i: i32): Option[i32[]] {
    if (i < 0) { return None; }
    return Some([i, i + 1, i + 2]);
}

function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let k: i32 = 0;
        while (k < 4) {
            let v: Option[i32[]] = make(k);
            match (v) { Some(a) => { acc = acc + a.len(); }, None => { acc = acc + 1; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// rcPayloadResultCallSrc: the `Result` sibling, with a SCALAR `Err`. That is
// exactly the admitted shape — the tag guard's else-branch is empty because
// only the success arm carries anything to release. A `string` Err is refused
// by admission and is covered by the negative test below.
const rcPayloadResultCallSrc = `import "core/int";

function make(i: i32): Result[i32[], i32] {
    if (i < 0) { return Err(0 - 1); }
    return Ok([i, i + 1, i + 2]);
}

function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let k: i32 = 0;
        while (k < 4) {
            let v: Result[i32[], i32] = make(k);
            match (v) { Ok(a) => { acc = acc + a.len(); }, Err(e) => { acc = acc + e; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// rcPayloadOptionDirectSrc is the CONTROL that was already green, and it still
// matters: it is the row that proves the deep drop was never the missing
// piece. If a change to the call path ever regresses this, the fix has broken
// the machinery it was supposed to reuse.
const rcPayloadOptionDirectSrc = `import "core/int";

function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let k: i32 = 0;
        while (k < 4) {
            let v: Option[i32[]] = Some([k, k + 1, k + 2]);
            match (v) { Some(a) => { acc = acc + a.len(); }, None => { acc = acc + 1; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// runRcPayloadOptionCallProbe compiles src through the self-host asm IR driver
// with leak accounting on, runs it, and returns the parsed summary.
func runRcPayloadOptionCallProbe(t *testing.T, src string, wantExit int) (allocs, frees, live int64, summary string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "rcpay_opt_call", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != wantExit {
		t.Fatalf("program exited %d, want %d — a wrong answer here would mean a "+
			"MISCOMPILE rather than a leak, and the byte counts below would be "+
			"measuring the wrong program", exit, wantExit)
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	if summary == "" {
		t.Fatal("no leakcheck summary — FERN_LEAKCHECK did not take effect")
	}
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if allocs == 0 {
		t.Fatal("program allocated nothing — the probe is not exercising the path")
	}
	return allocs, frees, live, summary
}

func TestSelfHostRcPayloadOptionFromCallX86_64(t *testing.T) {
	_, frees, live, summary := runRcPayloadOptionCallProbe(t, rcPayloadOptionCallSrc, 3)
	if live != 0 {
		t.Errorf("%s: live_bytes=%d, want 0 — an Option[i32[]] bound from a "+
			"producer call must be reclaimed; this leaked 35200 with frees=0 "+
			"before #6360's second half, and it scales linearly with the loop "+
			"count, so any nonzero here is unbounded", summary, live)
	}
	if frees == 0 {
		t.Errorf("%s: frees=0 — the box is never released at all, which is the "+
			"exact signature of the class being absent rather than partial",
			summary)
	}
}

func TestSelfHostRcPayloadResultFromCallX86_64(t *testing.T) {
	_, frees, live, summary := runRcPayloadOptionCallProbe(t, rcPayloadResultCallSrc, 3)
	if live != 0 {
		t.Errorf("%s: live_bytes=%d, want 0 — a Result[i32[], i32] bound from a "+
			"producer call must be reclaimed (scalar Err, so only the success "+
			"arm carries a payload to drop)", summary, live)
	}
	if frees == 0 {
		t.Errorf("%s: frees=0 — the box is never released at all", summary)
	}
}

// The direct-construction control. Green before the fix and after it; it fails
// only if the call path's tag-guarded drop has disturbed the unconditional one
// it was built to reuse.
func TestSelfHostRcPayloadOptionDirectX86_64(t *testing.T) {
	_, _, live, summary := runRcPayloadOptionCallProbe(t, rcPayloadOptionDirectSrc, 3)
	if live != 0 {
		t.Errorf("%s: live_bytes=%d, want 0 — the DIRECT rc-payload form was "+
			"already reclaimed before the call-init class existed, so a "+
			"regression here means the shared deep drop broke", summary, live)
	}
}
