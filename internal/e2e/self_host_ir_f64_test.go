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

// TestSelfHostIRF64Eligible locks in the IR-coverage widening for f64: programs
// using f64 locals + arithmetic + comparison used to bail every float literal
// to the AST path. It compiles a self-host probe that calls asm_ir.all_eligible
// on f64 programs and bit-packs the per-case results into the exit code (15 ==
// all four eligible). A function with f64 in its SIGNATURE still bails (that's a
// later slice), so case (d) — an f64-returning function — must be INELIGIBLE,
// pinning the signature boundary.
func TestSelfHostIRF64Eligible(t *testing.T) {
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
	// (a) arithmetic+compare, (b) negation, (c) mixed-precedence chain are all
	// eligible (bits 8/4/2); (d) an f64 SIGNATURE is NOT (bit 1 stays 0) — so
	// the want is 8+4+2+0 = 14.
	probe := `import "./lexer";
import "./parser";
import "./asm_ir";

function elig(src: string): i32 {
    var mod: parser.Module = parser.module_with_builtins(parser.parse_module(lexer.tokenize(src)));
    if (asm_ir.all_eligible(mod)) { return 1; }
    return 0;
}

function main(): i32 {
    var a: i32 = elig("function main(): i32 { var x: f64 = 1.5; var y: f64 = 2.25; var z: f64 = x + y; if (z > 3.0) { return 7; } return 0; }");
    var b: i32 = elig("function main(): i32 { var x: f64 = 3.0; var y: f64 = -x; if (y <= 0.0) { return 4; } return 0; }");
    var c: i32 = elig("function main(): i32 { var x: f64 = 1.0; var y: f64 = 2.0; var z: f64 = 3.0; var r: f64 = x + y * z; if (r >= 7.0) { return 6; } return 0; }");
    var d: i32 = elig("function half(): f64 { return 0.5; } function main(): i32 { return 0; }");
    return a * 8 + b * 4 + c * 2 + d;
}
`
	probePath := filepath.Join(dir, "zz_f64_probe.fern")
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
	if got := cmd.ProcessState.ExitCode(); got != 14 {
		t.Errorf("f64 IR eligibility = %d, want 14 (locals eligible: bits 8/4/2; an f64 signature stays bit 1 = 0)", got)
	}
}
