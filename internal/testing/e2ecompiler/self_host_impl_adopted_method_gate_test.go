package e2ecompiler

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostEmptyImplAdoptedMethodGate(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")

	progDir := t.TempDir()
	const modSrc = `pub struct Val { n: i32 }

pub function (v: Val) hash(): i32 { return v.n * 31; }

pub function make(n: i32): Val { return Val { n: n }; }
`
	const progSrc = `import "./bigish";

trait Hash { function hash(self: Self): i32; }

impl Hash for bigish.Val { }

function main(): i32 {
    let v: bigish.Val = bigish.make(7);
    if (v.n == 7) { return 0; }
    return 1;
}
`
	mustWrite(t, progDir, "bigish.fern", modSrc)
	prog := mustWrite(t, progDir, "prog.fern", progSrc)

	asm, err := exec.Command(mmc, prog, "-treeshake").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && strings.Contains(string(ee.Stderr), "E021") {
			t.Fatalf("build gate read the treeshaked module: %s", ee.Stderr)
		}
		t.Fatalf("self-host compile failed: %s", childFailure(err))
	}
	if len(asm) == 0 {
		t.Fatal("emitted 0 bytes")
	}
	bin := buildBin(t, gcc, dir, "impl_adopted_gate", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Errorf("program exited %d, want 0", code)
	}
}
