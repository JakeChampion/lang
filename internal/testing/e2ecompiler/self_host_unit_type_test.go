package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const unitRuntimeProgram = `function sink(u: void): i32 { return 7; }
function fallible(): Result[void, i32] { return Ok(()); }
function main(): i32 {
    let u: void = ();
    let v = ();
    let t: (void, i32) = ((), 5);
    let n: i32 = 0;
    match (fallible()) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 10; } }
    return n + sink(u) + sink(v) + sink(t.0) + t.1;
}
`

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
