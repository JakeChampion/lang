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

// TestSelfHostIRMethodReturnEligible locks in the IR-coverage widening for
// methods returning structs / tuples. The struct/tuple return registries used
// to record only free functions, so `var p = obj.mk()` for a struct/tuple-
// returning method fell back to AST (the call site couldn't type p, and a
// struct method-call result's `p.x` bails). It compiles a self-host probe that
// calls asm_ir.all_eligible on method-returning programs and encodes the
// per-case results in the exit code (a*10 + b == 11 when both are eligible).
func TestSelfHostIRMethodReturnEligible(t *testing.T) {
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
	probe := `import "./lexer";
import "./parser";
import "./asm_ir";

function elig(src: string): i32 {
    var mod: parser.Module = parser.module_with_builtins(parser.parse_module(lexer.tokenize(src)));
    if (asm_ir.all_eligible(mod)) { return 1; }
    return 0;
}

function main(): i32 {
    var a: i32 = elig("struct P { x: i32, y: i32 } struct B { } function (b: B) mk(): P { return P { x: 3, y: 4 }; } function main(): i32 { var b = B { }; var p = b.mk(); return p.x * 10 + p.y; }");
    var b: i32 = elig("struct B { } function (b: B) pair(): (string, i32) { return (\"hi\", 5); } function main(): i32 { var b = B { }; var (s, n) = b.pair(); return s.len() + n; }");
    return a * 10 + b;
}
`
	probePath := filepath.Join(dir, "zz_mret_probe.fern")
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
	if got := cmd.ProcessState.ExitCode(); got != 11 {
		t.Errorf("method-returning IR eligibility = %d, want 11 (each digit is one case's all_eligible)", got)
	}
}
