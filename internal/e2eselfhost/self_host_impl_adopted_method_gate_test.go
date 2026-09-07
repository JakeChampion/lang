package e2eselfhost

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostEmptyImplAdoptedMethodGate pins that the file-loading driver's
// build gate reads the module AS WRITTEN, not the treeshaked one.
//
// `impl Trait for T { }` is satisfied by an inherent method T already carries
// (core/cmp's `impl Hash for bigint.BigInt { }` is the shape). treeshake prunes
// mod.funcs by name reachability but leaves mod.impls whole, so a program that
// never calls that method loses it and the impl is left pointing at nothing.
// Gating on that pruned module reported `bigint__BigInt does not implement
// Hash: missing method "hash"` for every stdlib-importing program (#8885) —
// which the native checker, running before any prune, never says.
//
// The type is reached through an import so the impl names it module-qualified
// (`bigish__Val` after mangling), the spelling the failure carried.
//
// Native only: the file-loading driver reads modules by host path from argv.
func TestSelfHostEmptyImplAdoptedMethodGate(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	progDir := t.TempDir()
	const modSrc = `pub struct Val { n: i32 }

pub function (v: Val) hash(): i32 { return v.n * 31; }

pub function make(n: i32): Val { return Val { n: n }; }
`
	// main never spells `.hash()`, so the prune drops `bigish__Val.hash`.
	const progSrc = `import "./bigish";

trait Hash { function hash(self: Self): i32; }

impl Hash for bigish.Val { }

function main(): i32 {
    var v: bigish.Val = bigish.make(7);
    if (v.n == 7) { return 0; }
    return 1;
}
`
	mustWrite(t, progDir, "bigish.fern", modSrc)
	prog := mustWrite(t, progDir, "prog.fern", progSrc)

	// `-treeshake` forces the prune the stdlib root turns on by default, so the
	// pin costs a two-module load rather than the whole stdlib closure.
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
