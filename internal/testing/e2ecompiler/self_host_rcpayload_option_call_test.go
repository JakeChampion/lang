package e2ecompiler

import (
	"strings"
	"testing"
)

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

func TestSelfHostRcPayloadOptionDirectX86_64(t *testing.T) {
	_, _, live, summary := runRcPayloadOptionCallProbe(t, rcPayloadOptionDirectSrc, 3)
	if live != 0 {
		t.Errorf("%s: live_bytes=%d, want 0 — the DIRECT rc-payload form was "+
			"already reclaimed before the call-init class existed, so a "+
			"regression here means the shared deep drop broke", summary, live)
	}
}
