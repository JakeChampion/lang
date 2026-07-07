package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Statement-temporary reclamation, stage (a), on the self-host IR path: a
// discarded bare-ExprStmt whose value is a FRESH scalar-element array literal
// (`[i, i + 1, i + 2];`) is DEC'd at the statement boundary (the rc-guarded
// __fern_rc_dec, discardable_scalar_arr_lit) instead of leaking its buffer
// every iteration. This is the self-host sibling of native's
// emitOwnedTempStackDrop (internal/e2e/rc_heap_bump_stmt_temp_test.go);
// #4365 flagged it as a native-tested behavior with no self-host equivalent.
//
// Two assertions, both through the self-host x86-64 IR driver (asm_run):
//   - FIXPOINT: the discarded-temp loop's bump-growth is now BOUNDED — equal at
//     N=50 and N=5000 (before the reclaim it scaled with N: 96 -> 128 -> …).
//   - OVER-RELEASE: the discarded temp must reclaim its OWN box without touching
//     the live `xs` built from the same loop-variable operands — a wrong "owned"
//     verdict that freed a shared buffer would corrupt the sum (999) or trip the
//     __rc_underflow detector (> 0).

func stmtTempArrBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < ` + n + `) { [i, i + 1, i + 2]; i = i + 1; }
    return __heap_bump_bytes() - before;
}`
}

// A discarded owned array temp reclaims its box while the live `xs` (built from
// the same operands) is untouched: sum over i=0..199 of (i)+(i+1)+(i+2) =
// 3*(199*200/2) + 3*200 = 60300. __rc_underflow() (the self-host detector) then
// reports 0 only if nothing was over-released.
const stmtTempReclaimDetectorSrc = `function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        [i, i + 1, i + 2];
        var xs: i32[] = [i, i + 1, i + 2];
        acc = acc + xs[0] + xs[1] + xs[2];
        i = i + 1;
    }
    if (acc != 60300) { return 999; }
    return __rc_underflow();
}`

// The other stage-(a) discarded-temp shapes, each a fresh rc=1 sole-owner box
// released at the statement boundary: a scalar tuple / scalar struct literal
// (shallow __fern_rc_dec) and a string concat (rc-aware __fern_str_free).

func stmtTempTupleBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < ` + n + `) { (i, i + 1); i = i + 1; }
    return __heap_bump_bytes() - before;
}`
}

func stmtTempStructBumpSrc(n string) string {
	return `struct P { x: i32, y: i32 }
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < ` + n + `) { P { x: i, y: i + 1 }; i = i + 1; }
    return __heap_bump_bytes() - before;
}`
}

func stmtTempStrConcatBumpSrc(n string) string {
	return `function main(): i32 {
    var a: string = "hello";
    var b: string = "world";
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < ` + n + `) { a + b; i = i + 1; }
    return __heap_bump_bytes() - before;
}`
}

// Detectors: the discarded temp reclaims its OWN box while a live value built
// from the same operands stays intact — a wrong "owned" verdict that freed a
// shared box would corrupt the sum (999) or trip __rc_underflow (> 0). Sums:
// tuple/struct t=(i,i+2): 2i+2 over 0..199 = 40200; string s=a+b: 200 * 10.
const stmtTempTupleDetectorSrc = `function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        (i, i + 1);
        var t: (i32, i32) = (i, i + 2);
        acc = acc + t.0 + t.1;
        i = i + 1;
    }
    if (acc != 40200) { return 999; }
    return __rc_underflow();
}`

const stmtTempStructDetectorSrc = `struct P { x: i32, y: i32 }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        P { x: i, y: i + 1 };
        var p: P = P { x: i, y: i + 2 };
        acc = acc + p.x + p.y;
        i = i + 1;
    }
    if (acc != 40200) { return 999; }
    return __rc_underflow();
}`

const stmtTempStrConcatDetectorSrc = `function main(): i32 {
    var a: string = "hello"; var b: string = "world";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        a + b;
        var s: string = a + b;
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 2000) { return 999; }
    return __rc_underflow();
}`

// TestSelfHostStmtTempReclaimIRX86_64 builds the self-host x86-64 IR driver once
// and drives the two programs through it.
func TestSelfHostStmtTempReclaimIRX86_64(t *testing.T) {
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

	fixpointShapes := []struct {
		name string
		src  func(n string) string
	}{
		{"scalar-array", stmtTempArrBumpSrc},
		{"scalar-tuple", stmtTempTupleBumpSrc},
		{"scalar-struct", stmtTempStructBumpSrc},
		{"string-concat", stmtTempStrConcatBumpSrc},
	}
	for _, sh := range fixpointShapes {
		t.Run("fixpoint-bounded/"+sh.name, func(t *testing.T) {
			small := run(t, sh.name+"-50", sh.src("50"))
			large := run(t, sh.name+"-5000", sh.src("5000"))
			if small != large {
				t.Errorf("discarded-%s-temp bump must be bounded: N=50 -> %d, N=5000 -> %d", sh.name, small, large)
			}
			if small == 0 {
				t.Errorf("%s: expected a non-zero bounded high-water, got 0 (nothing allocated / measured)", sh.name)
			}
		})
	}

	detectorShapes := []struct {
		name string
		src  string
	}{
		{"scalar-array", stmtTempReclaimDetectorSrc},
		{"scalar-tuple", stmtTempTupleDetectorSrc},
		{"scalar-struct", stmtTempStructDetectorSrc},
		{"string-concat", stmtTempStrConcatDetectorSrc},
	}
	for _, sh := range detectorShapes {
		t.Run("no-over-release/"+sh.name, func(t *testing.T) {
			if code := run(t, sh.name+"-detector", sh.src); code != 0 {
				t.Errorf("discarded-%s-temp reclaim: exit=%d (999=value mismatch, >0=over-release)", sh.name, code)
			}
		})
	}
}
