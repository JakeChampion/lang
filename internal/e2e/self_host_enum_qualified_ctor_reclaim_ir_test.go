package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostEnumQualifiedCtorReclaimIRX86_64 covers a Perceus completeness fix:
// a consume-by-match enum local built with a QUALIFIED variant constructor
// (`Bag.Items(..)`, not the bare `Items(..)`) is now reclaimed too. The
// fresh-rc/scalar-enum-init classifiers only matched a bare-ident callee, so a
// qualified-style construction's callee — an ExprFieldAccess (obj=Enum,
// field=Variant) — fell through and the enum box + payload leaked (while the
// identical unqualified form reclaimed). Both classifiers now resolve the variant by
// its field name, so qualified and unqualified construction reclaim identically.
//
// SOUNDNESS: the qualified construction path shares the unqualified lowering, so a
// bare-ident array payload is array-alias-inc'd at construction exactly as the
// unqualified form is — the match-site rc-dec is balanced (no double-free). Proven
// by the bare-ident case staying bounded AND value-correct over the churn.
//
// Reclaim is shown by heap exhaustion: a long consume-by-match churn that leaks the
// payload buffer each iteration is SIGKILLed (137); with the reclaim it stays
// bounded (0).
func TestSelfHostEnumQualifiedCtorReclaimIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// QUALIFIED ctor + fresh-literal payload: 40M consume-by-match cycles stay bounded
	// (exit 0). Pre-fix the qualified callee wasn't recognised, so the box + buffer
	// leaked every iteration → heap exhausted → SIGKILL.
	run(t, `enum Bag { Items(i32[]), None }
function mk(): i32 {
    var b: Bag = Bag.Items([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]);
    match (b) { Items(_) => {}, None => {}, }
    return 5;
}
function main(): i32 {
    var s: i32 = 0; var f: i32 = 0;
    while (f < 40000000) { s = mk(); f = f + 1; }
    return s - 5;
}`, "qual_ctor_literal_churn", 0)

	// QUALIFIED ctor + BARE-IDENT payload (aliases a local read before the match):
	// the construction array-alias-inc balances the match-site dec, so the churn is
	// bounded AND the read-back is intact. xs[0]+xs[15] = 1 + 16 = 17. A missing inc
	// would double-free → freelist corruption / crash; a wrong free → wrong value.
	run(t, `enum Bag { Items(i32[]), None }
function mk(): i32 {
    var xs: i32[] = [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16];
    var b: Bag = Bag.Items(xs);
    var r: i32 = xs[0] + xs[15];
    match (b) { Items(_) => {}, None => {}, }
    return r;
}
function main(): i32 {
    var s: i32 = 0; var f: i32 = 0;
    while (f < 20000000) { s = mk(); f = f + 1; }
    return s - 17;
}`, "qual_ctor_bareident_churn", 0)
}
