package e2ecompiler

import (
	"os/exec"
	"testing"
)

const arrStrJoinScaleSrc = `function main(): i32 {
    let parts: string[] = [];
    let i: i32 = 0;
    while (i < 100000) { parts = parts.append("item"); i = i + 1; }
    let j: string = parts.join(",");
    if (j.len() != 499999) { return 1; }
    if (j[0] as i32 != 105) { return 2; }
    if (j[4] as i32 != 44) { return 3; }
    return 42;
}`

func TestSelfHostArrStrJoinScaleX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(arrStrJoinScaleSrc))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "arr-str-join-scale", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("joined 100k parts exited %d, want 42 (137 is the OOM the quadratic form died of)", code)
	}
}
