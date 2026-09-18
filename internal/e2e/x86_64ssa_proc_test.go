package e2e

import (
	"testing"
)

// The seventh group of builtins x86_64ssa had no emitter for (#9559): the
// spawn family - proc_fork, proc_exec, proc_exec_as, proc_waitpid and
// proc_waitpid_nohang.
//
// This one has to be exercised end to end rather than a call at a time,
// because what it produces is a second process: an exec that silently loses
// argv[0], or a status word decoded with the wrong shift, is invisible from
// inside the caller. So the probe forks, execs, and reads the child's own exit
// code back through the wait, which is the only place those mistakes show.
//
// The comparison is against the flat x86-64 emitter, for the reason
// x86_64ssa_path_helpers_test.go gives.
const x86SSAProcSrc = `function main(): i32 {
    // proc_fork / proc_exec: the child replaces its image with /bin/true and
    // the parent reaps its exit code.
    var pid: i32 = proc_fork();
    if (pid < 0) { return 10; }
    if (pid == 0) {
        proc_exec("/bin/true", []);
        exit(97);
    }
    if (proc_waitpid(pid) != 0) { return 11; }

    // The exit code the child chose reaches the parent unchanged.
    var p2: i32 = proc_fork();
    if (p2 < 0) { return 20; }
    if (p2 == 0) {
        proc_exec("/bin/sh", ["-c", "exit 42"]);
        exit(98);
    }
    if (proc_waitpid(p2) != 42) { return 21; }

    // A death by signal surfaces as 128 + the signal.
    var p3: i32 = proc_fork();
    if (p3 < 0) { return 30; }
    if (p3 == 0) {
        proc_exec("/bin/sh", ["-c", "kill -9 $$"]);
        exit(99);
    }
    if (proc_waitpid(p3) != 137) { return 31; }

    // proc_exec_as passes argv verbatim, argv[0] included, and its own envp.
    var p4: i32 = proc_fork();
    if (p4 < 0) { return 40; }
    if (p4 == 0) {
        proc_exec_as("/bin/sh", ["sh", "-c", "test \"$PROBE\" = yes"], ["PROBE=yes"]);
        exit(100);
    }
    if (proc_waitpid(p4) != 0) { return 41; }

    // proc_waitpid_nohang answers -1 while the child is still running, and
    // the exit code once it is not.
    var p5: i32 = proc_fork();
    if (p5 < 0) { return 50; }
    if (p5 == 0) {
        proc_exec("/bin/sh", ["-c", "sleep 1; exit 7"]);
        exit(101);
    }
    if (proc_waitpid_nohang(p5) != 0 - 1) { return 51; }
    if (proc_waitpid(p5) != 7) { return 52; }

    // An exec of a name that is not there returns rather than replacing.
    var p6: i32 = proc_fork();
    if (p6 < 0) { return 60; }
    if (p6 == 0) {
        proc_exec("/no-such-program-here", []);
        exit(23);
    }
    if (proc_waitpid(p6) != 23) { return 61; }
    return 0;
}
`

func TestX86_64SSAProcessFamilyMatchesTheFlatEmitter(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	flat := runPathProbe(t, bin, qemu, dir, "proc", "flat", x86SSAProcSrc, "")
	if flat != 0 {
		t.Fatalf("the flat emitter itself reports %d - the probe is wrong, not the SSA backend", flat)
	}
	if ssa := runPathProbe(t, bin, qemu, dir, "proc", "ssa", x86SSAProcSrc, ""); ssa != flat {
		t.Errorf("-backend ssa reports %d where the flat emitter reports %d.\n\n"+
			"Each code names one assertion in the probe source above; the two backends "+
			"are two implementations of the same builtins and must agree.", ssa, flat)
	}
}
