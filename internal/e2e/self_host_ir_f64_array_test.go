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

// TestSelfHostIRF64ArrayEligible locks in that f64 arrays now lower through the
// IR. f64 array literals allocate an 8-byte-stride buffer (arr_make width 64);
// a[i] reads/writes an 8-byte f64 (arr_get/arr_set width 64 → f64.load/store on
// wasm; the register backends already used 8-byte slots). The slot binding
// tracks f64-array-ness (local_is_f64arr) so a[i] types as f64. This compiles a
// self-host probe that bit-packs all_eligible results into the exit code:
//   (a) f64 array literal + indexed read  → ELIGIBLE (1)
//   (b) f64 array indexed write a[i] = v  → ELIGIBLE (1)
//   (c) f64[] param + indexed read        → ELIGIBLE (1)
//   (d) f64[]-RETURNING function          → INELIGIBLE (0)  [call site can't
//                                            recover element width — bailed]
// Expected: a*8 + b*4 + c*2 + d == 8 + 4 + 2 + 0 == 14.
func TestSelfHostIRF64ArrayEligible(t *testing.T) {
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
    var a: i32 = elig("function main(): i32 { var a: f64[] = [1.5, 2.5]; var x: f64 = a[0] + a[1]; if (x > 3.0) { return 7; } return 0; }");
    var b: i32 = elig("function main(): i32 { var a: f64[] = [1.0, 2.0]; a[1] = 5.5; var x: f64 = a[0] + a[1]; if (x > 6.0) { return 8; } return 0; }");
    var c: i32 = elig("function sum(a: f64[]): f64 { return a[0] + a[1]; } function main(): i32 { var arr: f64[] = [2.5, 4.0]; var r: f64 = sum(arr); if (r > 6.0) { return 5; } return 0; }");
    var d: i32 = elig("function mk(): f64[] { return [1.5, 2.5]; } function main(): i32 { var a: f64[] = mk(); if (a[0] > 1.0) { return 4; } return 0; }");
    return a * 8 + b * 4 + c * 2 + d;
}
`
	probePath := filepath.Join(dir, "zz_f64array_probe.fern")
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
		t.Errorf("f64-array IR eligibility = %d, want 14 (literal/write/param eligible; f64[] return ineligible)", got)
	}
}
