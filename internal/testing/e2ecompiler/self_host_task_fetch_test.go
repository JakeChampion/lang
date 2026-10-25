package e2ecompiler

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostTaskFetchFanoutX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	port := e2eharness.StartBodyUpstream(t, []byte("hello-world"))
	asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.TaskFetchFanoutSource(port))
	cmd := binCmd(runner, buildBin(t, gcc, progDir, "taskfetchfanout", asm))
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("gather_tasks over fetch.send exit = %d, want 42", code)
	}
}

func TestSelfHostTaskFetchLargeBodyX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	const bodyLen = 10000
	port := e2eharness.StartBodyUpstream(t, bytes.Repeat([]byte("A"), bodyLen))
	asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.TaskFetchLargeBodySource(port, bodyLen))
	cmd := binCmd(runner, buildBin(t, gcc, progDir, "taskfetchbig", asm))
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("large-body fetch exit = %d, want 42 (truncated multi-chunk read?)", code)
	}
}
