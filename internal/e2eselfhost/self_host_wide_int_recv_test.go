package e2eselfhost

import (
	"os/exec"
	"testing"
)

// Wide-int receiver methods through the self-hosted compiler. Before this,
// the self-host checker only recognised i32 / boolean / string / f64 as
// primitive receiver types: a method declared on i64 / u32 / u64 tripped
// the E021 receiver-type gate, and once that gate was relaxed, dispatch
// still missed because every integer collapsed to the
// "i32" type-name so the call site couldn't recover the width to match the
// method's declared receiver. The checker now carries integer width +
// signedness on its TypeI32 (staying width-LENIENT for assignability but
// width-AWARE for the dispatch key), which unblocks scalar receiver methods
// on the wider integer types — the shape std/i64, std/u32 and std/u64 are
// entirely built from.
//
// twice()/plus1()/dbl() are declared inline (no stdlib import) so the test
// pins the language feature itself: 18*2 + (3+1) + (1*2) = 36 + 4 + 2 = 42.
const wideRecvMain = `function (n: i64) twice(): i64 { return n + n; }
function (n: u32) plus1(): u32 { return n + 1; }
function (n: u64) dbl(): u64 { return n * (2 as u64); }
function main(): i32 {
    var a: i64 = 18;
    var b: u32 = 3;
    var c: u64 = 1;
    return ((a.twice()) as i32) + ((b.plus1()) as i32) + ((c.dbl()) as i32);
}
`

// TestSelfHostWideIntReceiverMethodsX86_64 compiles wideRecvMain through the
// self-hosted x86-64 compiler and asserts the runtime exit code (42).
func TestSelfHostWideIntReceiverMethodsX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	asm, progDir := compileStdProgModload(t, runner, driverBin, []string{}, wideRecvMain)
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
