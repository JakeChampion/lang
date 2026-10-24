package e2ecompiler

import (
	"os/exec"
	"testing"
)

var tupleMethodElemCases = []struct {
	name string
	src  string
	exit int
}{
	{"method-arm-in-tuple-elem", `struct Pair { fst: i32, snd: i32 } function (p: Pair) sum(): i32 { return p.snd; } function gen(p1: Pair, c: boolean): (i32, i64) { return ((if (c) { p1.sum() } else { 6i32 }), 165i64); } function main(): i32 { let t: (i32, i64) = gen(Pair { fst: 1i32, snd: 7i32 }, true); return (t.0 + (t.1 as i32)) & 63i32; }`, 44},
	{"method-arms-both", `struct Pair { fst: i32, snd: i32 } function (p: Pair) sum(): i32 { return p.snd; } function gen(p1: Pair, c: boolean): (i32, i64) { return ((if (c) { p1.sum() } else { p1.fst }), 165i64); } function main(): i32 { let t: (i32, i64) = gen(Pair { fst: 1i32, snd: 7i32 }, false); return (t.0 + (t.1 as i32)) & 63i32; }`, 38},
	{"method-arm-all-i32-tuple", `struct Pair { fst: i32, snd: i32 } function (p: Pair) sum(): i32 { return p.snd; } function gen(p1: Pair, c: boolean): (i32, i32) { return ((if (c) { p1.sum() } else { 6i32 }), 165i32); } function main(): i32 { let t: (i32, i32) = gen(Pair { fst: 1i32, snd: 7i32 }, true); return (t.0 + t.1) & 63i32; }`, 44},
	{"len-arm-string-receiver", `function gen(p0: string, c: boolean): (i32, i64) { return ((if (c) { p0.len() } else { 6i32 }), 165i64); } function main(): i32 { let t: (i32, i64) = gen("abcd", true); return (t.0 + (t.1 as i32)) & 63i32; }`, 41},
	{"len-arm-array-receiver", `function gen(xs: i32[], c: boolean): (i32, i64) { return ((if (c) { xs.len() } else { 6i32 }), 165i64); } function main(): i32 { let t: (i32, i64) = gen([1i32, 2i32, 3i32], true); return (t.0 + (t.1 as i32)) & 63i32; }`, 40},
	{"free-fn-elem-unchanged", `function f(n: i32): i32 { return n + 1i32; } function gen(c: boolean): (i32, i64) { return ((if (c) { f(6i32) } else { 6i32 }), 165i64); } function main(): i32 { let t: (i32, i64) = gen(true); return (t.0 + (t.1 as i32)) & 63i32; }`, 44},
	{"literal-arms-unchanged", `function gen(c: boolean): (i32, i64) { return ((if (c) { 7i32 } else { 6i32 }), 165i64); } function main(): i32 { let t: (i32, i64) = gen(true); return (t.0 + (t.1 as i32)) & 63i32; }`, 44},
}

func TestSelfHostTupleMethodElemIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleMethodElemCases {
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
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostTupleMethodElemIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleMethodElemCases {
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
