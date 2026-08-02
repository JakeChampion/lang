package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostSymTab pins the symbol intern table (examples/self_host/symtab.fern's
// SymTab — #4394 lever 1 foundation, docs/SELFHOST-SYMBOL-INTERNING.md). SymTab
// maps a name to a small stable i32 id, storing each distinct name once; it is the
// building block for turning the compiler's dominant identifier / mangled-name
// string traffic (and the Op.str fields on the persistent op arrays) into i32 ops.
//
// The symtab_run driver exercises the full contract: dense id assignment, dedup of
// a repeated name to its original id, the name_of inverse (incl. the out-of-range
// guard on negative / too-large ids), lookup's -1-on-absent, and the round-trip
// invariant name_of(intern(s).id) == s. The golden below pins it exactly.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the map.
func TestSelfHostSymTab(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("symtab_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "symtab_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "symtab_run.fern", "symtab_run")

	const want = "alpha=0\n" +
		"beta=1\n" +
		"alpha2=0 same=T\n" +
		"gamma=2\n" +
		"count=3\n" +
		"name_of(0)=alpha\n" +
		"name_of(1)=beta\n" +
		"name_of(2)=gamma\n" +
		"name_of(-1)=<empty>\n" +
		"name_of(99)=<empty>\n" +
		"lookup(beta)=1\n" +
		"lookup(missing)=-1\n" +
		"roundtrip=T\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("symtab_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("symtab contract mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
