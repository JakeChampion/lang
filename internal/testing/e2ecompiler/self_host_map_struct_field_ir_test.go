package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapStructFieldIRX86_64 verifies that a struct with a Map field
// (`m: Map[string, i32]`) lowers through the IR path and that the map
// round-trips through the field. Reading the field into a
// `let got: Map[K, V] = c.m` local keeps the map type from the annotation so
// get_or dispatches as a map op.
//
// f builds mm{"a": 3}, stores it in Cache{m: mm, n: 4}, reads c.m back into
// got, and returns got.get_or("a", 0) + c.n = 3 + 4 = 7; the exit code proves
// the round-trip.
func TestSelfHostMapStructFieldIRX86_64(t *testing.T) {
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

	prog := `struct Cache { m: Map[string, i32], n: i32 }
function f(): i32 {
    let mm: Map[string, i32] = map_new(0);
    mm = mm.insert("a", 3);
    let c: Cache = Cache { m: mm, n: 4 };
    let got: Map[string, i32] = c.m;
    return got.get_or("a", 0) + c.n;
}
function main(): i32 { return f(); }`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("driver produced no asm")
	}
	progBin := buildBin(t, gcc, dir, "map_struct_field", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("exit %d, want 7 (c.m[\"a\"] + c.n)", code)
	}
}
