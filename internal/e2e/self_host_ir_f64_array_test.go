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

// TestSelfHostIRF64ArrayBails locks in that an f64 array LITERAL bails the IR
// path (the program falls back to the AST backend). f64 *locals*, arithmetic,
// casts and struct fields all lower (#2717–#2721), and that made `[1.5, 2.5]`
// eligible — but the wasm array layout still uses a 4-byte element stride
// (correct for i32 / wasm32 pointers, truncating for an 8-byte f64), so an f64
// array would miscompile there. Until array elements widen to 8-byte slots on
// wasm (a separate, stride-wide change), f64 array literals must stay
// ineligible. This compiles a self-host probe that bit-packs per-case
// all_eligible results into the exit code:
//   (a) f64 array literal  → must be INELIGIBLE (0)
//   (b) i32 array literal   → must stay ELIGIBLE (1)  [guards the guard]
//   (c) f64 scalars (no arr)→ must stay ELIGIBLE (1)  [#2717 still lowers]
// Expected: a*4 + b*2 + c == 0 + 2 + 1 == 3.
func TestSelfHostIRF64ArrayBails(t *testing.T) {
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
    var b: i32 = elig("function main(): i32 { var a: i32[] = [10, 20]; return a[0] + a[1]; }");
    var c: i32 = elig("function main(): i32 { var x: f64 = 1.5 + 2.5; if (x > 3.0) { return 7; } return 0; }");
    return a * 4 + b * 2 + c;
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
	if got := cmd.ProcessState.ExitCode(); got != 3 {
		t.Errorf("f64-array IR eligibility = %d, want 3 (f64 arr ineligible; i32 arr + f64 scalars eligible)", got)
	}
}
