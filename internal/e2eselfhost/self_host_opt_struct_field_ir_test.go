package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostOptStructFieldIRX86_64 verifies that a struct with Option[T] /
// Result[T,E] fields (leak-safe scalar payloads) is admitted to the IR path and
// that `match (b.opt)` / `match (b.res)` recover the Option/Result type from the
// field. use_box builds Box{opt: Some(7), res: Ok(3), n: 5}, matches both fields,
// and returns 5 + 7 + 3 = 15. Without Option/Result-field support Box is not
// leaf-safe and the whole module bails to the ~35 KB AST runtime; with it the IR
// output is small — so the size check proves admission, the exit code the matches.
func TestSelfHostOptStructFieldIRX86_64(t *testing.T) {
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

	prog := `struct Box { opt: Option[i32], res: Result[i32, string], n: i32 }
function use_box(): i32 {
    var b: Box = Box { opt: Some(7), res: Ok(3), n: 5 };
    var sum: i32 = b.n;
    match (b.opt) { Some(x) => { sum = sum + x; }, None => { sum = sum + 100; } }
    match (b.res) { Ok(y) => { sum = sum + y; }, Err(e) => { sum = sum + 200; } }
    return sum;
}
function main(): i32 { return use_box(); }`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 || len(asm) > 18000 {
		t.Fatalf("asm is %d bytes — expected small IR output; the Option/Result-field module likely bailed to the AST runtime", len(asm))
	}
	progBin := buildBin(t, gcc, dir, "opt_struct_field", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 15 {
		t.Errorf("exit %d, want 15 (b.n + Some payload + Ok payload)", code)
	}
}
