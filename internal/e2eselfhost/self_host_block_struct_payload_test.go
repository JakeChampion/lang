package e2eselfhost

import (
	"strings"
	"testing"
)

// A BLOCK-scoped `Option[P]` whose payload struct carries an rc ARRAY field,
// consumed by a match one block deeper (#6319's struct arm).
//
// `blockable` admits only payloads whose drop is COMPLETE on its own, and a
// struct payload was excluded for a concrete reason: the block-level emission had
// three branches (tagged / string / flat dec) and no deep one, so claiming a
// struct there emitted a shallow dec, zeroed the slot, and starved the fn-level
// OPTSTRUCT machinery of the array FIELDS — the #5453 regression.
//
// The fix is on the EMISSION side, not the admission side: the block pass gained
// the same `emit_opt_struct_payload_drop` branch lower_func has, and `blockable`
// now keys on `dsty` — which already means "the deep drop is available AND no arm
// moved a field out of it".
//
// THE FIELD-MOVE GATE HAD TO BE CORRECTED FIRST. It read `body[match_idx]`, which
// under a nested lookup is the enclosing `if` — a statement it cannot parse, so
// it answered "no field moved" and would have granted a deep drop over a moved-out
// field. That was masked while struct payloads were unblockable and by the
// arm-escape gate refusing those shapes earlier; making them blockable puts real
// weight on it, so it now reads the match itself.

// The row this closes.
const blkStructNestedSrc = `import "core/int";
struct P { xs: i32[], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var o: Option[P] = Some(P { xs: [k, k + 1], n: k });
            if (k >= 0) {
                match (o) { Some(p) => { acc = acc + p.n + p.xs.len(); }, None => { acc = acc + 1; } }
            }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// The flat control — always worked, and must not gain a second credit.
const blkStructFlatSrc = `import "core/int";
struct P { xs: i32[], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var o: Option[P] = Some(P { xs: [k, k + 1], n: k });
            match (o) { Some(p) => { acc = acc + p.n + p.xs.len(); }, None => { acc = acc + 1; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// THE HAZARD THE DEEP DROP EXISTS TO AVOID: the arm moves the array FIELD out to
// an outer local, so the field buffer is still live where the deep drop would
// walk it. Freeing it is a use-after-free, which shows up as a wrong exit rather
// than as a byte count — hence the oracle, and hence this row asserting a leak.
const blkStructFieldMovedSrc = `struct P { xs: i32[], n: i32 }
function main(): i32 {
    var held: i32[] = [0, 0];
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var o: Option[P] = Some(P { xs: [k, k + 1], n: k });
            if (k >= 0) {
                match (o) { Some(p) => { held = p.xs; acc = acc + p.n; }, None => { acc = acc + 1; } }
            }
            acc = acc + held[1];
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 83;
}
`

func TestSelfHostBlockStructPayloadNestedMatchX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		// These return a value, so `fern -interp` IS the oracle: a deep drop over
		// a live field buffer shows up as a wrong answer or a crash, and that
		// matters more than the byte counts.
		want := interpExit(t, interpBin, src)
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != want {
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — the deep field "+
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
		{"struct_payload_nested", blkStructNestedSrc},
		{"struct_payload_flat_control", blkStructFlatSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance. "+
					"The nested spelling leaked 51200 over 100 rounds with frees=0, because "+
					"the block pass had no deep-drop branch to reach", tc.name, allocs, frees, live)
			}
		})
	}

	t.Run("field_moved_out_of_the_arm", func(t *testing.T) {
		allocs, frees, live := counts(t, "field_moved_out_of_the_arm", blkStructFieldMovedSrc)
		if live == 0 {
			t.Errorf("field_moved_out_of_the_arm: allocs=%d frees=%d live_bytes=%d — the arm "+
				"moves the array field to an outer local, so the deep drop must be withheld "+
				"and the buffer stranded. Exit agreement above is the real detector; a full "+
				"balance here means the field-move gate was bypassed", allocs, frees, live)
		}
	})
}
