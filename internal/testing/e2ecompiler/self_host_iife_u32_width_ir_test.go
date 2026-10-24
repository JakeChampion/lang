package e2ecompiler

import (
	"os/exec"
	"testing"
)

var iifeU32WidthIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"u32-literal-arm-past-i32max", `enum E1 { __E1_V0, __E1_V1 }
function gen_f0(p2: u32): u32 { let v0: (i32, i64) = ((((941i32 -| 648i32), (520i64)))); let v1: Option[i32] = (Some((match ((v0.0) /? ((v0.0 % 65i32))) { Some(__chk_v0) => __chk_v0, None => v0.0 }))); return (p2 << (match (v1) { Some(__opt_x1) => 2147484624u32, None => (779u32) })); }
function main(): i32 { return ((gen_f0(5u32) >> 16u32) as i32) & 63i32; }`, 5},
	{"bare-wide-literal-arms-control", `function main(): i32 {
    let v: i64 = (if (true) { 5000000000 } else { 1 });
    return ((v / 1000000000) as i32) & 63i32;
}`, 5},
	{"u64-suffix-arms-control", `function main(): i32 {
    let v: u64 = (if (true) { 18000000000000000000u64 } else { 1u64 });
    return ((v / 1000000000000000000u64) as i32) & 63i32;
}`, 18},
	{"literal-arm-control", `function main(): i32 {
    let w: u32 = (match ((2147484129u32) -? (if (true) { 2147484571u32 } else { 250u32 })) { Some(c) => c, None => 5u32 });
    return (w as i32) & 63i32;
}`, 5},

	{"issue-6400-repro", `function main(): i32 {
    let b: boolean = true;
    let w: u32 = (match ((2147484129u32) -? (if (true) { 2147484571u32 } else { 250u32 })) { Some(c) => c, None => (if (b) { 5u32 } else { 687u32 }) });
    return (w as i32) & 63i32;
}`, 5},
	{"u32-payload-none-arm-taken", `function main(): i32 {
    let b: boolean = true;
    let w: u32 = (match ((100u32) -? (200u32)) { Some(c) => c, None => (if (b) { 5u32 } else { 687u32 }) });
    return (w as i32) & 63i32;
}`, 5},
	{"u32-payload-some-arm-taken", `function main(): i32 {
    let b: boolean = true;
    let w: u32 = (match ((4000000000u32) -? (3999999995u32)) { Some(c) => c, None => (if (b) { 9u32 } else { 7u32 }) });
    return (w as i32) & 63i32;
}`, 5},
	{"u32-payload-above-i32max", `function main(): i32 {
    let b: boolean = true;
    let w: u32 = (match ((4294967290u32) -? (2u32)) { Some(c) => c, None => (if (b) { 9u32 } else { 7u32 }) });
    return (w as i32) & 63i32;
}`, 56},
	{"u32-result-compares-unsigned", `function f(): Option[u32] { return Some(4294967290u32); }
function main(): i32 {
    let b: boolean = true;
    let w: u32 = (match (f()) { Some(c) => c, None => (if (b) { 9u32 } else { 7u32 }) });
    if (w > 2147483648u32) { return 1i32; }
    return 2i32;
}`, 1},
	{"optu32-local-scrutinee", `function main(): i32 {
    let b: boolean = true;
    let o: Option[u32] = None;
    let w: u32 = (match (o) { Some(c) => c, None => (if (b) { 9u32 } else { 7u32 }) });
    return (w as i32) & 63i32;
}`, 9},
	{"i32-spelling-control", `function main(): i32 {
    let b: boolean = true;
    let w: i32 = (match ((100i32) -? (200i32)) { Some(c) => c, None => (if (b) { 5i32 } else { 687i32 }) });
    return w & 63i32;
}`, 28},
}

func TestSelfHostIifeU32WidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range iifeU32WidthIRCases {
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

func TestSelfHostIifeU32WidthIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 IIFE-u32-width gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range iifeU32WidthIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
