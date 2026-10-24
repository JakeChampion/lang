package e2ecompiler

import (
	"os/exec"
	"testing"
)

const u32ModMain = `import "std/u32";
function main(): i32 {
    let a: u32 = 10u32;
    let b: u32 = 20u32;
    return ((a.min(b)) as i32) + ((a.max(b)) as i32) + ((a.clamp(0u32, 100u32)) as i32);
}
`

const i64ModMain = `import "std/i64";
function main(): i32 {
    let a: i64 = (0 as i64) - (5 as i64);
    let b: i64 = 3 as i64;
    let c: i64 = 7 as i64;
    return ((a.abs()) as i32) + ((b.min(c)) as i32) + ((b.max(c)) as i32);
}
`

const u64ModMain = `import "std/u64";
function main(): i32 {
    let a: u64 = 10u64;
    let b: u64 = 20u64;
    return ((a.min(b)) as i32) + ((a.max(b)) as i32) + ((a.clamp(0u64, 100u64)) as i32);
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

func TestSelfHostStdU32X86_64(t *testing.T) {
	runSelfHostStdModuleX86(t, "u32prog", u32ModMain, 40)
}

func TestSelfHostStdI64X86_64(t *testing.T) {
	runSelfHostStdModuleX86(t, "i64prog", i64ModMain, 15)
}

func TestSelfHostStdU64X86_64(t *testing.T) {
	runSelfHostStdModuleX86(t, "u64prog", u64ModMain, 40)
}
