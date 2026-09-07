package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// selfHostIdentitySource asks the self-host compiler for the four id builtins
// and the two list-returning ones, and returns a code naming the first thing
// that disagreed with what the Go side sees.
//
// Every value is machine-dependent, so the expectations are baked in from the
// test process rather than pinned: the compiled program is a child of this one
// and inherits its credentials and its environment.
func selfHostIdentitySource(uid, gid, euid, egid int, groups []int, envCount int, probe string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `function main(): i32 {
    if (getuid() != (%d as u32)) { return 1; }
    if (getgid() != (%d as u32)) { return 2; }
    if (geteuid() != (%d as u32)) { return 3; }
    if (getegid() != (%d as u32)) { return 4; }
    var gs: i64[] = getgroups();
    if (gs.len() != %d) { return 5; }
`, uid, gid, euid, egid, len(groups))
	for i, g := range groups {
		fmt.Fprintf(&b, "    if (gs[%d] != (%d as i64)) { return %d; }\n", i, g, 10+i)
	}
	fmt.Fprintf(&b, `    var e: string[] = environ();
    if (e.len() != %d) { return 6; }
    var seen: i32 = 0;
    var i: i32 = 0;
    while (i < e.len()) {
        if (e[i] == %q) { seen = seen + 1; }
        i = i + 1;
    }
    if (seen != 1) { return 7; }
    return 0;
}
`, envCount, probe)
	return b.String()
}

// TestSelfHostIdentityAndEnvironIR pins the four id builtins plus getgroups
// and environ on the self-host x86-64 IR path.
//
// The two list-returning ones are where the self-host and native differ most:
// native builds the array in hand-written assembly and caches it, while the
// self-host compiles a Fern body (`asmcore.rt_src_getgroups` /
// `rt_src_environ`) through its own array lowering. getgroups' widening is the
// part worth a gate — gid_t is 32-bit unsigned and the slots are 8 bytes, so
// the body assembles each id from its four bytes rather than through the
// signed __load_i32 a gid past 2^31 would come back negative from.
func TestSelfHostIdentityAndEnvironIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("identity test runs only natively (reads the host's own credentials)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	t.Setenv("FERN_ENVIRON_PROBE", "selfhost-identity")
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("getgroups: %v", err)
	}
	src := selfHostIdentitySource(os.Getuid(), os.Getgid(), os.Geteuid(), os.Getegid(),
		groups, len(os.Environ()), "FERN_ENVIRON_PROBE=selfhost-identity")

	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	progBin := buildBin(t, gcc, dir, "identity_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("identity program exited %d, want 0 — the code names the case (see selfHostIdentitySource)", code)
	}
}
