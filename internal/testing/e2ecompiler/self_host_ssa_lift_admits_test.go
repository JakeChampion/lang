package e2ecompiler

import "testing"

// TestSelfHostSSALiftAdmitsEveryOp pins the production lift's admission
// census (compiler/drivers/ssa_lift_admits_run.fern): every registered IR
// op kind reaches the register path, as an instruction of the lift's own or
// through the stack machine's arm for it, except the ones listed here: a
// kind with a pop count ir.op_pops does not model cannot be bridged. A new
// line in the report is a kind the register path declines on every program
// that uses it, which is what this gate exists to refuse.
func TestSelfHostSSALiftAdmitsEveryOp(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ssa_lift_admits_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ssa_lift_admits_run.fern", "ssa_lift_admits_run")

	// load, store and call_closure_direct are registered kinds no lowering
	// produces and no backend has an arm for; they stay unmodelled in
	// op_pops, so the lift cannot bridge them either. syscall6 adds one
	// registered and admitted kind, with seven inputs and one result; the raw
	// byte pipeline (#10995, #11000) adds six more: buf_push_bytes_range,
	// buf_take_bytes, read_chunk_bytes, write_bytes, write_file_bytes and
	// write_some_bytes. Byte scans, reductions and transformations are
	// registered and admitted alongside the rename and xattr operations, as
	// are str_from_bytes_range and the -g line marker. opt_none is retired
	// (#11510): a payloadless Option is op_const_option's static block.
	// outer_mul_f64, inner_mul_add_f64 and arr_reserve each add one admitted
	// array operation; none adds an unmodelled stack effect.
	// ret_word and call_word, the word a paired return passes beside its
	// result, are two more, admitted through the flat arm. buf_clear and
	// tcp_send_buf, which empty a builder and send from one, are two more.
	// clock_set, clock_resolution, str_hash, read_dir_ino, signal_catch,
	// signal_taken, signal_catch_interrupting, signal_raise,
	// disable_core_dumps, proc_waitpid_status and mounts are eleven more, the
	// constant-time gate's ct_mark and vg_request two more, sysctl one more,
	// and the AES-GCM kernels aes_expand_key, aes_ctr32 and ghash three more.
	// clone_file and set_extproc are two more.
	// reader_copy_range and pipe add two admitted kinds with three and zero
	// inputs respectively; each pushes one Result and has a modelled effect.
	// The eight Dir kinds (420-427) each have a modelled pop count and push
	// one Result, so all eight are admitted through the flat arm.
	const want = "load pops=-1 pushes=1\n" +
		"store pops=-1 pushes=1\n" +
		"call_closure_direct pops=-1 pushes=1\n" +
		"registered=380 declined=3\n"

	cmd := runX86_64Bin(runner, bin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ssa_lift_admits_run: %v\n%s", err, out)
	}
	if got := string(out); got != want {
		t.Errorf("lift admission census mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
