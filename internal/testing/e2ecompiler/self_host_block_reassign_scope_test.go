package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostBlockReassignScopeX86_64(t *testing.T) {
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
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d", name, exit, want)
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

	const optStrAlone = `function round(r: i32): i32 {
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
}`

	const optStrSiblingAssignsTheSpelling = `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = Some("v" + "x");
        acc = acc + i;
        i = i + 1;
    }
    if (r % 2 == 0) {
        let v: i32 = 0;
        v = r + 1;
        acc = acc + v;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

	const optArrAlone = `function mk(i: i32): Option[i32[]] {
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
}`

	const optArrSiblingAssignsTheSpelling = `function mk(i: i32): Option[i32[]] {
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
    if (r % 2 == 0) {
        let v: i32 = 0;
        v = r + 1;
        acc = acc + v;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

	for _, tc := range []struct{ name, src string }{
		{"optstr_alone", optStrAlone},
		{"optstr_sibling_assigns_the_spelling", optStrSiblingAssignsTheSpelling},
		{"optarr_alone", optArrAlone},
		{"optarr_sibling_assigns_the_spelling", optArrSiblingAssignsTheSpelling},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, "brs_"+tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance. A "+
					"sibling block assigning the same SPELLING must not withdraw this "+
					"block's credit; that is the function-wide set answering a question "+
					"only the block's own set can answer (#6127)",
					allocs, frees, live)
			}
		})
	}
}
