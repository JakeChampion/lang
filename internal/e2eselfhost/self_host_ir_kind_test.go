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
	for _, name := range []string{"util.fern", "ir.fern", "ir_kind_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, gcc, dir, "ir_kind_run.fern", "ir_kind_run")

	// Golden report — locks kind_count, the full-table bijection (all 197 ids
	// round-trip), the extension-tag sweep (the 15 registered ids beyond
	// kind_count(), struct_copy=198 … proc_waitpid=212 — #5452's skew left them
	// unrendered by kind_name), the KIND_INVALID sentinels, a few stable ids,
	// and every classifier predicate's answer on representative kinds.
	const want = "kind_count=197\n" +
		"bijection_ok=197\n" +
		"bijection_failures=0\n" +
		"ext_ok=15\n" +
		"ext_failures=0\n" +
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
		"tag_consistency ok=21 bad=0\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("ir_kind_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("kind registry report mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// Exit code is the bijection-failure count — 0 proves the whole 197-entry
	// table round-tripped, an independent check of the report's bijection_ok.
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("ir_kind_run exit code = %d, want 0 (bijection failures)", code)
	}
}
