package e2e

import (
	"os/exec"
	"testing"
)

// std/u32 and std/i64 are whole stdlib modules built entirely from scalar
// receiver methods on a wider integer type (min / max / clamp / abs / pow /
// gcd / lcm / to_string / …). Before wide-int receiver-method support landed
// in the self-host checker, each tripped E021 ("method receiver references
// unknown struct") on its very first method declaration and could never go
// through the self-hosted path. These differential gates lock in that
// real-module capability end-to-end (compile through the self-hosted x86-64
// compiler, then run); the inline-method unit case lives in
// self_host_wide_int_recv_test.go. Each program's full transitive closure
// (std/u32 / std/i64 → core/int, for to_string) is vendored so it links.

// min(10,20)=10, max(10,20)=20, clamp(10, 0, 100)=10 → 10+20+10 = 40.
const u32ModMain = `import "std/u32";
function main(): i32 {
    var a: u32 = 10u32;
    var b: u32 = 20u32;
    return ((a.min(b)) as i32) + ((a.max(b)) as i32) + ((a.clamp(0u32, 100u32)) as i32);
}
`

// abs(-5)=5, min(3,7)=3, max(3,7)=7 → 5+3+7 = 15.
const i64ModMain = `import "std/i64";
function main(): i32 {
    var a: i64 = (0 as i64) - (5 as i64);
    var b: i64 = 3 as i64;
    var c: i64 = 7 as i64;
    return ((a.abs()) as i32) + ((b.min(c)) as i32) + ((b.max(c)) as i32);
}
`

func runSelfHostStdModuleX86(t *testing.T, name, mainSrc string, want int) {
	t.Helper()
	gcc, runner, driverBin := buildModloadDriverX86(t)
	asm, progDir := compileSourceModload(t, runner, driverBin, mainSrc)
	progBin := buildBin(t, gcc, progDir, name, asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("%s exited %d, want %d", name, code, want)
	}
}

// TestSelfHostStdU32X86_64 compiles a program importing the real std/u32
// through the self-hosted x86-64 compiler and asserts the runtime result.
func TestSelfHostStdU32X86_64(t *testing.T) {
	runSelfHostStdModuleX86(t, "u32prog", u32ModMain, 40)
}

// TestSelfHostStdI64X86_64 is the std/i64 counterpart.
func TestSelfHostStdI64X86_64(t *testing.T) {
	runSelfHostStdModuleX86(t, "i64prog", i64ModMain, 15)
}
