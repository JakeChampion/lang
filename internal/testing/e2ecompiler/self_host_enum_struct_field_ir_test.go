package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostEnumStructFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	prog := `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { s: Shape, n: i32 }
function use_tagged(): i32 {
    let t: Tagged = Tagged { s: Rect(7), n: 5 };
    let r: i32 = 0;
    match (t.s) {
        Circle => { r = 1; },
        Square => { r = 2; },
        Rect(w) => { r = w; },
    }
    return r + t.n;
}
function main(): i32 { return use_tagged(); }`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("driver produced no asm")
	}
	progBin := buildBin(t, gcc, dir, "enum_struct_field", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 12 {
		t.Errorf("exit %d, want 12 (matched Rect payload + t.n)", code)
	}
}
