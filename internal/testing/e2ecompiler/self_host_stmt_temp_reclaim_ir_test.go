package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func stmtTempArrBumpSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { [i, i + 1, i + 2]; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const stmtTempReclaimDetectorSrc = `function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        [i, i + 1, i + 2];
        let xs: i32[] = [i, i + 1, i + 2];
        acc = acc + xs[0] + xs[1] + xs[2];
        i = i + 1;
    }
    if (acc != 60300) { return 999; }
    return __rc_underflow_count();
}`

func stmtTempTupleBumpSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { (i, i + 1); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stmtTempStructBumpSrc(n string) string {
	return `struct P { x: i32, y: i32 }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { P { x: i, y: i + 1 }; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stmtTempStrConcatBumpSrc(n string) string {
	return `function main(): i32 {
    let a: string = "hello";
    let b: string = "world";
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { a + b; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stmtTempRcFieldStructBumpSrc(n string) string {
	return `struct H { id: i32, xs: i32[] }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { H { id: i, xs: [i, i + 1, i + 2] }; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stmtTempFreshCallBumpSrc(n string) string {
	return `struct P { x: i32, y: i32, a:i32, b:i32, c:i32 }
@noinline function mk(a: i32): P { return P { x: a, y: a + 1, a:0, b:0, c:0 }; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stmtTempFreshCallArrBumpSrc(n string) string {
	return `function mk(a: i32): i32[] { return [a, a + 1, a + 2]; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stmtTempLenReceiverBumpSrc(n string) string {
	return `function main(): i32 {
    let s1: string = "ab";
    let s2: string = "cd";
    let acc: i32 = 0;
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { acc = (acc + (s1 + s2).len()) % 251; i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stmtTempFreshCallRcFieldBumpSrc(n string) string {
	return `struct H { id: i32, xs: i32[] }
function mk(a: i32): H { return H { id: a, xs: [a, a + 1, a + 2] }; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stmtTempFreshStrArrBumpSrc(n string) string {
	return `function main(): i32 {
    let p: string = "x";
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { [p + "a", p + "b"]; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const stmtTempTupleDetectorSrc = `function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        (i, i + 1);
        let t: (i32, i32) = (i, i + 2);
        acc = acc + t.0 + t.1;
        i = i + 1;
    }
    if (acc != 40200) { return 999; }
    return __rc_underflow_count();
}`

const stmtTempStructDetectorSrc = `struct P { x: i32, y: i32 }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        P { x: i, y: i + 1 };
        let p: P = P { x: i, y: i + 2 };
        acc = acc + p.x + p.y;
        i = i + 1;
    }
    if (acc != 40200) { return 999; }
    return __rc_underflow_count();
}`

const stmtTempStrConcatDetectorSrc = `function main(): i32 {
    let a: string = "hello"; let b: string = "world";
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        a + b;
        let s: string = a + b;
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 2000) { return 999; }
    return __rc_underflow_count();
}`

const stmtTempRcFieldStructDetectorSrc = `struct H { id: i32, xs: i32[] }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        H { id: i, xs: [i, i + 1, i + 2] };
        let h: H = H { id: i, xs: [i, i + 1, i + 2] };
        acc = acc + h.id + h.xs[0] + h.xs[1] + h.xs[2];
        i = i + 1;
    }
    if (acc != 80200) { return 999; }
    return __rc_underflow_count();
}`

const stmtTempFreshCallDetectorSrc = `struct P { x: i32, y: i32 }
function mk(a: i32): P { return P { x: a, y: a + 1 }; }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        mk(i);
        let p: P = mk(i + 1);
        acc = acc + p.x + p.y;
        i = i + 1;
    }
    if (acc != 40400) { return 999; }
    return __rc_underflow_count();
}`

const stmtTempLenReceiverDetectorSrc = `function main(): i32 {
    let s1: string = "ab" + "c";
    let s2: string = "de";
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        acc = acc + (s1 + s2).len();
        i = i + 1;
    }
    if (acc != 1000) { return 999; }
    if (s1.len() != 3 || s2.len() != 2) { return 998; }
    return __rc_underflow_count();
}`

const stmtTempFreshCallArrDetectorSrc = `function mk(a: i32): i32[] { return [a, a + 1, a + 2]; }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        mk(i);
        let v = mk(i + 1);
        acc = acc + v[0] + v[1] + v[2];
        i = i + 1;
    }
    if (acc != 60900) { return 999; }
    return __rc_underflow_count();
}`

const stmtTempFreshCallRcFieldDetectorSrc = `struct H { id: i32, xs: i32[] }
function mk(a: i32): H { return H { id: a, xs: [a, a + 1, a + 2] }; }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        mk(i);
        let p: H = mk(i + 1);
        acc = acc + p.id + p.xs[0] + p.xs[1] + p.xs[2];
        i = i + 1;
    }
    if (acc != 81000) { return 999; }
    return __rc_underflow_count();
}`

const stmtTempFreshStrArrDetectorSrc = `function main(): i32 {
    let p: string = "x";
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        [p + "a", p + "b"];
        let xs: string[] = [p + "c", p + "d"];
        acc = acc + xs[0].len() + xs[1].len();
        i = i + 1;
    }
    if (acc != 800) { return 999; }
    return __rc_underflow_count();
}`

const stmtTempBorrowedStrArrDetectorSrc = `function main(): i32 {
    let s: string = "hello";
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        [s, s];
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 1000) { return 999; }
    return __rc_underflow_count();
}`

func TestSelfHostStmtTempReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../../compiler", "drivers/asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

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
		name    string
		src     func(n string) string
		unbuilt bool
	}{
		{"scalar-array", stmtTempArrBumpSrc, false},
		{"scalar-tuple", stmtTempTupleBumpSrc, true},
		{"scalar-struct", stmtTempStructBumpSrc, true},
		{"rc-field-struct", stmtTempRcFieldStructBumpSrc, false},
		{"string-concat", stmtTempStrConcatBumpSrc, false},
		{"fresh-call-struct", stmtTempFreshCallBumpSrc, false},
		{"fresh-call-rc-field-struct", stmtTempFreshCallRcFieldBumpSrc, false},
		{"fresh-call-array", stmtTempFreshCallArrBumpSrc, false},
		{"len-receiver-concat", stmtTempLenReceiverBumpSrc, false},
		{"fresh-strarr", stmtTempFreshStrArrBumpSrc, false},
	}
	for _, sh := range fixpointShapes {
		t.Run("fixpoint-bounded/"+sh.name, func(t *testing.T) {
			small := run(t, sh.name+"-50", sh.src("50"))
			large := run(t, sh.name+"-5000", sh.src("5000"))
			if small != large {
				t.Errorf("discarded-%s-temp bump must be bounded: N=50 -> %d, N=5000 -> %d", sh.name, small, large)
			}
			if sh.unbuilt && small != 0 {
				t.Errorf("%s: the literal nothing reads was built: high-water %d, want 0", sh.name, small)
			}
			if !sh.unbuilt && small == 0 {
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
		{"rc-field-struct", stmtTempRcFieldStructDetectorSrc},
		{"string-concat", stmtTempStrConcatDetectorSrc},
		{"fresh-call-struct", stmtTempFreshCallDetectorSrc},
		{"fresh-call-rc-field-struct", stmtTempFreshCallRcFieldDetectorSrc},
		{"fresh-call-array", stmtTempFreshCallArrDetectorSrc},
		{"len-receiver-concat", stmtTempLenReceiverDetectorSrc},
		{"fresh-strarr", stmtTempFreshStrArrDetectorSrc},
		{"borrowed-strarr", stmtTempBorrowedStrArrDetectorSrc},
	}
	for _, sh := range detectorShapes {
		t.Run("no-over-release/"+sh.name, func(t *testing.T) {
			if code := run(t, sh.name+"-detector", sh.src); code != 0 {
				t.Errorf("discarded-%s-temp reclaim: exit=%d (999=value mismatch, >0=over-release)", sh.name, code)
			}
		})
	}
}
