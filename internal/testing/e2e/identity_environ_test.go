package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func identitySource(uid, gid, euid, egid int, groups []int, envCount int, probe string) string {
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
		fmt.Fprintf(&b, "    if (gs[%d] != %d) { return %d; }\n", i, g, 10+i)
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
    // Every entry carries its '=' — that is what makes the raw list
    // usable without a second lookup.
    let k: i32 = 0;
    while (k < e.len()) {
        let entry: string = e[k];
        let eq: i32 = 0 - 1;
        let j: i32 = 0;
        while (j < entry.len()) {
            if (entry[j] == b'=') { if (eq < 0) { eq = j; } }
            j = j + 1;
        }
        if (eq < 1) { return 8; }
        k = k + 1;
    }
    return 0;
}
`, envCount, probe)
	return b.String()
}

func identityProbe(t *testing.T) (groups []int, envCount int, probe string) {
	t.Helper()
	t.Setenv("FERN_ENVIRON_PROBE", "identity-slice")
	gs, err := os.Getgroups()
	if err != nil {
		t.Fatalf("getgroups: %v", err)
	}
	return gs, len(os.Environ()), "FERN_ENVIRON_PROBE=identity-slice"
}

func TestX86_64IdentityAndEnviron(t *testing.T) {
	groups, envCount, probe := identityProbe(t)
	src := identitySource(os.Getuid(), os.Getgid(), os.Geteuid(), os.Getegid(), groups, envCount, probe)
	code, out := compileRunX86_64WithSetup(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see identitySource)\n%s", code, out)
	}
}

func TestArm64IdentityAndEnviron(t *testing.T) {
	groups, envCount, probe := identityProbe(t)
	src := identitySource(os.Getuid(), os.Getgid(), os.Geteuid(), os.Getegid(), groups, envCount, probe)
	out, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see identitySource)\n%s", code, out)
	}
}

func TestInterpIdentityAndEnviron(t *testing.T) {
	groups, envCount, probe := identityProbe(t)
	src := identitySource(os.Getuid(), os.Getgid(), os.Geteuid(), os.Getegid(), groups, envCount, probe)
	if code := runInterpExit(t, src); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see identitySource)", code)
	}
}

const wasmEnvironSrc = `function main(): i32 {
    let e: string[] = environ();
    let seen: i32 = 0;
    let i: i32 = 0;
    while (i < e.len()) {
        if (e[i] == "FERN_ENVIRON_PROBE=identity-slice") { seen = seen + 1; }
        if (e[i] == "FERN_ENVIRON_OTHER=two") { seen = seen + 10; }
        i = i + 1;
    }
    if (seen != 11) { write("seen"); return 1; }
    if (e.len() != 2) { write("len"); return 2; }
    write("ok");
    return 0;
}`

var wasmEnvironVars = []string{"FERN_ENVIRON_PROBE=identity-slice", "FERN_ENVIRON_OTHER=two"}

func TestWasmEnviron(t *testing.T) {
	stdout, stderr, ec := runCLIComponent(t, wasmEnvironSrc, runOpts{envs: wasmEnvironVars})
	if ec != 0 || stdout != "ok" {
		t.Errorf("exit %d, stdout = %q, want 0 and \"ok\"\nstderr=%q", ec, stdout, stderr)
	}
}

func TestWasmPreview1Environ(t *testing.T) {
	stdout, stderr, _ := runWasmStdinEnv(t, wasmEnvironSrc, "", wasmEnvironVars)
	if !strings.Contains(stdout, "ok") {
		t.Errorf("stdout = %q, want it to contain \"ok\"\nstderr=%q", stdout, stderr)
	}
}
