package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostIRStrengthPeephole pins the self-hosted stack IR's
// strength-reduction peephole (examples/self_host/ir.fern's reduce_strength /
// optimize_ops — the op-list twin of native's internal/ir/strength.go, #6638).
//
// The ir_strength_run driver runs the pass over one op list per rewrite and
// prints the rendered result. The golden below is what every backend receives,
// so a rewrite that changes shape fails here — and so does one that starts
// firing where it must not: signed div/rem by a power of two (div_s rounds
// toward zero where shr_s rounds toward negative infinity), a 64-bit-width
// binary (the replacement const is i32-width), and a non-decimal literal
// (digits_to_i32 reads "0x10" as 0, which would look like `* 0`).
func TestSelfHostIRStrengthPeephole(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ir_strength_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ir_strength_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ir_strength_run.fern", "ir_strength_run")

	const want = "mul_1: load_local 0\n" +
		"mul_0: load_local 0 ; drop ; const_i32 0\n" +
		"mul_8: load_local 0 ; const_i32 3 ; shl\n" +
		"mul_2: load_local 0 ; const_i32 1 ; shl\n" +
		"mul_3: load_local 0 ; const_i32 3 ; mul\n" +
		"mul_neg8: load_local 0 ; const_i32 -8 ; mul\n" +
		"add_0: load_local 0\n" +
		"sub_0: load_local 0\n" +
		"or_0: load_local 0\n" +
		"xor_0: load_local 0\n" +
		"shl_0: load_local 0\n" +
		"shr_s_0: load_local 0\n" +
		"shr_u_0: load_local 0\n" +
		"add_1: load_local 0 ; const_i32 1 ; add\n" +
		"and_0: load_local 0 ; drop ; const_i32 0\n" +
		"and_neg1: load_local 0\n" +
		"and_7: load_local 0 ; const_i32 7 ; and\n" +
		"div_s_1: load_local 0\n" +
		"div_s_8: load_local 0 ; const_i32 8 ; div_s\n" +
		"div_u_1: load_local 0\n" +
		"div_u_8: load_local 0 ; const_i32 3 ; shr_u\n" +
		"div_u_6: load_local 0 ; const_i32 6 ; div_u\n" +
		"rem_s_1: load_local 0 ; drop ; const_i32 0\n" +
		"rem_s_8: load_local 0 ; const_i32 8 ; rem_s\n" +
		"rem_u_1: load_local 0 ; drop ; const_i32 0\n" +
		"rem_u_8: load_local 0 ; const_i32 7 ; and\n" +
		"rem_u_6: load_local 0 ; const_i32 6 ; rem_u\n" +
		"mul_8_w64: load_local 0 ; const_i32 8 ; mul\n" +
		"add_0_w64: load_local 0 ; const_i32 0 ; add\n" +
		"mul_text_8: load_local 0 ; const_i32 3 ; shl\n" +
		"mul_text_hex: load_local 0 ; const_i32_text 0x10 ; mul\n" +
		"chain: load_local 0 ; const_i32 2 ; shl ; const_i32 1 ; shl\n" +
		"fold_then_strength: load_local 0 ; const_i32 2 ; shl\n" +
		"strength_then_fold: load_local 0 ; drop ; const_i32 5\n" +
		"idempotent=1\n" +
		"dce_arm: block ; const_i32 1 ; return ; end\n" +
		"dce_after_br: block ; br 0 ; end ; const_i32 2 ; drop\n" +
		"dce_after_brif: block ; brif 0 ; const_i32 1 ; drop ; end\n" +
		"dce_else_revives: if ; return ; else ; const_i32 2 ; drop ; end\n" +
		"dce_nested_dead: const_i32 1 ; return\n" +
		"dce_exit_kept: const_i32 3 ; exit ; drop ; const_i32 0 ; return\n" +
		"dce_live: load_local 0 ; const_i32 1 ; add ; return\n" +
		"dce_idempotent=1\n" +
		"dce_in_optimize: const_i32 4 ; return\n" +
		// #6638 tee fusion. The three refusals matter as much as the hit: a
		// different slot is an unrelated value, a gap is copy propagation's job,
		// and the reversed order is a plain read-then-write. `tee_two` guards the
		// walk's index bump — consuming the load must not skip the next store.
		"tee_fused: const_i32 5 ; tee_local 0 ; return\n" +
		"tee_other_slot: const_i32 5 ; store_local 0 ; load_local 1\n" +
		"tee_gap: const_i32 5 ; store_local 0 ; const_i32 1 ; drop ; load_local 0\n" +
		"tee_reversed: load_local 0 ; store_local 0\n" +
		"tee_two: tee_local 0 ; tee_local 1\n" +
		"tee_idempotent=1\n" +
		"tee_in_optimize: const_i32 4 ; tee_local 0 ; return\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("ir_strength_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("strength-reduction report mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// Exit code is 0 only when a second optimize_ops round changed nothing —
	// an independent check that the pipeline reached a fixpoint.
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("ir_strength_run exit code = %d, want 0 (fixpoint held)", code)
	}
}
