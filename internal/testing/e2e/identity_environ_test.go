package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// identitySource asks for all four ids and both lists, and returns a code
// naming the first thing that disagreed with what the Go side sees.
//
// Every value here is machine-dependent, so the expectations are baked in
// from the test process rather than pinned: the compiled program is a child
// of this one and inherits its credentials and its environment.
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
	// The order is the kernel's and both sides read the same kernel, so
	// this is an element-for-element check rather than a set comparison.
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

// identityProbe seeds one environment entry this process did not already
// have, so the child's list can be checked for a value rather than only a
// count, and reports what the child should see.
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

// wasm has no users — `internal/pkg/platforms` refuses getuid / getgid /
// getgroups there with E066 — but it does have an environment, so
// `environ()` is a real question on both previews.
func TestWasmEnviron(t *testing.T) {
	src := `function main(): i32 {
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
	stdout, stderr, _ := runWasmStdinEnv(t, src, "",
		[]string{"FERN_ENVIRON_PROBE=identity-slice", "FERN_ENVIRON_OTHER=two"})
	// `seen`: an entry is missing or duplicated. `len`: the list is not
	// the whole environment. main's return value reaches stdout under
	// --invoke, so the tag is what distinguishes them.
	if !strings.Contains(stdout, "ok") {
		t.Errorf("stdout = %q, want it to contain \"ok\"\nstderr=%q", stdout, stderr)
	}
}

// The preview-1 leg of the same probe. `buildWasmCore` builds with
// Preview2WASI, so the wasm tests above only ever reach the
// get-environment path; preview 1's environ_sizes_get / environ_get pair
// is a different body and needs its own run. A preview-1 core module runs
// directly under wasmtime, no adapter and no component.
func TestWasmPreview1Environ(t *testing.T) {
	e2eharness.Wasmtime(t)
	src := `function main(): i32 {
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
	wasmPath := e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, src, nil)
	cmd := exec.Command("wasmtime", "run",
		"--env", "FERN_ENVIRON_PROBE=identity-slice",
		"--env", "FERN_ENVIRON_OTHER=two",
		"--invoke", "main", wasmPath)
	cmd.Env = append(os.Environ(), "WASMTIME_NEW_CLI=0")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	_ = cmd.Run()
	if !strings.Contains(so.String(), "ok") {
		t.Errorf("stdout = %q, want it to contain \"ok\"\nstderr=%q", so.String(), se.String())
	}
}
