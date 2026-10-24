package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func selfHostIdentitySource(uid, gid, euid, egid int, groups []int, envCount int, probe string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `function main(): i32 {
    if (getuid() != (%d as u32)) { return 1; }
    if (getgid() != (%d as u32)) { return 2; }
    if (geteuid() != (%d as u32)) { return 3; }
    if (getegid() != (%d as u32)) { return 4; }
    let gs: i64[] = getgroups();
    if (gs.len() != %d) { return 5; }
`, uid, gid, euid, egid, len(groups))
	for i, g := range groups {
		fmt.Fprintf(&b, "    if (gs[%d] != (%d as i64)) { return %d; }\n", i, g, 10+i)
	}
	fmt.Fprintf(&b, `    let e: string[] = environ();
    if (e.len() != %d) { return 6; }
    let seen: i32 = 0;
    let i: i32 = 0;
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

func TestSelfHostIdentityAndEnvironIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("identity test runs only natively (reads the host's own credentials)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	t.Setenv("FERN_ENVIRON_PROBE", "selfhost-identity")
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("getgroups: %v", err)
	}
	src := selfHostIdentitySource(os.Getuid(), os.Getgid(), os.Geteuid(), os.Getegid(),
		groups, len(os.Environ()), "FERN_ENVIRON_PROBE=selfhost-identity")

	cmd := exec.Command(driverBin)
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
