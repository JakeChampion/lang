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
		// The tee is GONE here where the direct fuse_tee cases above keep theirs:
		// optimize_ops runs propagate_copies after the fusion, and nothing else
		// touches slot 0, so the write is dead.
		"tee_in_optimize: const_i32 4 ; return\n" +
		// #6638 copy propagation. The three "live" rows are the gate: a real read, a
		// second store, and a second tee each keep the slot alive. cp_pipeline is
		// native's worked example end to end — inline round trip, fuse, drop the dead
		// tee, then fold the exposed const pair to a single 14.
		"cp_dead_tee: const_i32 7 ; const_i32 2 ; mul\n" +
		"cp_live_read: const_i32 7 ; tee_local 0 ; load_local 0 ; add\n" +
		"cp_live_store: const_i32 7 ; tee_local 0 ; const_i32 1 ; store_local 0\n" +
		"cp_two_tees: const_i32 7 ; tee_local 0 ; const_i32 8 ; tee_local 0\n" +
		"cp_store_kept: const_i32 7 ; store_local 3\n" +
		"cp_pipeline: const_i32 14 ; return\n" +
		// #6638 constant propagation. kp_far is the shape fuse_tee structurally
		// cannot reach: a store and a load that are not adjacent.
		"kp_far: const_i32 7 ; store_local 0 ; call_direct side/0 ; drop ; const_i32 7 ; const_i32 3 ; add\n" +
		// The merge rule, in both directions on every scope kind. It kills what a
		// scope COULD have written, so the survivors below are the slots the scope
		// provably does not touch — and the refusal twin of each is a slot it does.
		// A wrong verdict here is a miscompile no backend can see, which is why each
		// pair is pinned rather than sampled.
		"kp_if_untouched: const_i32 7 ; store_local 0 ; const_i32 1 ; if ; end ; const_i32 7\n" +
		"kp_if_written: const_i32 7 ; store_local 0 ; const_i32 1 ; if ; call_direct side/0 ; store_local 0 ; end ; load_local 0\n" +
		// The then-arm still sees the ENTRY binding (const_i32 7 inside the arm); what
		// it does not see is the other arm's write, and what survives the `end` is
		// neither.
		"kp_else_written: const_i32 7 ; store_local 0 ; const_i32 1 ; if ; const_i32 7 ; drop ; else ; call_direct side/0 ; store_local 0 ; end ; load_local 0\n" +
		"kp_block_straight: block ; const_i32 7 ; store_local 0 ; end ; const_i32 7\n" +
		"kp_block_branched: block ; const_i32 1 ; brif 0 ; const_i32 7 ; store_local 0 ; end ; load_local 0\n" +
		// The loop pair is where this rule is FINER than native's, which drops its
		// whole table at a loop: a slot the body cannot write is still propagated
		// inside the body, not just after it.
		"kp_loop_invariant: const_i32 7 ; store_local 0 ; loop ; const_i32 7 ; drop ; end ; const_i32 7\n" +
		"kp_loop_written: const_i32 7 ; store_local 0 ; loop ; load_local 0 ; drop ; call_direct side/0 ; store_local 0 ; end ; load_local 0\n" +
		"kp_nested: const_i32 7 ; store_local 0 ; const_i32 1 ; if ; block ; const_i32 7 ; const_i32 3 ; add ; drop ; end ; end ; return\n" +
		"kp_killed: const_i32 7 ; store_local 0 ; call_direct side/0 ; store_local 0 ; load_local 0\n" +
		// kp_hex_refused inherits the shared const_i32_readable guard — a non-decimal
		// text constant is not propagated as though it were its own digits.
		"kp_text: const_i32_text 7 ; store_local 0 ; call_direct side/0 ; drop ; const_i32_text 7\n" +
		"kp_hex_refused: const_i32_text 0x10 ; store_local 0 ; call_direct side/0 ; drop ; load_local 0\n" +
		// The binding source is the rewritten list, so the substituted load at slot 0
		// is itself the constant that binds slot 1 — one walk carries `var b = a`.
		"kp_chain: const_i32 7 ; store_local 0 ; const_i32 7 ; store_local 1 ; const_i32 7 ; return\n" +
		// str_slice_frame is the only op that writes a local without being a store —
		// three slots at `i32_imm - 1` — so a binding on one ends there, while the
		// heap form (immediate 0) leaves it alone. Unreachable on today's programs,
		// where irlower names those slots `!view!` and no store can name them; pinned
		// so the pass is sound on its own reasoning rather than on that convention.
		"kp_frame_write: const_i32 7 ; store_local 1 ; str_slice frame:1 ; drop ; load_local 1\n" +
		"kp_heap_slice_kept: const_i32 7 ; store_local 1 ; str_slice ; drop ; const_i32 7\n" +
		// The loop's body scan is the other half of that model: a frame write inside a
		// body ends the binding for the whole loop, including the load that precedes
		// it, which the back-edge reaches after the write.
		"kp_frame_write_in_loop: const_i32 7 ; store_local 1 ; loop ; load_local 1 ; drop ; str_slice frame:1 ; drop ; end ; load_local 1\n" +
		"kp_idempotent=1\n" +
		// The payoff, through optimize_ops, and the clearest example of the battery
		// composing: pruning the decided `if` leaves the store and load ADJACENT, so
		// fuse_tee fires where it previously could not reach, propagate_copies then
		// drops the tee as dead, and the fold collapses what is left. Nine ops to
		// three, none of which any single pass could have done alone.
		"kp_in_optimize: const_i32 10 ; drop ; return\n" +
		// #6638 the unary fold. `not` is LOGICAL in this IR — i32.eqz on wasm, setz on
		// x86, cset eq on arm64, bool_not in eval_ops — so zero folds to 1 and
		// anything else to 0. There is no bitwise complement in the vocabulary.
		"nf_zero: const_i32 1\n" +
		"nf_nonzero: const_i32 0\n" +
		"nf_text: const_i32 1\n" +
		"nf_hex_refused: const_i32_text 0x10 ; not\n" +
		"nf_opaque_refused: load_local 0 ; not\n" +
		// The two folds compose: `while (true)`'s exit test is `const 1 ; not ; brif`,
		// and a loop that never exits needs no exit check, so the line is empty.
		"nf_while_true_test_gone: \n" +
		// Folding at the very end of the list, which the three-op window cannot reach.
		"nf_at_tail: load_local 0 ; drop ; const_i32 1\n" +
		// #6638 constant-branch pruning. A decided `if` keeps the arm that runs and
		// drops the condition, the scope, and the other arm.
		"pi_true_arm: const_i32 7 ; drop ; return\n" +
		"pi_false_arm: const_i32 8 ; drop ; return\n" +
		"pi_false_no_else: return\n" +
		// The wrap pair, and the reason it counts depth instead of looking for a
		// branch. pi_escaping_wrapped's `br 1` leaves the arm, so the arm keeps a
		// scope of its own and the depth still resolves to the same outer block —
		// this is the COMMON case, since every `break` inside an `if` is one.
		// pi_local_branch_bare's branch targets the arm's own block, so a wrap there
		// would be the bug: it would push every depth in the arm out by one.
		"pi_escaping_wrapped: block ; block ; br 1 ; end ; end ; return\n" +
		"pi_local_branch_bare: block ; br 0 ; end ; return\n" +
		// Refusals: the shared const_i32_readable guard, a non-void if (whose end
		// would carry a value that deleting the scope strands), and an unbalanced
		// list, which is left alone rather than half-rewritten.
		"pi_hex_refused: const_i32_text 0x10 ; if ; const_i32 7 ; drop ; end\n" +
		"pi_typed_refused: const_i32 1 ; if ; const_i32 7 ; end\n" +
		"pi_unclosed_refused: const_i32 1 ; if ; const_i32 7 ; drop\n" +
		// The decided `brif`. The dead tail after the new `br` survives here because
		// this calls the fold directly; optimize_ops sweeps it in finish_ops, which is
		// why that second dead-code pass exists.
		"pb_zero_dropped: block ; const_i32 7 ; drop ; end\n" +
		"pb_one_becomes_br: block ; br 0 ; const_i32 7 ; drop ; end\n" +
		// Two nested ifs and the arithmetic behind them, all in one call, because the
		// fold self-iterates.
		"pi_nested_cascade: const_i32 5 ; drop ; return\n" +
		"pi_idempotent=1\n" +
		// End to end: `while (true)` keeps its body and loses its exit test.
		"pw_while_true_in_optimize: loop ; const_i32 7 ; drop ; br 0 ; end ; return\n"

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
