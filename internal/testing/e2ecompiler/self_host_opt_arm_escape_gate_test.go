package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostOptArmEscapeGateX86_64(t *testing.T) {
	boxedProbes(t)
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
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — the payload free "+
				"reached a string the arm binding still holds", name, exit, want)
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

	t.Run("escaping_arm_binding_string_is_refused", func(t *testing.T) {
		src := `function mk(i: i32): Option[string] {
    return Some("va" + "lue");
}
function round(r: i32): i32 {
    let held: string = "";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = mk(i);
        match (v) { Some(s) => { held = s; acc = acc + s.len(); }, None => {} }
        i = i + 1;
    }
    let junk: string = "";
    let c: i32 = 0;
    while (c < 6) { junk = "zz" + "zzz"; c = c + 1; }
    let sum: i32 = 0;
    let k: i32 = 0;
    while (k < held.len()) { sum = sum + (held[k] as i32); k = k + 1; }
    return acc + sum + junk.len() + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 251;
}`
		allocs, frees, live := counts(t, "oae_str_escape", src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance", allocs, frees, live)
		}
	})

	t.Run("escaping_arm_binding_result_ok_is_refused", func(t *testing.T) {
		src := `function mk(i: i32): Result[string, i32] {
    return Ok("va" + "lue");
}
function round(r: i32): i32 {
    let held: string = "";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[string, i32] = mk(i);
        match (v) { Ok(s) => { held = s; acc = acc + s.len(); }, Err(e) => { acc = acc + e; } }
        i = i + 1;
    }
    let junk: string = "";
    let c: i32 = 0;
    while (c < 6) { junk = "zz" + "zzz"; c = c + 1; }
    let sum: i32 = 0;
    let k: i32 = 0;
    while (k < held.len()) { sum = sum + (held[k] as i32); k = k + 1; }
    return acc + sum + junk.len() + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 251;
}`
		allocs, frees, live := counts(t, "oae_result_escape", src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance, as the "+
				"`Some(s)` spelling has", allocs, frees, live)
		}
	})

	t.Run("borrowing_arm_binding_still_reclaims", func(t *testing.T) {
		src := `function mk(i: i32): Option[string] {
    return Some("va" + "lue");
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[string] = mk(i);
        match (v) { Some(s) => { acc = acc + s.len(); }, None => {} }
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
		allocs, frees, live := counts(t, "oae_borrow_ok", src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — a borrow-only binding must still "+
				"reclaim; the gate refuses escapes, not bindings", allocs, frees, live)
		}
	})

	t.Run("escaping_arm_binding_array_still_reclaims", func(t *testing.T) {
		src := `function mk(i: i32): Option[i32[]] {
    return Some([i + 11, i + 22]);
}
function round(r: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Option[i32[]] = mk(i);
        match (v) { Some(xs) => { held = xs; acc = acc + xs[0]; }, None => {} }
        i = i + 1;
    }
    let junk: i32[] = [];
    let c: i32 = 0;
    while (c < 6) { junk = [c + 90, c + 91]; c = c + 1; }
    return acc + held[0] + held[1] + junk[0] + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 251;
}`
		allocs, frees, live := counts(t, "oae_arr_escape", src)
		if frees != 1400 || live != 0 {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want 1400 frees and 0 remaining. "+
				"An escaping arm binding RETAINS an array, so this shape reclaims fully",
				allocs, frees, live)
		}
	})

	t.Run("scalar_err_binding_is_not_an_escape", func(t *testing.T) {
		src := `function mk(i: i32): Result[string, i32] {
    if (i % 3 == 0) { return Err(i); }
    return Ok("v" + "x");
}
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let v: Result[string, i32] = mk(i);
        match (v) { Ok(s) => { acc = acc + s.len(); }, Err(e) => { acc = acc + e; } }
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
		allocs, frees, live := counts(t, "oae_scalar_err", src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance. A scalar Err "+
				"binding is not a payload this drop can release, so treating it as an escape "+
				"only strands the success payload", allocs, frees, live)
		}
	})
}
