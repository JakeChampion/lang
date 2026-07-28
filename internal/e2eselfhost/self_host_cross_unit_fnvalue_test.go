package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostCrossUnitFnValue pins the cross-unit fn-value miscompile on the
// per-module IR path (#5698), found while making the flagship std/http +
// std/tcp handler compile: a function passed as a VALUE into another module and
// invoked there segfaults.
//
// The caller boxes a fn-value argument only when it can see that the callee's
// parameter is fn-typed, and that lookup is per-MODULE. Within one module the
// caller emits the box:
//
//	leaq __fn_main$wrap0(%rip), %rax
//	movq $1, %rdi ; call __fern_arr_box     ; [len=1][fn_addr]
//
// Across a unit boundary it emits the bare address instead:
//
//	leaq __fn_dbl(%rip), %rax               ; unboxed
//
// while the callee — compiled with no more knowledge than its own signature —
// always dereferences a box (a bounds-checked element read). So it reads slot 1
// of a code address and jumps through garbage.
//
// Fixed by threading a whole-program signature view through the lift pass
// (irlower.lift_lambdas_view): the caller now sees the sibling module's
// declaration, so it boxes the argument exactly as it does for a local callee.
func TestSelfHostCrossUnitFnValue(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	// std/http is imported only to push the program over the 512-function
	// merged-IR budget, so it takes the per-module concat path. The defect
	// itself needs nothing but two modules and one fn value.
	files := map[string]string{
		"apply.fern": `import "std/http";
pub function apply_i32(f: (i32) => i32, n: i32): i32 { return f(n); }
`,
		"main.fern": `import "std/http";
import "./apply";

function dbl(n: i32): i32 { return n * 2; }

function main(): i32 {
    return apply.apply_i32(dbl, 21);
}
`,
	}
	progDir := t.TempDir()
	bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	asm := string(runDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern")))
	if !strings.Contains(asm, ".Lir") {
		t.Fatal("program did not route through the per-module IR path")
	}
	bin := buildBin(t, gcc, progDir, "cross_unit_fnvalue", asm)
	if _, exit := runBin(binCmd(runner, bin), ""); exit != 42 {
		t.Errorf("exit = %d, want 42 (dbl(21) through a cross-unit fn value)", exit)
	}
}
