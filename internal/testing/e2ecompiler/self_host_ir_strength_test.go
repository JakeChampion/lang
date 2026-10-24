package e2ecompiler

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostIRStrengthPeephole(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ir_strength_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ir_strength_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ir_strength_run.fern", "ir_strength_run")

	want := "mul_1: load_local 0\n" +
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
		"strength_then_fold: const_i32 5\n" +
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
		"tee_fused: const_i32 5 ; tee_local 0 ; return\n" +
		"tee_other_slot: const_i32 5 ; store_local 0 ; load_local 1\n" +
		"tee_gap: const_i32 5 ; store_local 0 ; const_i32 1 ; drop ; load_local 0\n" +
		"tee_reversed: load_local 0 ; store_local 0\n" +
		"tee_two: tee_local 0 ; tee_local 1\n" +
		"tee_idempotent=1\n" +
		"tee_in_optimize: const_i32 4 ; return\n" +
		"cp_dead_tee: const_i32 7 ; const_i32 2 ; mul\n" +
		"cp_live_read: const_i32 7 ; tee_local 0 ; load_local 0 ; add\n" +
		"cp_live_store: const_i32 7 ; tee_local 0 ; const_i32 1 ; store_local 0\n" +
		"cp_two_tees: const_i32 7 ; tee_local 0 ; const_i32 8 ; tee_local 0\n" +
		"cp_store_kept: const_i32 7 ; store_local 3\n" +
		"cp_pipeline: const_i32 14 ; return\n" +
		"kp_far: const_i32 7 ; store_local 0 ; call_direct side/0 ; drop ; const_i32 7 ; const_i32 3 ; add\n" +
		"kp_if_untouched: const_i32 7 ; store_local 0 ; const_i32 1 ; if ; end ; const_i32 7\n" +
		"kp_if_written: const_i32 7 ; store_local 0 ; const_i32 1 ; if ; call_direct side/0 ; store_local 0 ; end ; load_local 0\n" +
		"kp_else_written: const_i32 7 ; store_local 0 ; const_i32 1 ; if ; const_i32 7 ; drop ; else ; call_direct side/0 ; store_local 0 ; end ; load_local 0\n" +
		"kp_block_straight: block ; const_i32 7 ; store_local 0 ; end ; const_i32 7\n" +
		"kp_block_branched: block ; const_i32 1 ; brif 0 ; const_i32 7 ; store_local 0 ; end ; load_local 0\n" +
		"kp_loop_invariant: const_i32 7 ; store_local 0 ; loop ; const_i32 7 ; drop ; end ; const_i32 7\n" +
		"kp_loop_written: const_i32 7 ; store_local 0 ; loop ; load_local 0 ; drop ; call_direct side/0 ; store_local 0 ; end ; load_local 0\n" +
		"kp_nested: const_i32 7 ; store_local 0 ; const_i32 1 ; if ; block ; const_i32 7 ; const_i32 3 ; add ; drop ; end ; end ; return\n" +
		"kp_killed: const_i32 7 ; store_local 0 ; call_direct side/0 ; store_local 0 ; load_local 0\n" +
		"kp_text: const_i32_text 7 ; store_local 0 ; call_direct side/0 ; drop ; const_i32_text 7\n" +
		"kp_hex_refused: const_i32_text 0x10 ; store_local 0 ; call_direct side/0 ; drop ; load_local 0\n" +
		"kp_chain: const_i32 7 ; store_local 0 ; const_i32 7 ; store_local 1 ; const_i32 7 ; return\n" +
		"kp_frame_write: const_i32 7 ; store_local 1 ; str_slice frame:1 ; drop ; load_local 1\n" +
		"kp_heap_slice_kept: const_i32 7 ; store_local 1 ; str_slice ; drop ; const_i32 7\n" +
		"kp_frame_write_in_loop: const_i32 7 ; store_local 1 ; loop ; load_local 1 ; drop ; str_slice frame:1 ; drop ; end ; load_local 1\n" +
		"kp_idempotent=1\n" +
		"kp_in_optimize: return\n" +
		"nf_zero: const_i32 1\n" +
		"nf_nonzero: const_i32 0\n" +
		"nf_text: const_i32 1\n" +
		"nf_hex_refused: const_i32_text 0x10 ; not\n" +
		"nf_opaque_refused: load_local 0 ; not\n" +
		"nf_while_true_test_gone: \n" +
		"nf_at_tail: load_local 0 ; drop ; const_i32 1\n" +
		"pi_true_arm: const_i32 7 ; drop ; return\n" +
		"pi_false_arm: const_i32 8 ; drop ; return\n" +
		"pi_false_no_else: return\n" +
		"pi_escaping_wrapped: block ; block ; br 1 ; end ; end ; return\n" +
		"pi_local_branch_bare: block ; br 0 ; end ; return\n" +
		"pi_hex_refused: const_i32_text 0x10 ; if ; const_i32 7 ; drop ; end\n" +
		"pi_typed_refused: const_i32 1 ; if ; const_i32 7 ; end\n" +
		"pi_unclosed_refused: const_i32 1 ; if ; const_i32 7 ; drop\n" +
		"pb_zero_dropped: block ; const_i32 7 ; drop ; end\n" +
		"pb_one_becomes_br: block ; br 0 ; const_i32 7 ; drop ; end\n" +
		"pi_nested_cascade: const_i32 5 ; drop ; return\n" +
		"pi_idempotent=1\n" +
		"pw_while_true_in_optimize: loop ; br 0 ; end ; return\n" +
		"wc_nested: const_i32 7\n" +
		"wc_u8_masks: const_i32 44\n" +
		"wc_u32: const_i32 9\n" +
		"wc_u32_neg_refused: const_i32 -1 ; int_cast\n" +
		"wc_u32_wrap: const_i32 9\n" +
		"wc_u32_wrap_neg_refused: const_i32 -1 ; u32_wrap\n" +
		"wc_int_extend: const_i64 9\n" +
		"wc_int_wrap_refused: const_i32 9 ; int_wrap\n" +
		"wc_hex_refused: const_i32_text 0x10 ; int_cast\n" +
		"wc_opaque_refused: load_local 0 ; int_cast\n" +
		"zg_fires: const_i32 0 ; if ; load_local 1 ; call_direct __fern_arr_dec/1 ; drop ; end ; return\n" +
		"zg_param_refused: load_local 1 ; call_direct __fern_rc_is_unique/1 ; if ; load_local 1 ; call_direct __fern_arr_dec/1 ; drop ; end ; return\n" +
		"zg_in_loop_refused: loop ; load_local 1 ; call_direct __fern_rc_is_unique/1 ; if ; end ; br 0 ; end\n" +
		"zg_past_loop_fires: loop ; br 0 ; end ; const_i32 0\n" +
		"zg_written_refused: call_direct mk/0 ; store_local 1 ; load_local 1 ; call_direct __fern_rc_is_unique/1\n" +
		"zg_late_write_fires: const_i32 0 ; if ; end ; call_direct mk/0 ; store_local 1\n" +
		"zg_rezero_fires: const_i32 0 ; store_local 1 ; const_i32 0\n" +
		"zg_frame_write_refused: str_slice frame:1 ; drop ; load_local 1 ; call_direct __fern_rc_is_unique/1\n" +
		"zg_in_optimize: return\n" +
		"zg_idempotent=1\n" +
		"f64_neg: const_i64 -3\n" +
		"f64_mul_wraps: const_i64 0\n" +
		"f64_cmp: const_i32 1\n" +
		"f64_div_zero_refused: const_i64 7 ; const_i64 0 ; div_s\n" +
		"f64_min_div_refused: const_i64 -9223372036854775808 ; const_i64 -1 ; div_s\n" +
		"f64_min_rem: const_i64 0\n" +
		"f64_unsigned_refused: const_i64 -8 ; const_i64 1 ; shr_s\n" +
		"f64_hex_refused: const_i64 0x10 ; const_i64 1 ; add\n" +
		"f64_extend_s: const_i64 -1\n" +
		"f64_extend_u: const_i64 4294967295\n" +
		"f64_extend_ptr_refused: const_i32 100 ; int_extend\n" +
		"f64_wrap: const_i32 1\n" +
		"f64_wrap_u32_high_refused: const_i64 3000000000 ; int_wrap\n" +
		"f64_wrap_u32: const_i32 7\n" +
		magicParityLines() +
		"readable 0: i32=true i64=true agree=true\n" +
		"readable 7: i32=true i64=true agree=true\n" +
		"readable -7: i32=true i64=true agree=true\n" +
		"readable 007: i32=false i64=false agree=true\n" +
		"readable -0: i32=false i64=false agree=true\n" +
		"readable : i32=false i64=false agree=true\n" +
		"readable -: i32=false i64=false agree=true\n" +
		"readable 0x10: i32=false i64=false agree=true\n" +
		"readable 7u8: i32=false i64=false agree=true\n" +
		"readable +5: i32=false i64=false agree=true\n" +
		"readable 2147483647: i32=true i64=true agree=true\n" +
		"readable 2147483648: i32=false i64=true agree=true\n" +
		"readable -2147483648: i32=true i64=true agree=true\n" +
		"readable -2147483649: i32=false i64=true agree=true\n" +
		"readable 4294967296: i32=false i64=true agree=true\n" +
		"readable 9223372036854775807: i32=false i64=true agree=true\n" +
		"readable 9223372036854775808: i32=false i64=false agree=true\n" +
		"readable -9223372036854775808: i32=false i64=true agree=true\n" +
		"readable -9223372036854775809: i32=false i64=false agree=true\n" +
		"readable 99999999999999999999: i32=false i64=false agree=true\n" +
		"ds_const: const_i32 0 ; return\n" +
		"ds_loaded: const_i32 7 ; store_local 1 ; load_local 1 ; return\n" +
		"ds_effect: call_direct side/0 ; drop ; const_i32 0 ; return\n" +
		"ds_tee: load_local 0 ; return\n" +
		"ds_chain: const_i32 0 ; return\n" +
		"ds_not_top: load_local 0 ; const_i32 1 ; add ; drop ; const_i32 0 ; return\n" +
		"ds_scope: const_i32 1 ; block ; drop ; end ; const_i32 0 ; return\n" +
		"ds_frame: const_i32 5 ; store_local 2 ; load_local 0 ; const_i32 0 ; const_i32 1 ; str_slice frame:2 ; return\n" +
		"ds_dyn: load_local 0 ; store_local 2 ; const_i32 9 ; store_local 3 ; dyn_dispatch ; return\n" +
		"ds_copies: const_i32 0 ; return\n" +
		"ds_read_again: const_i32 7 ; store_local 0 ; load_local 0 ; return\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("ir_strength_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("strength-reduction report mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("ir_strength_run exit code = %d, want 0 (fixpoint held)", code)
	}
}

func magicParityLines() string {
	var b strings.Builder
	for _, d := range []int32{3, 7, -7, 97, 4093, -4093, 641, 1000000, 715827883, -1234567, 10, 100, 2147483647, -2147483647, 5, -5} {
		mg := deriveMagicS32(d)
		fmt.Fprintf(&b, "magic_s %d: m=%d s=%d add=%v sub=%v\n", d, mg.M, mg.S, mg.Add, mg.Sub)
	}
	for _, d := range []uint32{3, 7, 97, 641, 1000000, 2147483649, 2863311531, 4294967291, 4294967295, 10, 100, 5, 4093} {
		mg := deriveMagicU32(d)
		fmt.Fprintf(&b, "magic_u %d: m=%d s=%d add=%v\n", d, mg.M, mg.S, mg.Add)
	}
	for _, d := range []int64{3, 7, -7, 10, 100, 641, -1000000007, 1000000000000, 6148914691236517205, -6148914691236517205, 9223372036854775807, -9223372036854775807} {
		mg := deriveMagicS64(d)
		fmt.Fprintf(&b, "magic_s64 %d: m=%d s=%d add=%v sub=%v\n", d, mg.M, mg.S, mg.Add, mg.Sub)
	}
	for _, d := range []uint64{3, 7, 10, 100, 641, 1000000007, 9223372036854775809, 12297829382473034411, 18446744073709551557, 18446744073709551615} {
		mg := deriveMagicU64(d)
		fmt.Fprintf(&b, "magic_u64 %d: m=%d s=%d add=%v\n", int64(d), int64(mg.M), mg.S, mg.Add)
	}
	return b.String()
}
