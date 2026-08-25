package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"
)

// Self-host RC: a heap array payload of Ok()/Some()/Err() must be alias-inc'd.
//
// Regression guard for a self-host-only UAF (#2649): `Ok(r)` / `Some(r)` where
// `r` is a local array stored the buffer into the enum box WITHOUT the Perceus
// alias-inc that the enum-variant construction already performs, so the box's
// payload aliased the local `r` at refcount 1. The match arm that extracts the
// payload reclaims it at arm exit (an arr_dec), and the constructing function's
// own exit-sweep decremented `r` too — a double owner over one +1, freeing the
// buffer out from under the returned box. Benign until a later allocation reuses
// the freed store, so it only bit when the extracted array was held live ACROSS
// an allocating call (gdb: `names[j]` came back as the allocator's 0x7979... filler,
// faulting at the element's `movq 8(%rax)`).
//
// Fixed by adding the array alias-inc to lower_opt_make_payload (mirroring the
// enum-variant construction, irlower.fern). The native compiler always handled
// both shapes; this closes the self-host gap. rcMatchPayloadWorks /
// rcMatchPayloadUAF are the isolating pair (var-binding vs match-extraction of the
// SAME append-built array) — both must return 17.
//
// rcMatchPayloadWorks: the array is returned directly and bound to a `var` (no
// enum wrapper). Held across the same allocating `eat` loop, it stays intact.
// This is the ACTIVE guard — it must keep returning 17.
const rcMatchPayloadWorks = `function eat(n: i32): i32 {
    var s: string = "x";
    var i: i32 = 0;
    while (i < n) { s = s + "yyyyyyyyyy"; i = i + 1; }
    return s.len();
}
function build(): string[] {
    var r: string[] = [];
    r = r.append("alpha");
    r = r.append("bravo");
    r = r.append("charlie");
    return r;
}
function main(): i32 {
    var names: string[] = build();
    var total: i32 = 0;
    var j: i32 = 0;
    while (j < names.len()) {
        var junk: i32 = eat(200);
        total = total + names[j].len();
        j = j + 1;
    }
    return total;
}`

// rcMatchPayloadUAF: the SAME array, but returned as Ok(r) and extracted via
// `match`. Everything else is identical. Currently SIGSEGVs on the self-host IR
// path (correct answer, matching rcMatchPayloadWorks + native, is 17). Un-skip
// the subtest below when the self-host match-arm RC gains the ownership transfer.
const rcMatchPayloadUAF = `function eat(n: i32): i32 {
    var s: string = "x";
    var i: i32 = 0;
    while (i < n) { s = s + "yyyyyyyyyy"; i = i + 1; }
    return s.len();
}
function build(): Result[string[], i32] {
    var r: string[] = [];
    r = r.append("alpha");
    r = r.append("bravo");
    r = r.append("charlie");
    return Ok(r);
}
function main(): i32 {
    match (build()) {
        Ok(names) => {
            var total: i32 = 0;
            var j: i32 = 0;
            while (j < names.len()) {
                var junk: i32 = eat(200);
                total = total + names[j].len();
                j = j + 1;
            }
            return total;
        },
        Err(_) => { return 99; }
    }
}`

// rcOptStructPayloadUAF: the STRUCT-BOX sibling of rcMatchPayloadUAF, and the
// same bug one payload kind over. lower_opt_make_payload's alias-inc was gated on
// slot_is_rc_container — array / string / tuple slots — so `Some(p)` / `Ok(p)`
// where p is a struct LOCAL stored the box with no retain. p's exit sweep then
// freed a box the RETURNED option still pointed at, and `clobber`'s allocation
// reused the cell, so the payload read back 9999 instead of i.
//
// Both spellings are exercised: they build the same box through the same
// lowering, so a fix reaching only one of them is not a fix. Correct answer is 0
// (no round disagrees); the pre-fix self-host returned 100.
const rcOptStructPayloadUAF = `struct P { xs: i32[], k: i32 }

function some_of(i: i32): Option[P] {
    var p: P = P { xs: [i, i + 1], k: i };
    var o: Option[P] = Some(p);
    return o;
}

function ok_of(i: i32): Result[P, i32] {
    var p: P = P { xs: [i, i + 1], k: i };
    var o: Result[P, i32] = Ok(p);
    return o;
}

function clobber(i: i32): i32 {
    var q: P = P { xs: [9999, 9999], k: 9999 };
    return q.k + q.xs[0];
}

function round(i: i32): i32 {
    var a: Option[P] = some_of(i);
    var b: Result[P, i32] = ok_of(i);
    var junk: i32 = clobber(i);
    var m: i32 = 0;
    var n: i32 = 0;
    match (a) { Some(p2) => { m = p2.k; }, None => { m = 0 - 1; } }
    match (b) { Ok(p3) => { n = p3.k; }, Err(e) => { n = 0 - 1; } }
    if (m != i) { return 1; }
    if (n != i) { return 1; }
    return 0;
}

function main(): i32 {
    var bad: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { bad = bad + round(r); r = r + 1; }
    return bad;
}`

func compileAndRunSelfHostIR(t *testing.T, gcc string, runner []string, dir, driverBin, name, src string) int {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed for %s: %v", name, err)
	}
	progBin := buildBin(t, gcc, dir, name, string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	return run.ProcessState.ExitCode()
}

// TestSelfHostMatchPayloadRC pins the working half of the match-payload RC pair
// (a heap array bound to a `var` and held across an allocating call stays intact)
// and documents the broken half (the same array extracted via `match` is freed
// prematurely — a self-host-only UAF, #2649) as a skipped target for the fix.
func TestSelfHostMatchPayloadRC(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// Active guard: the var-binding shape must stay correct (17 = 5+5+7).
	t.Run("var_binding_across_alloc", func(t *testing.T) {
		if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "rc_work", rcMatchPayloadWorks); code != 17 {
			t.Errorf("var-binding array across an allocating call exited %d, want 17", code)
		}
	})

	// Fixed (#2649): the Option/Result construction now emits the Perceus
	// array alias-inc (lower_opt_make_payload), balancing the match-arm's reclaim
	// so the extracted array survives an intervening allocation.
	t.Run("match_payload_across_alloc", func(t *testing.T) {
		if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "rc_uaf", rcMatchPayloadUAF); code != 17 {
			t.Errorf("match-extracted array across an allocating call exited %d, want 17", code)
		}
	})

	// The struct-box payload takes the same retain: a returned `Some(p)` / `Ok(p)`
	// must outlive p's exit sweep.
	t.Run("struct_payload_across_alloc", func(t *testing.T) {
		if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "rc_optstruct_uaf", rcOptStructPayloadUAF); code != 0 {
			t.Errorf("returned option carrying a struct local exited %d, want 0 "+
				"(each nonzero round read a clobbered payload)", code)
		}
	})
}
