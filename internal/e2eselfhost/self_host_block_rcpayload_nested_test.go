package e2eselfhost

import (
	"strings"
	"testing"
)

// A BLOCK-scoped rc-payload `Option` built by a DIRECT ctor and consumed by a
// match one block deeper (#6319, the rc-payload analogue of #6526's scalar row).
//
// #6480 widened `consumed_rcpayload_option_frees`' match lookup to the nested
// spelling for CALL inits only, because at function scope a direct ctor is
// already `precise_drop_names`' is_rcopt candidate and letting both analyses
// claim it segfaults. A BLOCK-scoped local has no such owner —
// `precise_drop_names` is only ever called with `fn.body` — so the direct ctor
// leaked there: 35200 over 100 rounds, `frees=0`, where every other spelling of
// the same shape was 0.
//
// THE FLAG IS LOAD-BEARING HERE, which is the difference from #6526's scalar
// version. Flipping the fn-scope call site to `nested_ok = true` segfaults both
// TestSelfHostNestedMatchBorrowNoUnderflow's program and the opt-struct-payload
// hazard, where the same mutation on the SCALAR collector was harmless. A scalar
// drop is a shallow box free that slot-zeroing neutralises on a second credit; an
// rc-payload drop releases the payload as well, so the second credit double-frees
// it. Same-shaped flag, genuinely different reason — do not assume one from the
// other.

// The row this closes: rc payload, direct ctor, declared in a loop, match nested.
const blkRcDirectNestedSrc = `import "core/int";
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var v: Option[i32[]] = Some([k, k + 1, k + 2]);
            if (k >= 0) {
                match (v) { Some(a) => { acc = acc + a.len(); }, None => { acc = acc + 1; } }
            }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// The same shape from a CALL — #6480's row, block-scoped. Already worked; it must
// keep working with exactly one credit now the direct ctor shares the lookup.
const blkRcCallNestedSrc = `import "core/int";
function make(i: i32): Result[i32[], string] {
    if (i < 0) { return Err("neg"); }
    return Ok([i, i + 1, i + 2]);
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var v: Result[i32[], string] = make(k);
            if (k >= 0) {
                match (v) { Ok(a) => { acc = acc + a.len(); }, Err(e) => { acc = acc + e.len(); } }
            }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// The FLAT control for the direct ctor — the spelling that always worked.
const blkRcDirectFlatSrc = `import "core/int";
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var v: Option[i32[]] = Some([k, k + 1, k + 2]);
            match (v) { Some(a) => { acc = acc + a.len(); }, None => { acc = acc + 1; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// The hazard, block-scoped: the arm binding escapes to an outer local, so the
// payload is still reachable where the drop would land and must be refused.
const blkRcEscapeSrc = `import "core/int";
function main(): i32 {
    var held: i32[] = [0, 0];
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var v: Option[i32[]] = Some([k, k + 1, k + 2]);
            if (k >= 0) {
                match (v) { Some(a) => { held = a; }, None => { acc = acc + 1; } }
            }
            acc = acc + held.len();
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

func TestSelfHostBlockRcPayloadNestedMatchX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		// These return a value, not the underflow counter, so `fern -interp` IS
		// the oracle: a double-freed payload shows up as a wrong answer or a
		// crash, which matters more than the byte counts.
		want := interpExit(t, interpBin, src)
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != want {
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — the payload drop "+
				"reached a live buffer", name, exit, want)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary — FERN_LEAKCHECK did not take effect", name)
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

	for _, tc := range []struct{ name, src string }{
		{"direct_ctor_nested", blkRcDirectNestedSrc},
		{"call_init_nested", blkRcCallNestedSrc},
		{"direct_ctor_flat_control", blkRcDirectFlatSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance. "+
					"The block-scoped direct ctor leaked 35200 over 100 rounds with frees=0 "+
					"while every other spelling of this shape was 0", tc.name, allocs, frees, live)
			}
		})
	}

	t.Run("arm_binding_escapes", func(t *testing.T) {
		allocs, frees, live := counts(t, "arm_binding_escapes", blkRcEscapeSrc)
		if live == 0 {
			t.Errorf("arm_binding_escapes: allocs=%d frees=%d live_bytes=%d — the arm "+
				"binding escapes to an outer local, so the payload must be stranded rather "+
				"than released. Exit agreement above is the real detector; a full balance "+
				"here means the escape gate was bypassed", allocs, frees, live)
		}
	})
}
