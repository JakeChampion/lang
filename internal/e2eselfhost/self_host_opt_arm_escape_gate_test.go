package e2eselfhost

import (
	"strings"
	"testing"
)

// --- The call-bound escape gate, which matched nothing (#6360) ---------------
//
// `opt_arm_binding_escapes` compares the arm's variant SPELLING against the name
// its caller passes. #6451 introduced the call-init candidate with a hardcoded
// `variant: "Ok"` and a comment claiming "Ok covers Some too — matches the arm by
// payload position, not by spelling". It does not: `"Ok"` never equals `"Some"`,
// so for every `match (v) { Some(s) => ..., None => ... }` the gate iterated the
// arms and refused nothing.
//
// That was survivable while the payload was an ARRAY, because an escaping arm
// binding RETAINS — `held = xs` incs the buffer, so the drop's dec is balanced.
// #6469 then admitted STRING success payloads to the same drop, and a string
// assignment is a BORROW. With the gate matching nothing, `held = s` leaves
// `__fern_str_free` releasing a box the caller still reads.
//
// The gate now takes `"Ok|Some"` for the string rows: the SUCCESS arm under
// either spelling. Two things it deliberately is NOT:
//
//   - not applied to the array rows. Doing so refuses a shape that is already
//     correct and costs real reclaim — `escaping_arm_binding_array` drops from
//     1600/1600 to 800/1600. That row is pinned below for exactly this reason.
//   - not "every arm". A scalar `Err(e)` used in arithmetic reads as an escape,
//     which strands `Result[string, i32]` at 22400. A scalar binding is never a
//     payload this drop releases.
//
// THE FAILURE IS INVISIBLE TO A PLAIN PROBE. With the bug present, the escaping
// shape still exits correctly — the freed box is simply not reused before it is
// read. It only becomes observable once same-shaped strings are churned in
// between, which recycles it: exit 178 against `-interp`'s 55. Both hazard
// probes below therefore churn before the aliased read, and both take their
// expected exit from `fern -interp` rather than a constant.
//
// #6469's suite covers the producer-side aliases (payload aliases a live local or
// a parameter). This is the consumer side, which its "f" flag cannot speak to:
// the flag proves the producer built a fresh payload, and says nothing about what
// the CONSUMER's match arm then does with it.

func TestSelfHostOptArmEscapeGateX86_64(t *testing.T) {
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

	// The shape that dangles when the gate matches nothing. `held = s` aliases the
	// arm binding out of the match; the churn recycles the released box before the
	// read, which is what turns a silent corruption into a wrong exit code.
	t.Run("escaping_arm_binding_string_is_refused", func(t *testing.T) {
		src := `function mk(i: i32): Option[string] {
    return Some("va" + "lue");
}
function round(r: i32): i32 {
    var held: string = "";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[string] = mk(i);
        match (v) { Some(s) => { held = s; acc = acc + s.len(); }, None => {} }
        i = i + 1;
    }
    var junk: string = "";
    var c: i32 = 0;
    while (c < 6) { junk = "zz" + "zzz"; c = c + 1; }
    var sum: i32 = 0;
    var k: i32 = 0;
    while (k < held.len()) { sum = sum + (held[k] as i32); k = k + 1; }
    return acc + sum + junk.len() + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 251;
}`
		allocs, frees, live := counts(t, "oae_str_escape", src)
		if frees != 3300 || live == 0 {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want frees=3300 and a nonzero "+
				"remainder. A HIGHER count means the escaping binding's string was released "+
				"under a live alias, which is a dangle rather than the leak this shape must keep",
				allocs, frees, live)
		}
	})

	// Same escape written as a `Result`, so the SUCCESS arm is spelled `Ok`. Pins
	// that the marker covers both spellings rather than trading one miss for another.
	t.Run("escaping_arm_binding_result_ok_is_refused", func(t *testing.T) {
		src := `function mk(i: i32): Result[string, i32] {
    return Ok("va" + "lue");
}
function round(r: i32): i32 {
    var held: string = "";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[string, i32] = mk(i);
        match (v) { Ok(s) => { held = s; acc = acc + s.len(); }, Err(e) => { acc = acc + e; } }
        i = i + 1;
    }
    var junk: string = "";
    var c: i32 = 0;
    while (c < 6) { junk = "zz" + "zzz"; c = c + 1; }
    var sum: i32 = 0;
    var k: i32 = 0;
    while (k < held.len()) { sum = sum + (held[k] as i32); k = k + 1; }
    return acc + sum + junk.len() + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 251;
}`
		allocs, frees, live := counts(t, "oae_result_escape", src)
		if live == 0 {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want a nonzero remainder; an `Ok(s)` "+
				"arm that escapes must be refused exactly as the `Some(s)` spelling is",
				allocs, frees, live)
		}
	})

	// A borrow-only arm binding is still admitted — the gate must refuse escapes,
	// not every binding.
	t.Run("borrowing_arm_binding_still_reclaims", func(t *testing.T) {
		src := `function mk(i: i32): Option[string] {
    return Some("va" + "lue");
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[string] = mk(i);
        match (v) { Some(s) => { acc = acc + s.len(); }, None => {} }
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
		allocs, frees, live := counts(t, "oae_borrow_ok", src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — a borrow-only binding must still "+
				"reclaim; the gate refuses escapes, not bindings", allocs, frees, live)
		}
	})

	// The ARRAY row the marker is deliberately kept away from. An escaping array
	// binding retains, so refusing it would cost reclaim for no soundness gain —
	// this drops to 800 the moment the marker is widened past strings.
	t.Run("escaping_arm_binding_array_still_reclaims", func(t *testing.T) {
		src := `function mk(i: i32): Option[i32[]] {
    return Some([i + 11, i + 22]);
}
function round(r: i32): i32 {
    var held: i32[] = [];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[i32[]] = mk(i);
        match (v) { Some(xs) => { held = xs; acc = acc + xs[0]; }, None => {} }
        i = i + 1;
    }
    var junk: i32[] = [];
    var c: i32 = 0;
    while (c < 6) { junk = [c + 90, c + 91]; c = c + 1; }
    return acc + held[0] + held[1] + junk[0] + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 251;
}`
		allocs, frees, live := counts(t, "oae_arr_escape", src)
		if frees != 1600 || live != 0 {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want 1600 frees and 0 remaining. "+
				"An escaping arm binding RETAINS an array, so this shape is already correct "+
				"and reclaims fully; it drops to 800 if the string-only \"Ok|Some\" marker "+
				"is widened to the array rows (native leaks 11200 here)", allocs, frees, live)
		}
	})

	// A scalar `Err(e)` used in arithmetic must NOT read as an escape. This is the
	// row that strands at 22400 if the marker matches every arm rather than the
	// success one.
	t.Run("scalar_err_binding_is_not_an_escape", func(t *testing.T) {
		src := `function mk(i: i32): Result[string, i32] {
    if (i % 3 == 0) { return Err(i); }
    return Ok("v" + "x");
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[string, i32] = mk(i);
        match (v) { Ok(s) => { acc = acc + s.len(); }, Err(e) => { acc = acc + e; } }
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
		allocs, frees, live := counts(t, "oae_scalar_err", src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance. A scalar Err "+
				"binding is not a payload this drop can release, so treating it as an escape "+
				"only strands the success payload", allocs, frees, live)
		}
	})
}
