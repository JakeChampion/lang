package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStructArrayFieldIRX86_64 verifies that a struct with an
// array-of-struct field (`items: P[]`) is admitted to the IR path and that the
// element read `q.items[i].field` recovers the element struct type. use_q builds
// Q{items: [P,P], n} and returns q.items[0].x + q.items[1].y + q.n = 1 + 4 + 5 =
// 10. Without struct-array field support Q is not leaf-safe and the whole module
// bails to the ~35 KB AST runtime; with it the IR output is small — so the size
// check proves the IR path was taken, and the exit code pins the element typing.
func TestSelfHostStructArrayFieldIRX86_64(t *testing.T) {
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

	prog := `struct P { x: i32, y: i32 }
struct Q { items: P[], n: i32 }
function use_q(): i32 {
    var q: Q = Q { items: [P { x: 1, y: 2 }, P { x: 3, y: 4 }], n: 5 };
    return q.items[0].x + q.items[1].y + q.n;
}
function main(): i32 { return use_q(); }`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 || len(asm) > 18000 {
		t.Fatalf("asm is %d bytes — expected small IR output; the struct-array-field module likely bailed to the AST runtime", len(asm))
	}
	progBin := buildBin(t, gcc, dir, "structarray_field", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 10 {
		t.Errorf("exit %d, want 10 (q.items[0].x + q.items[1].y + q.n)", code)
	}
}
