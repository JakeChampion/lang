package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// --- Seccomp sandbox, runtime half (#6071) ------------------------
//
// internal/codegen/x86_64/seccomp_test.go decodes the emitted BPF and
// pins its shape. These two tests cover what that cannot:
//
//   - the filter actually LOADS (a seccomp(2) that quietly failed
//     would leave every structural assertion passing and the process
//     unprotected)
//   - the filter is not too TIGHT, which is the real hazard — an
//     over-tight filter is a crash, not a warning, and it would only
//     show up on whichever runtime path nobody exercised.

// buildSandboxed compiles src with the sandbox on or off and returns
// the binary path. Native x86-64 only: seccomp is a Linux facility and
// qemu-user does not emulate it faithfully.
func buildSandboxed(t *testing.T, src string, sandbox bool) string {
	t.Helper()
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" {
		t.Skip("seccomp sandbox is x86-64 Linux only; qemu-user does not emulate it faithfully")
	}
	gcc, runnerCmd := x86_64Tooling(t)
	if len(runnerCmd) != 0 {
		t.Skip("emulated x86-64 runner: seccomp semantics are not faithful under qemu-user")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	prev := ast.SandboxEnabled
	t.Cleanup(func() { ast.SandboxEnabled = prev })
	ast.SandboxEnabled = sandbox
	asm, emitErr := x86_64.Emit(prog, info)
	ast.SandboxEnabled = prev
	if emitErr != nil {
		t.Fatalf("emit: %v", emitErr)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	return binPath
}

var seccompStatusRe = regexp.MustCompile(`(?m)^Seccomp:\s*(\d+)`)

// TestSeccompFilterIsLoadedAtRuntime proves the filter is genuinely
// installed, by reading /proc/<pid>/status of the running process from
// outside it. This is the assertion the structural tests cannot make:
// __fern_seccomp_install deliberately ignores a seccomp(2) failure (so
// a kernel without CONFIG_SECCOMP_FILTER degrades to running
// unhardened rather than refusing to boot), which means a filter that
// never loads looks exactly like one that did from the inside.
//
// Reading procfs from the test rather than from Fern is deliberate
// too: read_file returns empty for procfs, whose files report size 0.
func TestSeccompFilterIsLoadedAtRuntime(t *testing.T) {
	const sleeper = `function main(): i32 {
    sleep_ms(1500);
    return 0;
}`
	for _, tc := range []struct {
		name    string
		sandbox bool
		want    string
	}{
		{"sandbox off", false, "0"},
		{"sandbox on", true, "2"}, // SECCOMP_MODE_FILTER
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildSandboxed(t, sleeper, tc.sandbox)
			cmd := exec.Command(bin)
			if err := cmd.Start(); err != nil {
				t.Fatalf("start: %v", err)
			}
			defer func() { _ = cmd.Wait() }()
			status, err := readStatusWhenRunning(cmd.Process.Pid)
			if err != nil {
				t.Fatalf("read /proc/%d/status: %v", cmd.Process.Pid, err)
			}
			m := seccompStatusRe.FindStringSubmatch(status)
			if m == nil {
				t.Skip("kernel does not report a Seccomp field in /proc/<pid>/status")
			}
			if m[1] != tc.want {
				t.Errorf("Seccomp = %s, want %s (0 = disabled, 2 = SECCOMP_MODE_FILTER)", m[1], tc.want)
			}
		})
	}
}

// readStatusWhenRunning reads /proc/<pid>/status, retrying briefly so
// the read lands while the child is still alive.
func readStatusWhenRunning(pid int) (string, error) {
	var last error
	for i := 0; i < 50; i++ {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
		if err == nil && strings.Contains(string(b), "Seccomp") {
			return string(b), nil
		}
		if err != nil {
			last = err
		}
	}
	if last != nil {
		return "", last
	}
	return "", os.ErrNotExist
}

// TestSeccompDoesNotBreakWorkingPrograms is the over-tightness gate,
// and the reason the sandbox is opt-in rather than on by default. Each
// program must behave identically sandboxed and not — same stdout,
// same exit code. A syscall the filter forgot shows up here as a
// SIGSYS death rather than as a subtle difference.
func TestSeccompDoesNotBreakWorkingPrograms(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"arithmetic only", `function main(): i32 { var a: i32 = 6; return a * 7 - 42; }`},
		{"heap + strings", `function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4];
    var s: string = "a" + "b";
    print(s);
    return xs.len() - 4;
}`},
		{"loops and allocation churn", `function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var row: i32[] = [i, i + 1, i + 2];
        n = n + row.len();
        i = i + 1;
    }
    print("churned");
    return n - 600;
}`},
		{"clock", `function main(): i32 {
    var t: i64 = now_unix_ms();
    if (t > 0) { return 0; }
    return 1;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plainBin := buildSandboxed(t, tc.src, false)
			sandboxBin := buildSandboxed(t, tc.src, true)

			plainOut, err := exec.Command(plainBin).Output()
			plainCode := exitCodeOf(err)
			sandboxOut, err := exec.Command(sandboxBin).Output()
			sandboxCode := exitCodeOf(err)

			if plainCode != sandboxCode {
				t.Errorf("exit code differs: plain %d, sandboxed %d — a sandboxed program dying where the plain one did not means the filter is missing a syscall the program legitimately makes", plainCode, sandboxCode)
			}
			if string(plainOut) != string(sandboxOut) {
				t.Errorf("stdout differs:\n plain     = %q\n sandboxed = %q", plainOut, sandboxOut)
			}
		})
	}
}

// exitCodeOf extracts a process exit code from exec's error, treating
// a nil error as 0. A signal death (SIGSYS from a filter violation)
// surfaces as a negative or 128+n code depending on the platform; any
// mismatch against the unsandboxed run fails the test either way.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}
