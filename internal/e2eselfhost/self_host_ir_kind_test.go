package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostIRKindRegistry exercises the self-hosted stack IR's integer
// op-kind registry (examples/self_host/ir.fern's kind_id / kind_name /
// kind_count + the int-keyed classifier predicates, issue #4394 lever 2 —
// the string->int op-kind conversion foundation).
//
// The ir_kind_run driver walks the whole registry (ids 1..kind_count()) and
// asserts the id<->name bijection, the KIND_INVALID contract, and the
// classifier predicates, printing a deterministic report and exiting with the
// bijection-failure count (0 on a clean sweep). This pins the registry
// end-to-end through the self-host -> native pipeline, proving ir.fern's
// registry compiles + round-trips (not just type-checks); a new op kind added
// to only one direction, or a duplicate id, fails the golden here.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the
// report and its exit code is the bijection-failure count.
func TestSelfHostIRKindRegistry(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ir_kind_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ir_kind_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ir_kind_run.fern", "ir_kind_run")

	// Golden report — locks kind_count, the full-table bijection (all 196 ids
	// round-trip), the extension-tag sweep (the 43 registered ids beyond
	// kind_count(), struct_copy=198 … reader_seek=242 — #5452's skew
	// left them unrendered by kind_name), the negative sweep (14 near-miss tags
	// that must all be KIND_INVALID, probing kind_id's (length, first byte)
	// narrowing from the other side), the KIND_INVALID sentinels, a few stable
	// ids, and every classifier predicate's answer on representative kinds.
	//
	// The two sweeps together name EVERY tag kind_id knows — the 196 dense ids
	// via kind_name, the 43 extension tags by name — so the round trip this
	// pins is exhaustive. An id that moved would fail here whatever else went
	// green.
	//
	// ext_ok and tag_consistency move together whenever an extension op is
	// added: both count entries in ir_kind_run.fern's sweep lists, and the
	// point of the golden is that adding a kind_id without registering it in
	// BOTH shows up here rather than as an "invalid" name at some call site.
	const want = "kind_count=196\n" +
		"bijection_ok=196\n" +
		"bijection_failures=0\n" +
		"ext_ok=43\n" +
		"ext_failures=0\n" +
		"neg_ok=14\n" +
		"neg_failures=0\n" +
		"unknown_id=0\n" +
		"id0=invalid\n" +
		"oob=invalid\n" +
		"const_i32=1\n" +
		"add=21\n" +
		"call_direct=149\n" +
		"return=160\n" +
		"is_const const_i32=1 const_str=1 add=0\n" +
		"is_term return=1 br=1 exit=1 brif=0\n" +
		"is_fold add=1 div_s=1 ge_s=1 fadd=0\n" +
		"is_commute add=1 xor=1 sub=0 shl=0\n" +
		"tag_consistency ok=42 bad=0\n"

	// The report ends with every registered tag's id in id order, pinned by
	// testdata/ir-kind-ids.txt. The backends dispatch on literal ids, so this
	// catches a renumbering the bijection sweep alone would pass.
	table, err := os.ReadFile(filepath.Join("testdata", "ir-kind-ids.txt"))
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("ir_kind_run did not exit normally")
	}
	if got := string(out); got != want+string(table) {
		t.Errorf("kind registry report mismatch:\n--- got ---\n%s\n--- want ---\n%s%s", got, want, table)
	}
	// Exit code totals the failures of all four sweeps — bijection over the
	// dense ids, over the ext ids, the negative sweep, and the tag census. 0
	// proves every one of the 233 tags round-tripped AND that no near miss
	// resolved, an independent check of the report's own _ok flags.
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("ir_kind_run exit code = %d, want 0 (total failures across the four sweeps)", code)
	}
}
