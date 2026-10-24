package e2ecompiler

import (
	"strings"
	"testing"
)

const rcenumBorrowedSrc = `import "core/int";
enum Box { Val(i32[]), Empty }
function head(b: Box): i32 {
    match (b) { Val(xs) => { return xs[0]; }, Empty => { return 0; } }
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < ROUNDS) {
        let k: i32 = 0;
        while (k < 4) {
            let b: Box = Val([k, k + 7]);
            acc = acc + head(b);
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

const rcenumNeverUsedSrc = `import "core/int";
enum Box { Val(i32[]), Empty }
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < ROUNDS) {
        let k: i32 = 0;
        while (k < 4) {
            let b: Box = Val([k, k + 7]);
            acc = acc + k;
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

const rcenumInlineMatchSrc = `import "core/int";
enum Box { Val(i32[]), Empty }
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < ROUNDS) {
        let k: i32 = 0;
        while (k < 4) {
            let b: Box = Val([k, k + 7]);
            match (b) { Val(xs) => { acc = acc + xs[0]; }, Empty => { acc = acc + 1; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

const rcenumPayloadEscapesSrc = `import "core/int";
enum Box { Val(i32[]), Empty }
function take(b: Box): i32[] {
    match (b) { Val(xs) => { return xs; }, Empty => { return [0]; } }
}
function main(): i32 {
    let held: i32[] = [0, 0];
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < ROUNDS) {
        let k: i32 = 0;
        while (k < 4) {
            let b: Box = Val([k, k + 7]);
            held = take(b);
            acc = acc + held[0];
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

func TestSelfHostRcEnumBorrowHelperX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	counts := func(t *testing.T, name, src string, rounds string) (int64, int64, int64) {
		t.Helper()
		src = strings.ReplaceAll(src, "ROUNDS", rounds)
		want := interpExit(t, interpBin, src)
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name+rounds, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != want {
			t.Fatalf("%s@%s: self-host exited %d, fern -interp exited %d — the enum drop "+
				"reached a live payload", name, rounds, exit, want)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s@%s: no leakcheck summary — FERN_LEAKCHECK did not take effect", name, rounds)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("%s@%s: parse %q: %v", name, rounds, summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s@%s allocated nothing — the probe is not exercising the path", name, rounds)
		}
		return allocs, frees, live
	}

	for _, tc := range []struct {
		name      string
		src       string
		wantStuck int64
	}{
		{"helper_borrows_the_enum", rcenumBorrowedSrc, 0},
		{"never_used", rcenumNeverUsedSrc, 0},
		{"inline_match_control", rcenumInlineMatchSrc, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a1, f1, l1 := counts(t, tc.name, tc.src, "100")
			a2, f2, l2 := counts(t, tc.name, tc.src, "200")
			if a2 != a1*2 {
				t.Fatalf("%s: allocs %d at 100 rounds and %d at 200 — the probe does not "+
					"scale with the loop, so it cannot separate a leak from a reclaim",
					tc.name, a1, a2)
			}
			if a1-f1 != tc.wantStuck || a2-f2 != tc.wantStuck {
				t.Errorf("%s: unfreed allocations %d at 100 rounds and %d at 200, want %d at "+
					"both. This shape leaked EVERY iteration (frees=0, 24000 bytes at 300 "+
					"rounds against 48000 at 600) because the enum earned no credit without "+
					"a consuming match, and `head(b)` read as an escape. A residue of 2 that "+
					"does NOT grow with the rounds is the separate tail case: the block's "+
					"final value, released by the \"RCENUMS:\" sweep credit",
					tc.name, a1-f1, a2-f2, tc.wantStuck)
			}
			if l1 != 0 || l2 != 0 {
				t.Errorf("%s: live_bytes %d at 100 rounds and %d at 200, want 0 at both — "+
					"every box and payload is accounted for once the sweep releases the "+
					"final value", tc.name, l1, l2)
			}
		})
	}

	t.Run("callee_returns_the_payload", func(t *testing.T) {
		a1, f1, l1 := counts(t, "callee_returns_the_payload", rcenumPayloadEscapesSrc, "100")
		a2, f2, l2 := counts(t, "callee_returns_the_payload", rcenumPayloadEscapesSrc, "200")
		if a1 != f1 || a2 != f2 || l1 != 0 || l2 != 0 {
			t.Errorf("counted payload return must balance: 100 rounds %d/%d live=%d; 200 rounds %d/%d live=%d", a1, f1, l1, a2, f2, l2)
		}
	})
}
