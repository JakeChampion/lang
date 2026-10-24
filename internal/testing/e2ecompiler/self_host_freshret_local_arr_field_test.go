package e2ecompiler

import (
	"os/exec"
	"testing"
)

var selfHostFreshRetLocalArrCases = []struct {
	name string
	src  string
	exit int
}{
	{"local-built-array-field", `struct Q { xs: i32[], pos: i32 }
function mkq(k: i32): Q { let xs: i32[] = [k, 8]; return Q { xs: xs, pos: 1 }; }
function churn(k: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) { let q: Q = mkq(i); t = t + q.pos + q.xs[1]; i = i + 1; }
    return t;
}
function main(): i32 {
    let w: i32 = churn(100);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(100);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (w != 900 || x != 900) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    return 0;
}`, 0},
	{"accumulator-producer", `struct Q { xs: i32[], pos: i32 }
function build(n: i32): Q {
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < n) { xs = xs.append(i * 2); i = i + 1; }
    return Q { xs: xs, pos: n };
}
function churn(k: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) { let q: Q = build(4); t = t + q.xs[3] + q.pos; i = i + 1; }
    return t;
}
function main(): i32 {
    let w: i32 = churn(100);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(100);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (w != 1000 || x != 1000) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    return 0;
}`, 0},
	{"producer-call-array-field", `struct Q { xs: i32[], pos: i32 }
function mk(n: i32): i32[] { let out: i32[] = []; let i: i32 = 0; while (i < 4) { out = out.append(n + i); i = i + 1; } return out; }
function mkq(k: i32): Q { let xs: i32[] = mk(k); return Q { xs: xs, pos: 1 }; }
function churn(k: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) { let q: Q = mkq(i); t = t + q.pos + q.xs[3]; i = i + 1; }
    return t;
}
function main(): i32 {
    let w: i32 = churn(100);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(100);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (w != 5350 || x != 5350) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    return 0;
}`, 0},
	{"passthru-producer-declined", `struct Q { xs: i32[], pos: i32 }
@noinline function passthru(p: i32[]): i32[] { return p; }
function mkq(p: i32[]): Q { let xs: i32[] = passthru(p); return Q { xs: xs, pos: 1 }; }
function work(k: i32): i32 {
    let base: i32[] = [11, 22, 33];
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) {
        let q: Q = mkq(base);
        t = t + q.pos;
        let churn: i32[] = [i, i + 1, i + 2];
        if (churn[0] != i) { return 96; }
        i = i + 1;
    }
    if (base[0] != 11 || base[1] != 22 || base[2] != 33) { return 97; }
    return t;
}
function main(): i32 {
    if (work(60) != 60) { return 95; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"param-array-field-declined", `struct Q { xs: i32[], pos: i32 }
function mk_from_param(p: i32[]): Q { return Q { xs: p, pos: 1 }; }
function work(k: i32): i32 {
    let base: i32[] = [11, 22, 33];
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) {
        let q: Q = mk_from_param(base);
        t = t + q.pos;
        let churn: i32[] = [i, i + 1, i + 2];
        if (churn[0] != i) { return 96; }
        i = i + 1;
    }
    if (base[0] != 11 || base[1] != 22 || base[2] != 33) { return 97; }
    return t;
}
function main(): i32 {
    if (work(60) != 60) { return 95; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"direct-producer-call-array-field", `struct Q { xs: i32[], pos: i32 }
function nums_of(k: i32): i32[] { let out: i32[] = []; let i: i32 = 0; while (i < 4) { out = out.append(k + i); i = i + 1; } return out; }
function mkq(k: i32): Q { return Q { xs: nums_of(k), pos: 1 }; }
function churn(k: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) { let q: Q = mkq(i); t = t + q.pos + q.xs.len(); i = i + 1; }
    return t;
}
function main(): i32 {
    let w: i32 = churn(100);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(100);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (w != 500 || x != 500) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    return 0;
}`, 0},
	{"strarr-producer-call-array-field", `struct S { deps: string[], pos: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function mks(pre: string): S { return S { deps: deps_of(pre), pos: 1 }; }
function churn(k: i32): i32 {
    let pre: string = "ab";
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) { let s: S = mks(pre); t = (t + s.pos + s.deps.len()) % 251; i = i + 1; }
    return t;
}
function main(): i32 {
    let w0: i32 = churn(2000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(2000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (w0 != x) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    return 0;
}`, 0},
	{"producer-call-param-field-retained", `struct P { deps: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fill(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function mkp(deps: string[]): P { return P { deps: deps }; }
function work(k: i32): i32 {
    let pre: string = "ab";
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) {
        let live: string[] = deps_of(pre);
        let p: P = mkp(live);
        let junk: string = fill(20);
        if (junk.len() < 0) { return 96; }
        if (p.deps.len() != 3) { return 94; }
        if (live[0].len() < 43) { return 93; }
        if (live[2].len() < 43) { return 92; }
        t = t + 1;
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    if (work(2000) != 2000) { return 95; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"producer-name-shadowed-retained", `struct D { deps: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fill(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function mkd(pre: string): D { let deps_of: string[] = [w(pre)]; return D { deps: deps_of }; }
function work(k: i32): i32 {
    let pre: string = "ab";
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) {
        let d: D = mkd(pre);
        let junk: string = fill(20);
        if (junk.len() < 0) { return 96; }
        if (d.deps.len() != 1) { return 94; }
        t = t + 1;
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    if (work(2000) != 2000) { return 95; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"one-local-two-fields-declined", `struct Q { xs: i32[], ys: i32[], pos: i32 }
function mk2(k: i32): Q { let xs: i32[] = [k, 8]; return Q { xs: xs, ys: xs, pos: 1 }; }
function work(k: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) {
        let q: Q = mk2(i);
        t = t + q.xs[1] + q.ys[1];
        let churn: i32[] = [i, i + 1, i + 2];
        if (churn[0] != i) { return 96; }
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    if (work(60) != 960) { return 95; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

func TestSelfHostFreshRetLocalArrFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostFreshRetLocalArrCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d (98 = the leak gate, 97 = a value read back wrong, 99 = over-release)", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostFreshRetLocalArrFieldIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostFreshRetLocalArrCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
