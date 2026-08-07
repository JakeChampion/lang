package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #4357: a reclaimable struct LOOP-LOCAL carrying an rc-array field
// (`while { var t: P = …; }`, P = `{ x: i32, xs: i32[] }`) was reclaimed by a
// SHALLOW box-only dec at each loop-rebind (emit_arr_store), leaking its `xs`
// buffer every iteration — the StmtVar binding path skipped the deep
// __field_reclaim_<T> the StmtAssign consume-rebind already used. Growth scaled
// with N (48 → 160). The fix routes a reclaimable rc-field struct loop-local
// binding through emit_field_reclaim_store, so each rebind frees the prior box's
// field buffers before the box. Both a struct-LITERAL source and a strict-fresh-
// returning-CALL source are covered.
//
// Gated empirically on the self-host x86-64 IR path (asm_run):
//   - FIXPOINT: the loop's bump growth is now BOUNDED — equal at N=50 and N=5000.
//   - OVER-RELEASE: the deep reclaim frees only the dead prior box's buffers
//     (cow-guarded against the live value), so the field reads stay correct and
//     __rc_underflow() reports 0.

func rcFieldLoopLocalLiteralSrc(n string) string {
	return `struct P { x: i32, xs: i32[] }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var t: P = P { x: i, xs: [i, i + 1] }; acc = acc + t.x; i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func rcFieldLoopLocalCallSrc(n string) string {
	return `struct P { x: i32, xs: i32[] }
function f(v: i32): P { return P { x: v, xs: [v, v + 1] }; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var t: P = f(i); acc = acc + t.x; i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// A SCALAR-only fresh-returning-CALL struct loop-local (`var t = mk(i)`, P = {x,y}
// no rc field) leaks its BOX every iteration if collect_fresh_ret_call_names
// excludes it via the struct_has_reclaim_array_field gate, since it then never
// reaches reclaimable_names. Without that gate it is admitted (reclaimed by the
// shallow box dec, complete for a scalar struct). #4357 follow-up.
func scalarLoopLocalCallSrc(n string) string {
	return `struct P { x: i32, y: i32 }
function mk(v: i32): P { return P { x: v, y: v + 1 }; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var t: P = mk(i); acc = acc + t.x + t.y; i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// The deep reclaim must free only the dead PRIOR box's field buffers, never the
// live value's: acc reads t.x + all three xs elements each iteration, and a wrong
// free would corrupt the sum or trip __rc_underflow. per iter: i + i + (i+1) +
// (i+2) = 4i+3; sum over 0..199 = 4*19900 + 600 = 80200.
const rcFieldLoopLocalDetectorSrc = `struct P { x: i32, xs: i32[] }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var t: P = P { x: i, xs: [i, i + 1, i + 2] };
        acc = acc + t.x + t.xs[0] + t.xs[1] + t.xs[2];
        i = i + 1;
    }
    if (acc != 80200) { return 99; }
    return __rc_underflow();
}`

func TestSelfHostRcFieldLoopLocalReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, tag, prog string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", tag)
		}
		progBin := buildBin(t, gcc, dir, tag, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	shapes := []struct {
		name string
		src  func(n string) string
	}{
		{"literal-source", rcFieldLoopLocalLiteralSrc},
		{"fresh-call-source", rcFieldLoopLocalCallSrc},
		{"scalar-fresh-call-source", scalarLoopLocalCallSrc},
	}
	for _, sh := range shapes {
		t.Run("fixpoint-bounded/"+sh.name, func(t *testing.T) {
			small := run(t, sh.name+"-50", sh.src("50"))
			large := run(t, sh.name+"-5000", sh.src("5000"))
			if small != large {
				t.Errorf("rc-field struct loop-local (%s) bump must be bounded: N=50 -> %d, N=5000 -> %d (xs buffer leaked per iteration)", sh.name, small, large)
			}
			if small == 0 {
				t.Errorf("%s: expected a non-zero bounded high-water, got 0", sh.name)
			}
		})
	}

	t.Run("no-over-release", func(t *testing.T) {
		if code := run(t, "rcfield-loop-detector", rcFieldLoopLocalDetectorSrc); code != 0 {
			t.Errorf("rc-field loop-local deep reclaim over-released (exit %d, 99=value mismatch, >0=__rc_underflow)", code)
		}
	})
}
