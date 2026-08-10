package e2eselfhost

import (
	"strings"
	"testing"
)

// An `Option[<tuple-with-array>]` consumed by a match one block deeper (#6319's
// tuple arm, the last of the four payload classes).
//
// `collect_fresh_opttup_names` is the consuming-match analysis for this shape and
// it found its match with `sole_top_level_match_idx` — the same flat-index
// blindness the scalar, rc-array and struct collectors each had. The EMITTER was
// never missing: `emit_opttup_deep_free` and `slot_is_reclaimable_opttup` already
// existed and already do the type-driven tuple deep-drop. Only the credit was
// absent, which is the opposite of #6588 next door, where emission was the work.
//
// NO nested_ok FLAG, unlike the scalar (#6526) and rc-payload (#6538) collectors,
// and that is measured rather than assumed: nothing else claims this shape at
// either scope. Both nested cells leaked — 12000 fn-scoped, 48000 block-scoped —
// while both flat cells were 0, so there is no territory to divide and no second
// credit to collide with.

const otNestedFnSrc = `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
    if (i >= 0) {
        match (o) { Some(t) => { acc = acc + t.0 + t.1.len(); }, None => { acc = acc + 1; } }
    }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 7;
}
`

const otFlatFnSrc = `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
    match (o) { Some(t) => { acc = acc + t.0 + t.1.len(); }, None => { acc = acc + 1; } }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 7;
}
`

const otNestedBlockSrc = `import "core/int";
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var o: Option[(i32, i32[])] = Some((k, [k, k + 1]));
            if (k >= 0) {
                match (o) { Some(t) => { acc = acc + t.0 + t.1.len(); }, None => { acc = acc + 1; } }
            }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

const otFlatBlockSrc = `import "core/int";
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var o: Option[(i32, i32[])] = Some((k, [k, k + 1]));
            match (o) { Some(t) => { acc = acc + t.0 + t.1.len(); }, None => { acc = acc + 1; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// The hazard `opttup_payload_escapes` exists for: the arm binds the tuple's ARRAY
// element out to an outer local, so the buffer is live where the deep drop would
// walk it. Measured at 1201/1 — refused, exit agreeing with the oracle.
const otPayloadEscapesSrc = `import "core/int";
function main(): i32 {
    var held: i32[] = [0, 0];
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var o: Option[(i32, i32[])] = Some((k, [k, k + 1]));
            if (k >= 0) {
                match (o) { Some(t) => { held = t.1; acc = acc + t.0; }, None => { acc = acc + 1; } }
            }
            acc = acc + held.len();
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

func TestSelfHostOptTupNestedMatchX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		// These return a value, so `fern -interp` IS the oracle: a tuple deep-drop
		// over a live element buffer shows up as a wrong answer, which matters
		// more than the byte counts.
		want := interpExit(t, interpBin, src)
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != want {
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — the tuple deep "+
				"drop reached a live buffer", name, exit, want)
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
		{"fn_scoped_nested", otNestedFnSrc},
		{"fn_scoped_flat_control", otFlatFnSrc},
		{"block_scoped_nested", otNestedBlockSrc},
		{"block_scoped_flat_control", otFlatBlockSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance. "+
					"The nested spelling leaked with frees=0 (12000 fn-scoped, 48000 "+
					"block-scoped) while both flat controls were 0", tc.name, allocs, frees, live)
			}
		})
	}

	t.Run("tuple_element_escapes_the_arm", func(t *testing.T) {
		allocs, frees, live := counts(t, "tuple_element_escapes_the_arm", otPayloadEscapesSrc)
		if live == 0 {
			t.Errorf("tuple_element_escapes_the_arm: allocs=%d frees=%d live_bytes=%d — the "+
				"arm binds the tuple's array element out to an outer local, so the deep "+
				"drop must be withheld. Exit agreement above is the real detector",
				allocs, frees, live)
		}
	})
}
