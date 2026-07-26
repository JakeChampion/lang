package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostEnumStructFieldIRX86_64 verifies that a struct with a nominal-enum
// field (`s: Shape`) is admitted to the IR path and that `match (t.s)` reads the
// enum box back through the field and discriminates its variants. use_tagged
// builds Tagged{s: Rect(7), n: 5} and returns the matched payload + t.n = 7 + 5 =
// 12. Enums are leak-only on the IR path (the exit sweep never frees an enum
// box), so an enum-typed field leaks with the struct like a string / Option /
// tuple field — no RC, no aliasing bail. Without enum-field support Tagged is not
// leaf-safe and the whole module bails to the ~35 KB AST runtime; with it the IR
// output is small, so the size check proves admission and the exit code the
// variant discrimination.
//
// The construction uses the BARE variant forms (`Rect(7)`, `Circle`), which lower
// correctly. The qualified form (`Shape.Rect(7)`) is deliberately avoided: it is
// a separate, pre-existing limitation of the self-host enum lowering (it bails to
// AST in field position rather than miscompiling), orthogonal to field admission.
func TestSelfHostEnumStructFieldIRX86_64(t *testing.T) {
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

	prog := `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { s: Shape, n: i32 }
function use_tagged(): i32 {
    var t: Tagged = Tagged { s: Rect(7), n: 5 };
    var r: i32 = 0;
    match (t.s) {
        Circle => { r = 1; },
        Square => { r = 2; },
        Rect(w) => { r = w; },
    }
    return r + t.n;
}
function main(): i32 { return use_tagged(); }`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 || len(asm) > 18000 {
		t.Fatalf("asm is %d bytes — expected small IR output; the enum-field module likely bailed to the AST runtime", len(asm))
	}
	progBin := buildBin(t, gcc, dir, "enum_struct_field", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 12 {
		t.Errorf("exit %d, want 12 (matched Rect payload + t.n)", code)
	}
}
