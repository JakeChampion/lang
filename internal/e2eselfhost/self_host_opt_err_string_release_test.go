package e2eselfhost

import (
	"strings"
	"testing"
)

// --- The Err arm's string, stranded by an empty else-branch (#6360) ----------
//
// Every tag-guarded Option/Result drop frees the payload under `tag == 0` and the
// box on every path, with NOTHING in the else-branch. #6463 made that explicit
// when it dropped the scalar-Err gate: "a non-scalar Err payload is never reached
// — it is stranded, not dangled". Stranded is sound, and it is still a leak:
// `Result[i32[], string]` from a call leaves 6400 over 100 rounds, exactly ×2.0
// per doubling, against 0 on native.
//
// Filling that branch needs a proof the registry did not have. The "f" flag is
// computed from the SUCCESS payload only (`body_has_nonfresh_opt_success_payload`),
// so it cannot vouch for an Err string, and reusing it here would release a
// caller's box on the Err path. The walker is now parameterised by which
// constructor it inspects — one traversal, two verdicts — and the Err verdict gets
// its own tagged registry row (`ERRFRESH:`, seeded as `OPTERRFRESH:`).
//
// Both quadrants are covered. The match-consumed one fills the else-branch of
// `emit_opt_tagged_payload_drop`; the UNMATCHED one reaches `emit_optarr_deep_free`
// instead, and its Err release is gated per-SLOT ("OPTARRERR:") because that
// emitter is shared with the reassigned+match OPTARR class, whose slots carry no
// Err-freshness proof — an unconditional branch there would dangle.

func TestSelfHostOptErrStringReleaseX86_64(t *testing.T) {
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
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — the Err release "+
				"reached a live string", name, exit, want)
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

	// The headline row: the Err strings are released, 6400 -> 0.
	t.Run("err_string_from_a_call_with_a_match", func(t *testing.T) {
		src := `function mk(i: i32): Result[i32[], string] {
    if (i % 3 == 0) { return Err("e" + "rr"); }
    return Ok([i, i + 1]);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32[], string] = mk(i);
        match (v) { Ok(xs) => { acc = acc + xs[0]; }, Err(e) => { acc = acc + e.len(); } }
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
		allocs, frees, live := counts(t, "oes_err_call", src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance; the Err "+
				"strings were 6400 while the tag guard's else-branch was empty", allocs, frees, live)
		}
	})

	// Every return is an Err, so the else-branch carries the whole program.
	t.Run("err_string_on_every_return", func(t *testing.T) {
		src := `function mk(i: i32): Result[i32[], string] {
    return Err("e" + "rr");
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32[], string] = mk(i);
        match (v) { Ok(xs) => { acc = acc + xs[0]; }, Err(e) => { acc = acc + e.len(); } }
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
		allocs, frees, live := counts(t, "oes_err_always", src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance", allocs, frees, live)
		}
	})

	// A bare Err literal: __fern_str_free's heap-base guard no-ops on .rodata.
	t.Run("err_string_literal", func(t *testing.T) {
		src := `function mk(i: i32): Result[i32[], string] {
    if (i % 3 == 0) { return Err("literal"); }
    return Ok([i, i + 1]);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32[], string] = mk(i);
        match (v) { Ok(xs) => { acc = acc + xs[0]; }, Err(e) => { acc = acc + e.len(); } }
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
		allocs, frees, live := counts(t, "oes_err_literal", src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance", allocs, frees, live)
		}
	})

	// REFUSED: the producer's Err payload is a bare parameter, so it aliases the
	// caller's box. Churns same-shaped strings before the aliased read, because
	// otherwise the released box is not recycled and this exits correctly with the
	// bug present.
	t.Run("aliased_err_payload_still_strands", func(t *testing.T) {
		src := `function mk(i: i32, held: string): Result[i32[], string] {
    if (i % 3 == 0) { return Err(held); }
    return Ok([i, i + 1]);
}
function round(r: i32): i32 {
    var shared: string = "ab" + "cd";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32[], string] = mk(i, shared);
        match (v) { Ok(xs) => { acc = acc + xs[0]; }, Err(e) => { acc = acc + e.len(); } }
        i = i + 1;
    }
    var junk: string = "";
    var c: i32 = 0;
    while (c < 6) { junk = "zz" + "zz"; c = c + 1; }
    var sum: i32 = 0;
    var k: i32 = 0;
    while (k < shared.len()) { sum = sum + (shared[k] as i32); k = k + 1; }
    return acc + sum + junk.len() + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 251;
}`
		allocs, frees, live := counts(t, "oes_err_alias", src)
		if live == 0 {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want a nonzero remainder. This "+
				"producer's Err payload is its own PARAMETER, so it aliases the caller's box "+
				"and releasing it is a dangle. The \"f\" flag would admit it — it describes "+
				"the success payload — which is why the Err verdict is computed separately",
				allocs, frees, live)
		}
	})

	// The other half, closed in turn. Same leak, different emitter: no consuming
	// match, so this reaches emit_optarr_deep_free rather than
	// emit_opt_tagged_payload_drop, and its Err release is gated per-SLOT
	// ("OPTARRERR:") because the OPTARR credit is shared with the reassigned+match
	// class, whose slots carry no Err-freshness proof.
	//
	// This case was pinned AS a leak while that half was open, and converted here
	// rather than deleted — which is what its failure message asked for.
	t.Run("unmatched_err_string_is_released", func(t *testing.T) {
		src := `function mk(i: i32): Result[i32[], string] {
    if (i % 3 == 0) { return Err("e" + "rr"); }
    return Ok([i, i + 1]);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32[], string] = mk(i);
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
		allocs, frees, live := counts(t, "oes_err_unmatched", src)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live_bytes=%d — want an exact balance; the Err "+
				"strings were 6400 here while only the match-consumed emitter had an Err branch",
				allocs, frees, live)
		}
	})
}
