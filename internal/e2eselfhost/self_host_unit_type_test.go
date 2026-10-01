package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// unitRuntimeProgram puts the unit through the self-host's WHOLE pipeline, in
// every position #8759 gave it a type: a local with and without an annotation,
// a variant payload, a tuple element, and a call argument.
//
// The front end is what needs a runtime check. `()` used to type i32 — the
// constant it lowers to — while the type `()` resolved to nothing at all, so
// the two disagreed and no destination spelled void ever saw the value. Both
// now say void, which is what the annotator stamps and irlower reads to size a
// slot. One backend is enough to say the slot survived: nothing in the change
// is target-specific.
//
//	Ok arm           →  1
//	sink × 3         → 21
//	tuple element .1 →  5
//	exit             → 27
const unitRuntimeProgram = `function sink(u: ()): i32 { return 7; }
function fallible(): Result[(), i32] { return Ok(()); }
function main(): i32 {
    var u: () = ();
    var v = ();
    var t: ((), i32) = ((), 5);
    var n: i32 = 0;
    match (fallible()) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 10; } }
    return n + sink(u) + sink(v) + sink(t.0) + t.1;
}
`

// The CLI driver rather than asm_ir_run, because the checker is half of what
// this pins: asm_ir_run imports only the lowering chain, so a program it
// refuses to type still compiles there.
func TestSelfHostUnitTypeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(dir, "unit_type.fern")
	if err := os.WriteFile(src, []byte(unitRuntimeProgram), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", src)
	asm, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("self-host compile: %v\n%s", err, stderr)
	}
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}

	bin := buildBin(t, gcc, dir, "unit_type", string(asm))
	run := exec.Command(bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 27 {
		t.Errorf("self-host x86-64: got exit %d, want 27", code)
	}
}
