package e2ecompiler

import (
	"os/exec"
	"testing"
)

const wideRecvMain = `function (n: i64) twice(): i64 { return n + n; }
function (n: u32) plus1(): u32 { return n + 1; }
function (n: u64) dbl(): u64 { return n * (2 as u64); }
function main(): i32 {
    let a: i64 = 18;
    let b: u32 = 3;
    let c: u64 = 1;
    return ((a.twice()) as i32) + ((b.plus1()) as i32) + ((c.dbl()) as i32);
}
`

func TestSelfHostWideIntReceiverMethodsX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	asm, progDir := compileSourceModload(t, runner, driverBin, wideRecvMain)
	progBin := buildBin(t, gcc, progDir, "widerecv", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("wide-int receiver methods exited %d, want 42", code)
	}
}
