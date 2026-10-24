package e2ecompiler

import (
	"strings"
	"testing"
)

const cbeOptCallSrc = `function mk(i: i32): Option[i32] {
    if (i < 0) { return None; }
    return Some(i);
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[i32] = mk(i);
        match (v) { Some(x) => { acc = acc + x; }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeResultScalarSrc = `function mk(i: i32): Result[i32, i32] {
    if (i < 0) { return Err(0); }
    return Ok(i);
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[i32, i32] = mk(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeDirectSrc = `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[i32, string] = Ok(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeMixedResultSrc = `function mk(i: i32): Result[i32, string] {
    if (i < 0) { return Err("neg"); }
    return Ok(i);
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[i32, string] = mk(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeMixedErrPathSrc = `function mk(i: i32): Result[i32, string] {
    if (i % 2 == 0) { return Err("neg"); }
    return Ok(i);
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[i32, string] = mk(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeMixedErrFreshPathSrc = `function mk(i: i32): Result[i32, string] {
    if (i % 2 == 0) { return Err("neg" + "!"); }
    return Ok(i);
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[i32, string] = mk(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeRcPayloadDirectMatchSrc = `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[i32[], string] = Ok([i, i + 1, i + 2]);
        match (v) { Ok(a) => { acc = acc + a[0]; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeRcPayloadDirectNoMatchSrc = `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[i32[], string] = Ok([i, i + 1, i + 2]);
        acc = acc + 1;
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeRcPayloadCallMatchSrc = `function mk(i: i32): Result[i32[], string] {
    if (i < 0) { return Err("neg"); }
    return Ok([i, i + 1, i + 2]);
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[i32[], string] = mk(i);
        match (v) { Ok(a) => { acc = acc + a[0]; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeOptArrFnScopeSrc = `function round(r: i32): i32 {
    let v: Option[i32[]] = Some([r, r + 1, r + 2]);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) { acc = acc + 1; i = i + 1; }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeOptArrCallNoMatchSrc = `function mk(i: i32): Result[i32[], string] {
    if (i < 0) { return Err("neg"); }
    return Ok([i, i + 1, i + 2]);
}
function round(r: i32): i32 {
    let v: Result[i32[], string] = mk(r);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) { acc = acc + 1; i = i + 1; }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeOptArrAliasedPayloadSrc = `function mk(xs: i32[]): Result[i32[], string] {
    if (xs.len() == 0) { return Err("empty"); }
    return Ok(xs);
}
function round(r: i32): i32 {
    let a: i32[] = [r, r + 1, r + 2];
    let v: Result[i32[], string] = mk(a);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) { acc = acc + a[1] + 3; i = i + 1; }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeOptArrLoopScopeSrc = `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[i32[]] = Some([i, i + 1, i + 2]);
        acc = acc + 1;
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

func TestSelfHostCallBoundEnumReclaimX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
		{"rcpayload_direct_with_match", cbeRcPayloadDirectMatchSrc, 72},
		{"optarr_fnscope_no_match", cbeOptArrFnScopeSrc, 38},
		{"rcpayload_from_call_with_match", cbeRcPayloadCallMatchSrc, 72},
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

	t.Run("mixed_result_from_call_reclaims_the_box", func(t *testing.T) {
		allocs, frees, live := counts(t, "mixed_result_from_call", cbeMixedResultSrc, 72)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live=%d — want an exact balance. The Err arm is unreachable "+
				"here (mk never returns Err), so admitting this shape strands nothing", allocs, frees, live)
		}
	})

	t.Run("mixed_result_err_path_strands_only_the_payload", func(t *testing.T) {
		allocs, frees, live := counts(t, "mixed_result_err_path", cbeMixedErrPathSrc, 72)
		if frees != 400 {
			t.Errorf("frees=%d, want 400 — every box must be released regardless of which arm ran "+
				"(allocs=%d live=%d)", frees, allocs, live)
		}
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d live=%d — want an exact balance. The Err payload is a literal, "+
				"so it allocates nothing and there is nothing for the shallow free to leave behind",
				allocs, frees, live)
		}
	})

	t.Run("mixed_result_fresh_err_path_strands_only_the_payload", func(t *testing.T) {
		allocs, frees, live := counts(t, "mixed_result_fresh_err_path", cbeMixedErrFreshPathSrc, 72)
		if allocs != 600 || frees != 600 || live != 0 {
			t.Errorf("allocs=%d frees=%d live=%d — want 600/600/0: every box and payload released",
				allocs, frees, live)
		}
	})

	t.Run("optarr_aliased_payload_balances", func(t *testing.T) {
		allocs, frees, live := counts(t, "optarr_aliased_payload", cbeOptArrAliasedPayloadSrc, 39)
		if allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live=%d — want an exact balance. The caller's buffer reaches "+
				"mk only as an argument, so it carries no credit for the payload dec to collide with",
				allocs, frees, live)
		}
	})

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
