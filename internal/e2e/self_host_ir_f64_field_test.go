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

// TestSelfHostIRF64FieldEligible locks in the IR-coverage widening for f64
// struct fields: a struct with an f64 field is now leaf-safe and lowers through
// the IR (register backends already used 8-byte field slots; wasm widened its
// field layout to 8-byte stride with per-field f64/i32 load-store). It compiles
// a self-host probe that calls asm_ir.all_eligible and bit-packs per-case
// results into the exit code: (a) read, (b) mixed i32/f64 fields, (c) field
// write — all eligible → 4+2+1 = 7.
func TestSelfHostIRF64FieldEligible(t *testing.T) {
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
    var a: i32 = elig("struct P { x: f64, n: i32 } function main(): i32 { var p = P { x: 3.5, n: 2 }; var y: f64 = p.x + 1.0; if (y > 4.0) { return p.n; } return 0; }");
    var b: i32 = elig("struct V { a: i32, d: f64, b: i32 } function main(): i32 { var v = V { a: 1, d: 2.5, b: 3 }; var s: f64 = v.d * 2.0; if (s > 4.0) { return v.a + v.b; } return 0; }");
    var c: i32 = elig("struct P { x: f64, n: i32 } function main(): i32 { var p = P { x: 1.0, n: 4 }; p.x = 5.5; if (p.x > 5.0) { return p.n; } return 0; }");
    return a * 4 + b * 2 + c;
}
`
	probePath := filepath.Join(dir, "zz_f64field_probe.fern")
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
	if got := cmd.ProcessState.ExitCode(); got != 7 {
		t.Errorf("f64-struct-field IR eligibility = %d, want 7 (read/mixed/write all eligible)", got)
	}
}
