package e2e

import (
	"testing"
)

// The second group of builtins x86_64ssa had no emitter for (#9559): the leaf
// syscalls that ask the kernel about this process and get an integer back.
// None allocates and none can fail in a way the language's signature has room
// for, so each is a syscall and a ret — but a wrong syscall number or a
// clobbered argument register is silent here, which is what this pins.
//
// The comparison is against the flat x86-64 emitter, for the reason
// x86_64ssa_path_helpers_test.go gives: the two backends are two
// implementations of the same builtins, and the only question is whether they
// agree.
const x86SSAProcessLeafSrc = `function main(): i32 {
    // Under qemu the process is the container's root, so every id may be 0;
    // what must hold on any host is that the real and effective halves agree
    // when nothing has dropped privilege, and that neither came back as the
    // kernel's -1.
    var u: u32 = getuid();
    var eu: u32 = geteuid();
    var g: u32 = getgid();
    var eg: u32 = getegid();
    if (u != eu || g != eg) { return 10; }
    if (u == 4294967295 as u32) { return 11; }

    // umask answers the mask it replaced, so setting it twice reads back.
    var prev: i32 = umask(18);
    if (umask(prev) != 18) { return 20; }

    // At least one processing unit, and not an implausible number: a mask
    // counted with the wrong width reads far too high.
    var n: i32 = cpu_count();
    if (n < 1 || n > 4096) { return 30; }

    // now_ns is the realtime clock in nanoseconds — past 2020, and it does not
    // run backwards over a sleep the sleep itself must cover.
    var t0: i64 = now_ns();
    if (t0 < 1577836800000000000i64) { return 40; }
    sleep_ns(2000000i64);
    var t1: i64 = now_ns();
    if (t1 - t0 < 1000000i64) { return 41; }
    // A non-positive sleep returns without a syscall.
    sleep_ns(0 as i64 - 1 as i64);
    return 0;
}
`

func TestX86_64SSAProcessLeavesMatchTheFlatEmitter(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	flat := runPathProbe(t, bin, qemu, dir, "leaves", "flat", x86SSAProcessLeafSrc)
	if flat != 0 {
		t.Fatalf("the flat emitter itself reports %d — the probe is wrong, not the SSA backend", flat)
	}
	if ssa := runPathProbe(t, bin, qemu, dir, "leaves", "ssa", x86SSAProcessLeafSrc); ssa != flat {
		t.Errorf("-backend ssa reports %d where the flat emitter reports %d.\n\n"+
			"Each code names one assertion in the probe source above; the two backends "+
			"are two implementations of the same builtins and must agree.", ssa, flat)
	}
}
