package e2eselfhost

import (
	"strings"
	"testing"
)

// --- A nested block's reassign set is the BLOCK's, not the function's (#6127) -
//
// The block-level reclaim classifiers EXCLUDE a reassigned name from being a
// free candidate, so the set they consult decides whether a free is emitted at
// all. It has to be the set of the block the candidate is declared in: a
// function-wide set is strictly wider, and answering from it silences a free
// the block emits today whenever some OTHER block of the same function happens
// to assign the same spelling.
//
// Each source below pairs a nested-block candidate with a sibling block that
// assigns a same-named SCALAR — a name collision and nothing more, no shared
// storage and no second allocation. Answering the candidate's rebind question
// from the function's set turns each of these from an exact balance into a
// per-iteration leak of the payload it stopped crediting.
//
// The `_alone` rows are the controls: identical but for the sibling block, so a
// failure there says the shape stopped reclaiming for some unrelated reason
// rather than that the scope rule broke.
func TestSelfHostBlockReassignScopeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
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

	// An unmatched Option[string] declared in a loop body
	// (collect_unmatched_optstr_names).
	const optStrAlone = `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[string] = Some("v" + "x");
        acc = acc + i;
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

	const optStrSiblingAssignsTheSpelling = `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[string] = Some("v" + "x");
        acc = acc + i;
        i = i + 1;
    }
    if (r % 2 == 0) {
        var v: i32 = 0;
        v = r + 1;
        acc = acc + v;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

	// An unmatched Option[i32[]] declared in a loop body
	// (collect_unmatched_optarr_names).
	const optArrAlone = `function mk(i: i32): Option[i32[]] {
    if (i % 3 == 0) { return None; }
    return Some([i, i + 1]);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[i32[]] = mk(i);
        acc = acc + i;
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

	const optArrSiblingAssignsTheSpelling = `function mk(i: i32): Option[i32[]] {
    if (i % 3 == 0) { return None; }
    return Some([i, i + 1]);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[i32[]] = mk(i);
        acc = acc + i;
        i = i + 1;
    }
    if (r % 2 == 0) {
        var v: i32 = 0;
        v = r + 1;
        acc = acc + v;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
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
