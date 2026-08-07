package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostQualifiedVariantIRX86_64 verifies that the QUALIFIED enum-variant
// surface forms — qualified construction (`Color.Custom(7)`, `Color.Red`) and
// qualified match patterns (`Color.Custom(v) =>`, `Color.Red =>`) — lower through
// the self-host IR path and run correctly.
//
// The native compiler accepts both the bare (`Custom(7)` / `Custom(v) =>`) and
// qualified spellings, and the self-host IR path must handle both: a qualified
// construction that makes the whole module IR-ineligible falls back to the AST
// emitter, which mis-lowers it (`# unresolved ident: Color`) and
// produces a binary that crashes — a native-vs-self-host gap and a miscompile.
// With qualified construction + qualified patterns lowered in irlower.fern, the
// module is IR-eligible and lowers correctly.
//
// use_box builds Box{c: Color.Custom(7), n: 5} and matches on the field with
// qualified patterns, returning the payload + n = 7 + 5 = 12. A size check proves
// the small IR path was taken (a bail to the ~35 KB AST runtime would be far
// larger and, here, crash); the exit code pins construction + pattern + payload
// binding through the field.
func TestSelfHostQualifiedVariantIRX86_64(t *testing.T) {
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

	prog := `enum Color { Red, Green, Custom(i32) }
struct Box { c: Color, n: i32 }
function use_box(): i32 {
    var b: Box = Box { c: Color.Custom(7), n: 5 };
    var r: i32 = 0;
    match (b.c) {
        Color.Red => { r = 1; },
        Color.Green => { r = 2; },
        Color.Custom(v) => { r = v; },
    }
    return r + b.n;
}
function main(): i32 { return use_box(); }`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 || len(asm) > 18000 {
		t.Fatalf("asm is %d bytes — expected small IR output; the qualified-variant module likely bailed to the AST runtime", len(asm))
	}
	progBin := buildBin(t, gcc, dir, "qualified_variant", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 12 {
		t.Errorf("exit %d, want 12 (qualified Custom(7) construction + qualified pattern payload + n)", code)
	}
}
