package e2eselfhost

import (
	"fmt"
	"testing"
)

// Self-host RC: an Option / Result box is released whatever its payload is made
// of, and a bound one nothing consumes is released too (#8806); the built-in I/O
// producers' boxes are released as well (#8811, the self-host half of #8405).
//
// Before this, the release rode two payload-KIND admissions: a scalar payload
// (consumed_scalar_enum_frees) or a leak-safe array / fresh string / nested
// scalar Option (consumed_rcpayload_option_frees). A payload in neither — a
// struct, an IoError, a Reader — kept the whole 40-byte box as well as itself,
// and the free rode the CONSUMING match, so a binding simply dead at scope exit
// had no sweep at all.
//
// Each leg pins INDEPENDENCE FROM THE ROUND COUNT: ten times the calls must cost
// the same live bytes, and every block the loop allocates must come back. That
// is what a per-call constant passing for a fix cannot satisfy, and unlike an
// absolute byte count it does not go stale when another allocation joins the
// probe.

// optBoxNonScalarScrutSrc: `match (g(i))` on a user producer whose payload is a
// struct-shaped IoError. No builtin is involved — the box leaked for the payload
// KIND alone.
func optBoxNonScalarScrutSrc(rounds int) string {
	return fmt.Sprintf(`function g(x: i32): Option[IoError] { return None; }
function main(): i32 {
    var i: i32 = 0;
    while (i < %d) {
        match (g(i)) { Some(_) => { return 9; }, None => {} }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

// optBoxNonScalarBoundSrc is the same box reached through a written binding
// rather than as a scrutinee — the two spellings measured identically before,
// and both have to be covered because they are released by different analyses.
func optBoxNonScalarBoundSrc(rounds int) string {
	return fmt.Sprintf(`function g(x: i32): Option[IoError] { return None; }
function main(): i32 {
    var i: i32 = 0;
    while (i < %d) {
        var e: Option[IoError] = g(i);
        match (e) { Some(_) => { return 9; }, None => {} }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

// optBoxDeadScalarSrc: a scalar-payload Option bound and never looked at. Its
// scalar sibling with a match after each binding was always released; dropping
// the value without matching it was the one shape that kept the block.
func optBoxDeadScalarSrc(rounds int) string {
	return fmt.Sprintf(`function f(x: i32): Option[i32] { if (x > 0) { return Some(x); } return None; }
function main(): i32 {
    var i: i32 = 0;
    while (i < %d) {
        var a: Option[i32] = f(i);
        i = i + 1;
    }
    return 0;
}`, rounds)
}

// optBoxDeadNonScalarSrc is the dead binding at a payload kind nothing released
// either — the two gaps at once.
func optBoxDeadNonScalarSrc(rounds int) string {
	return fmt.Sprintf(`function g(x: i32): Option[IoError] { return None; }
function main(): i32 {
    var i: i32 = 0;
    while (i < %d) {
        var a: Option[IoError] = g(i);
        i = i + 1;
    }
    return 0;
}`, rounds)
}

// optBoxReturningArmSrc leaves the function from INSIDE the matched arm, so the
// join release never runs on that path: only the return-path arming covers that
// box. Written the obvious way — the loop condition ends it and the `return`
// sits in an arm that never fires — it passes with the arming removed, which is
// what a probe for this has to avoid.
func optBoxReturningArmSrc(rounds int) string {
	return fmt.Sprintf(`function g(x: i32): Option[IoError] { return None; }
function main(): i32 {
    var i: i32 = 0;
    while (i < 1000000) {
        var e: Option[IoError] = g(i);
        match (e) {
            Some(_) => { return 9; },
            None => { i = i + 1; if (i >= %d) { return 0; } }
        }
    }
    return 0;
}`, rounds)
}

// optBoxNestedMatchSrc keeps the box at a function's TOP LEVEL with its match one
// block deeper, which is the precise-drop half of the fix rather than the
// consumed-match half: a top-level local whose consuming match is nested is
// claimed by neither consumed_optbox_frees (it looks for a top-level scrutinee)
// nor the dead-binding branch (the `if` is a use). The round is a call so the
// shape repeats without the binding moving into a block.
func optBoxNestedMatchSrc(rounds int) string {
	return fmt.Sprintf(`function g(x: i32): Option[IoError] { return None; }
function step(i: i32): i32 {
    var e: Option[IoError] = g(i);
    if (i >= 0) { match (e) { Some(_) => { return 9; }, None => {} } }
    return 0;
}
function main(): i32 {
    var i: i32 = 0;
    while (i < %d) {
        if (step(i) != 0) { return 9; }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

func TestSelfHostOptBoxReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const (
		few  = 20
		many = 200
	)

	for _, tc := range []struct {
		name string
		src  func(rounds int) string
	}{
		{name: "nonscalar_payload_scrutinee", src: optBoxNonScalarScrutSrc},
		{name: "nonscalar_payload_bound", src: optBoxNonScalarBoundSrc},
		{name: "dead_binding_scalar", src: optBoxDeadScalarSrc},
		{name: "dead_binding_nonscalar", src: optBoxDeadNonScalarSrc},
		{name: "returning_arm", src: optBoxReturningArmSrc},
		{name: "nested_match_fn_level", src: optBoxNestedMatchSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			measure := func(label string, rounds int) (int64, int64, int64) {
				asm := hevCompile(t, runner, driverBin, tc.src(rounds), []string{"FERN_LEAKCHECK=1"})
				bin := buildBin(t, gcc, dir, tc.name+"_"+label, asm)
				stderr, exit := runWithStdin(t, runner, bin, nil)
				if exit != 0 {
					t.Fatalf("%s/%s exited %d (9 = the wrong arm ran; 99 = rc underflow), stderr:\n%s",
						tc.name, label, exit, stderr)
				}
				return leakSummaryOf(t, tc.name+"/"+label, stderr)
			}
			fa, ff, fl := measure("few", few)
			ma, mf, ml := measure("many", many)
			if fl != ml {
				t.Errorf("live_bytes %d rounds=%d vs %d rounds=%d (allocs %d/%d, frees %d/%d): "+
					"the box is left behind every round", fl, few, ml, many, fa, ma, ff, mf)
			}
			if ma != mf || ml != 0 {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — every block the loop allocates must be freed",
					tc.name, ma, mf, ml)
			}
		})
	}
}

// ioOpenCloseSrc is #8811's own reproducer: an open-and-close round, whose two
// Result / Option boxes came back to nobody. #8813 gave __fern_open_res's
// NUL-terminated path buffer an owner too, so the round now leaves NOTHING and
// this leg reads the ordinary allocs-equal-frees assertion: one box a round
// unreleased shows as 200 unfreed blocks, both as 400.
func ioOpenCloseSrc(rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var i: i32 = 0;
    while (i < %d) {
        match (open_reader("/dev/null")) {
            Ok(r) => { match (r.close()) { Some(e) => { return 9; }, None => {} } },
            Err(e) => { return 8; }
        }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

// ioOpenCloseBoundSrc is the same round with the open's Result reached through a
// `var` first. A binding, unlike an anonymous match scrutinee, can carry reclaim
// credits of its own, so it is admitted by a different predicate
// (opt_box_init_type) and needs its own leg: `Result[Reader, IoError]` carries
// no payload either side that anything deep-drops, so the box-only release is
// the whole of it.
func ioOpenCloseBoundSrc(rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var i: i32 = 0;
    while (i < %d) {
        var r: Result[Reader, IoError] = open_reader("/dev/null");
        match (r) {
            Ok(rd) => { match (rd.close()) { Some(e) => { return 9; }, None => {} } },
            Err(e) => { return 8; }
        }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

// ioReadChunkSrc opens ONE reader outside the loop, so the round-count
// independence is about the per-call boxes alone: read_chunk's Result box and
// the string payload the arm binds.
func ioReadChunkSrc(rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var acc: i32 = 0;
    match (open_reader("/dev/zero")) {
        Ok(r) => {
            var i: i32 = 0;
            while (i < %d) {
                match (r.read_chunk(64)) {
                    Ok(chunk) => { acc = acc + chunk.len(); },
                    Err(e) => { return 9; }
                }
                i = i + 1;
            }
        },
        Err(e) => { return 8; }
    }
    if (acc < 0) { return 7; }
    return 0;
}`, rounds)
}

// TestSelfHostIoResultBoxReclaimX86_64 — the built-in I/O half (#8811). The
// family goes in together: an open-and-close round strands three boxes when none
// is admitted, so admitting one or two cannot be told from admitting none by a
// zero-live-bytes assertion alone, which is why this leg reads the SLOPE.
func TestSelfHostIoResultBoxReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const (
		few  = 20
		many = 200
	)
	measure := func(name, label string, src string) (int64, int64, int64) {
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		bin := buildBin(t, gcc, dir, name+"_"+label, asm)
		stderr, exit := runWithStdin(t, runner, bin, nil)
		if exit != 0 {
			t.Fatalf("%s/%s exited %d (8 = the open failed; 9 = the call reported an error; 99 = rc underflow), stderr:\n%s",
				name, label, exit, stderr)
		}
		return leakSummaryOf(t, name+"/"+label, stderr)
	}

	t.Run("open_close", func(t *testing.T) {
		fa, ff, fl := measure("open_close", "few", ioOpenCloseSrc(few))
		ma, mf, ml := measure("open_close", "many", ioOpenCloseSrc(many))
		if fl != ml || ma != mf || ml != 0 {
			t.Errorf("allocs %d/%d, frees %d/%d, live_bytes %d/%d at %d and %d rounds: "+
				"an open-and-close round must leave nothing — one box a round unreleased "+
				"shows here as %d unfreed blocks, both as %d",
				fa, ma, ff, mf, fl, ml, few, many, many, 2*many)
		}
	})

	t.Run("open_close_bound", func(t *testing.T) {
		fa, ff, fl := measure("open_close_bound", "few", ioOpenCloseBoundSrc(few))
		ma, mf, ml := measure("open_close_bound", "many", ioOpenCloseBoundSrc(many))
		if fl != ml || ma != mf || ml != 0 {
			t.Errorf("allocs %d/%d, frees %d/%d, live_bytes %d/%d at %d and %d rounds: "+
				"the bound open's Result box is not released",
				fa, ma, ff, mf, fl, ml, few, many)
		}
	})

	t.Run("read_chunk", func(t *testing.T) {
		_, _, fl := measure("read_chunk", "few", ioReadChunkSrc(few))
		ma, mf, ml := measure("read_chunk", "many", ioReadChunkSrc(many))
		if fl != ml {
			t.Errorf("live_bytes %d rounds=%d vs %d rounds=%d (allocs=%d frees=%d): "+
				"the per-call Result box or its payload is left behind every round",
				fl, few, ml, many, ma, mf)
		}
	})
}
