package e2e

import (
	"testing"
)

// The sixth group of builtins x86_64ssa had no emitter for (#9559): signals,
// scheduling priority and process groups - the calls that take integers and
// name no file, so each classifies its errno against an empty path.
//
// Every one of them is a bare syscall whose only observable is a number, which
// is exactly where a wrong syscall number or a mis-signed argument is silent.
// The probe reads each answer back rather than checking it returned:
// signal_disposition must see what signal_ignore wrote, signal_mask must
// answer the mask from BEFORE the call, and priority must read back what
// set_priority set. getpriority and setpriority are SWAPPED between the two
// ABIs, so a number copied from arm64ssa fails here and nowhere else.
//
// The comparison is against the flat x86-64 emitter, for the reason
// x86_64ssa_path_helpers_test.go gives.
const x86SSASignalSrc = `function main(): i32 {
    // signal_disposition reads without writing: SIGUSR1 starts at default,
    // becomes ignore, and goes back.
    if (signal_disposition(10) != 0) { return 10; }
    signal_ignore(10);
    if (signal_disposition(10) != 1) { return 11; }
    signal_default(10);
    if (signal_disposition(10) != 0) { return 12; }

    // signal_mask answers the mask that was blocked BEFORE the call, so
    // blocking then restoring reads the old value back.
    var before: i64 = signal_mask(0, 512i64);
    var blocked: i64 = signal_mask(2, before);
    if (blocked % 1024i64 < 512i64) { return 20; }
    if (signal_mask(0, 0i64) != before) { return 21; }

    // signal_send to this process with signal 0 is the liveness probe: no
    // signal is delivered, and a pid that cannot exist is an error.
    match (signal_send(0, 0)) { Ok(_) => {}, Err(e) => { return 30; } }
    match (signal_send(2147483647, 0)) { Ok(_) => { return 31; }, Err(e) => {} }

    // set_process_group with both zeroes names the caller and its own pid.
    match (set_process_group(0, 0)) { Ok(_) => {}, Err(e) => { return 40; } }
    match (set_process_group(2147483647, 0)) { Ok(_) => { return 41; }, Err(e) => {} }

    // priority reads the nice value unbiased, and raising it is always
    // permitted where lowering it may not be.
    var p: i32 = priority();
    if (p < 0 - 20 || p > 19) { return 50; }
    match (set_priority(p + 1)) { Ok(_) => {}, Err(e) => { return 51; } }
    if (priority() != p + 1) { return 52; }
    return 0;
}
`

func TestX86_64SSASignalsMatchTheFlatEmitter(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	flat := runPathProbe(t, bin, qemu, dir, "signals", "flat", x86SSASignalSrc, "")
	if flat != 0 {
		t.Fatalf("the flat emitter itself reports %d - the probe is wrong, not the SSA backend", flat)
	}
	if ssa := runPathProbe(t, bin, qemu, dir, "signals", "ssa", x86SSASignalSrc, ""); ssa != flat {
		t.Errorf("-backend ssa reports %d where the flat emitter reports %d.\n\n"+
			"Each code names one assertion in the probe source above; the two backends "+
			"are two implementations of the same builtins and must agree.", ssa, flat)
	}
}
