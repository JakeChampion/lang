package e2ecompiler

import (
	"os/exec"
	"testing"
)

var valueBlockWidthIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"i64-call-arm", `function widen(v: i64): i64 { return v; }
function main(): i32 {
    let x: i64 = (match ((1i64) /? (0i64)) { Some(w) => w, None => widen(5000000000i64) });
    return (x / 1000000000i64) as i32;
}`, 5},
	{"i64-payload-arm", `function widen(v: i64): i64 { return v; }
function main(): i32 {
    let x: i64 = (match ((9000000000i64) /? (2i64)) { Some(w) => w, None => widen(0i64) });
    return (x / 1000000000i64) as i32;
}`, 4},
	{"u64-call-arm", `function ident_u(v: u64): u64 { return v; }
function main(): i32 {
    let x: u64 = (match ((1u64) /? (0u64)) { Some(w) => w, None => ident_u(9000000000u64) });
    return (x / 1000000000u64) as i32;
}`, 9},
	{"wide-literal-arm-control", `function main(): i32 {
    let x: i64 = (match ((1i64) /? (0i64)) { Some(w) => w, None => 5000000000i64 });
    return (x / 1000000000i64) as i32;
}`, 5},
}

func TestSelfHostValueBlockWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range valueBlockWidthIRCases {
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

func TestSelfHostValueBlockWidthIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 value-block width gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range valueBlockWidthIRCases {
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

const valueBlockLiteralLocalWidthSrc = `function wide(n: u64): u64 { return n / 1000000000u64; }
function f(): i64 { return { let z = 2; z * 1000000000 }; }
function main(): i32 {
    let y: i64 = { let z = 5; z * 1000000000 };
    let w: u64 = wide({ let k = 3; k * 1000000000 });
    return ((y / 1000000000i64) as i32) + 10 * (w as i32) + 40 * ((f() / 1000000000i64) as i32);
}
`

func TestSelfHostValueBlockLiteralLocalWidth(t *testing.T) {
	if want := interpExit(t, buildLangBinForInterp(t), valueBlockLiteralLocalWidthSrc); want != 115 {
		t.Fatalf("interpreter exit %d, want 115", want)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, exit := cli.exitOf(t, valueBlockLiteralLocalWidthSrc, target); exit != 115 {
				t.Errorf("exit = %d, want 115\n%s", exit, stderr)
			}
		})
	}
}

const wideLiteralLocalWidthSrc = `function giga(n: u64): u64 { return n / 1000000000u64; }
function exa(n: u64): u64 { return n / 1000000000000000000u64; }
function main(): i32 {
    let k = 3;
    let w: u64 = giga(k * 3000000000);
    let v: u64 = giga({ let j = 5; j * 3000000000 });
    let two = 2;
    let big: u64 = exa(two * 7000000000000000000);
    let m = -2147483648;
    return (w as i32) + (v as i32) + (big as i32) + (m + 2147483647 + 1);
}
`

func TestSelfHostWideLiteralLocalWidth(t *testing.T) {
	if want := interpExit(t, buildLangBinForInterp(t), wideLiteralLocalWidthSrc); want != 38 {
		t.Fatalf("interpreter exit %d, want 38", want)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, exit := cli.exitOf(t, wideLiteralLocalWidthSrc, target); exit != 38 {
				t.Errorf("exit = %d, want 38\n%s", exit, stderr)
			}
		})
	}
}
