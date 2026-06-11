package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostIRTupleReturnEligible LOCKS IN the IR-coverage widening for
// tuple-returning functions. The differential gate (TestSelfHostAsmIRPath) can't
// prove a program actually takes the IR path, because irlower is built to emit
// asm byte-identical to the AST backend for the overlapping subset — so AST==IR
// holds whether or not the IR path was used. This test instead asserts
// asm_ir.all_eligible() directly: it compiles a self-host probe that calls
// all_eligible on tuple-returning programs and encodes the per-case results in
// the exit code. Before tuple-returning functions were lowered, lower_func
// bailed every `(...)` return, so all three cases would be ineligible (exit 0).
func TestSelfHostIRTupleReturnEligible(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Probe: all_eligible must be true for tuple-returning programs of three
	// shapes — destructure-from-call, string+i32 element tuple, and `.N` access
	// on a returned tuple local. Exit code = a*100 + b*10 + c (each 0/1), so a
	// fully-covered run exits 111.
	probe := `import "./lexer";
import "./parser";
import "./asm_ir";

function elig(src: string): i32 {
    var mod: parser.Module = parser.module_with_builtins(parser.parse_module(lexer.tokenize(src)));
    if (asm_ir.all_eligible(mod)) { return 1; }
    return 0;
}

function main(): i32 {
    var a: i32 = elig("function three(): (i32, i32, i32) { return (4, 5, 6); } function main(): i32 { var (a, b, c) = three(); return a + b + c; }");
    var b: i32 = elig("function pair(): (string, i32) { return (\"hi\", 5); } function main(): i32 { var (s, n) = pair(); return s.len() + n; }");
    var c: i32 = elig("function trip(): (i32, i32, i32) { return (1, 2, 3); } function main(): i32 { var t = trip(); return t.0 + t.1 + t.2; }");
    return a * 100 + b * 10 + c;
}
`
	probePath := filepath.Join(dir, "zz_elig_probe.fern")
	if err := os.WriteFile(probePath, []byte(probe), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	prog, _, err := modload.Load(probePath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	asmPath := filepath.Join(dir, "probe.s")
	binPath := filepath.Join(dir, "probe")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
	}
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally")
	}
	if got := cmd.ProcessState.ExitCode(); got != 111 {
		t.Errorf("tuple-returning IR eligibility = %d, want 111 (each digit is one case's all_eligible)", got)
	}
}
