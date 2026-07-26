package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostTupleStructFieldIRX86_64 verifies that a struct with a tuple field
// (`t: (i32, i32)`) is admitted to the IR path and that `p.t.N` recovers the
// element type from the field's tuple type string. use_pt builds Pt{t: (3,4), n:5}
// and returns p.t.0 + p.t.1 + p.n = 3 + 4 + 5 = 12. Without tuple-field support Pt
// is not leaf-safe and the whole module bails to the ~35 KB AST runtime; with it
// the IR output is small — so the size check proves admission, the exit code the
// element typing.
func TestSelfHostTupleStructFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	prog := `struct Pt { t: (i32, i32), n: i32 }
function use_pt(): i32 {
    var p: Pt = Pt { t: (3, 4), n: 5 };
    return p.t.0 + p.t.1 + p.n;
}
function main(): i32 { return use_pt(); }`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 || len(asm) > 18000 {
		t.Fatalf("asm is %d bytes — expected small IR output; the tuple-field module likely bailed to the AST runtime", len(asm))
	}
	progBin := buildBin(t, gcc, dir, "tuple_struct_field", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 12 {
		t.Errorf("exit %d, want 12 (p.t.0 + p.t.1 + p.n)", code)
	}
}
