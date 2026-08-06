package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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
// pins its shape. The tests here cover what inspecting the emitted form
// cannot, each answering a different question a filter can fail:
//
//   - does it LOAD? (a seccomp(2) that quietly failed would leave every
//     structural assertion passing and the process unprotected)
//   - does it DENY? (a filter permitting everything loads just fine)
//   - is it too TIGHT — on four hand-written programs, and then across
//     the whole fixture corpus? Over-tightness is the real hazard: it is
//     a crash rather than a warning, and it surfaces on whichever
//     runtime path nobody exercised.

// buildSandboxed compiles src with the sandbox on or off and returns
// the binary path. Native x86-64 only: seccomp is a Linux facility and
// qemu-user does not emulate it faithfully.
func buildSandboxed(t *testing.T, src string, sandbox bool) string {
	t.Helper()
	return buildSandboxedPatched(t, src, sandbox, nil)
}

// buildSandboxedPatched is buildSandboxed with a hook to rewrite the
// emitted assembly before it is assembled. Only TestSeccompFilterDenies
// uses it, to splice in a syscall no Fern source can express — see there
// for why that is the only way to test the deny path.
func buildSandboxedPatched(t *testing.T, src string, sandbox bool, patch func(string) string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	return buildSandboxedPath(t, srcPath, sandbox, patch)
}

// buildSandboxedPath is buildSandboxed over a source file already on disk,
// so a fixture's imports resolve against its own directory rather than a
// copy in a temp dir. The corpus gate below needs that; the inline-source
// helpers above are a thin shim over it.
func buildSandboxedPath(t *testing.T, srcPath string, sandbox bool, patch func(string) string) string {
	t.Helper()
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" {
		t.Skip("seccomp sandbox is x86-64 Linux only; qemu-user does not emulate it faithfully")
	}
	gcc, runnerCmd := x86_64Tooling(t)
	if len(runnerCmd) != 0 {
		t.Skip("emulated x86-64 runner: seccomp semantics are not faithful under qemu-user")
	}
	dir := t.TempDir()
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
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
	if patch != nil {
		patched := patch(asm)
		if patched == asm {
			t.Fatal("asm patch matched nothing — the emitted shape changed and the test is no longer exercising what it claims")
		}
		asm = patched
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
			got, err := pollSeccompStatus(cmd.Process.Pid, tc.want)
			if errors.Is(err, errNoSeccompField) {
				t.Skip("kernel does not report a Seccomp field in /proc/<pid>/status")
			}
			if err != nil {
				t.Fatalf("read /proc/%d/status: %v", cmd.Process.Pid, err)
			}
			if got != tc.want {
				t.Errorf("Seccomp = %s, want %s (0 = disabled, 2 = SECCOMP_MODE_FILTER)", got, tc.want)
			}
		})
	}
}

var errNoSeccompField = errors.New("no Seccomp field in /proc/<pid>/status")

// pollSeccompStatus samples the Seccomp field of /proc/<pid>/status
// until it reaches want or the window expires, returning the last value
// seen.
//
// Sampling until it settles, rather than reading once, is the whole
// contract: exec.Cmd.Start returns as soon as the child is forked —
// /proc/<pid>/status already exists and already reports Seccomp: 0,
// microseconds before the child reaches _start and installs the filter.
// A single early read therefore reports an unsandboxed process for a
// filter that does install. The window is shorter than the child's
// sleep, so the process is still alive throughout it, and the
// unsandboxed case polls it out in full — a returned 0 there means the
// field never became anything else.
func pollSeccompStatus(pid int, want string) (string, error) {
	deadline := time.Now().Add(1200 * time.Millisecond)
	last := ""
	for {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
		if err != nil {
			if last != "" {
				return last, nil
			}
			return "", err
		}
		m := seccompStatusRe.FindStringSubmatch(string(b))
		if m == nil {
			return "", errNoSeccompField
		}
		last = m[1]
		if last == want || !time.Now().Before(deadline) {
			return last, nil
		}
		time.Sleep(2 * time.Millisecond)
	}
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

// injectExecve splices a raw execve(2) into the top of main, which the
// filter never permits for a program that does not use `subprocess`.
//
// Patching assembly is deliberate and is the only way to reach this.
// The allowlist is derived from the syscalls the backend actually emits,
// so by construction no Fern source can make a syscall the filter denies
// — the feature would be broken if it could. What the filter defends
// against is control flow redirected somewhere the compiler never
// emitted, which is precisely what splicing an instruction models.
func injectExecve(asm string) string {
	return strings.Replace(asm, "main:\n",
		"main:\n\tmov eax, 59\n\txor edi, edi\n\txor esi, esi\n\txor edx, edx\n\tsyscall\n", 1)
}

// TestSeccompFilterDenies is the only test that observes the filter's
// EFFECT on a real kernel. Every other test in the feature asserts
// something about its emitted form: the structural tests decode the BPF
// and pin its shape, FilterIsLoadedAtRuntime reads Seccomp: 2 out of
// procfs, and DoesNotBreakWorkingPrograms shows nothing legitimate died.
// None of those runs a denied syscall, so none can tell you the kernel
// agrees with our reading of the seccomp ABI.
//
// That distinction is not theoretical. Mutating the filter's syscall-
// number load to a wrong offset — which makes the kernel reject the
// filter outright, leaving the process unhardened, because
// __fern_seccomp_install deliberately ignores a seccomp(2) failure —
// leaves DoesNotBreakWorkingPrograms GREEN. "Nothing broke" is not
// evidence that anything is protected; only a denied syscall is.
//
// (The two mutations tried so far are also caught structurally, so this
// is a second, independent line of defence rather than the sole one.
// The structural tests guard the bytes; this one guards the behaviour.)
//
// The control run is what makes this a proof rather than a coincidence:
// the same spliced execve, with the sandbox off, exits 0 because
// execve(NULL, NULL, NULL) merely returns EFAULT. So a SIGSYS death in
// the sandboxed run can only have come from the filter.
func TestSeccompFilterDenies(t *testing.T) {
	const src = `function main(): i32 { return 0; }`

	sandboxed := buildSandboxedPatched(t, src, true, injectExecve)
	cmd := exec.Command(sandboxed)
	_ = cmd.Run()
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no wait status for the sandboxed run")
	}
	if !ws.Signaled() || ws.Signal() != syscall.SIGSYS {
		t.Errorf("sandboxed run: signaled=%v signal=%v exit=%d — a denied syscall must kill with SIGSYS",
			ws.Signaled(), ws.Signal(), cmd.ProcessState.ExitCode())
	}

	plain := buildSandboxedPatched(t, src, false, injectExecve)
	ctl := exec.Command(plain)
	_ = ctl.Run()
	cws, ok := ctl.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no wait status for the control run")
	}
	if cws.Signaled() {
		t.Fatalf("control run died from signal %v — the spliced execve is fatal on its own, so the sandboxed SIGSYS proves nothing", cws.Signal())
	}
	if code := ctl.ProcessState.ExitCode(); code != 0 {
		t.Errorf("control run exited %d, want 0 — execve(NULL) should merely return EFAULT", code)
	}
}

// TestSeccompFixtureCorpus is #6071's over-tightness gate at corpus scale,
// and the precondition for ever defaulting the sandbox on.
//
// TestSeccompDoesNotBreakWorkingPrograms covers four hand-written
// programs. Four programs cannot establish that a filter derived from
// one program's emitted syscalls is right for every program, and the
// failure mode is not subtle: a syscall the filter forgot is a SIGSYS
// kill, in someone's build, on whichever path nobody exercised. The
// fixture corpus is the broadest body of real Fern programs there is, so
// it is the honest evidence.
//
// Each fixture runs twice — sandboxed and not — and must behave
// identically. Comparing against the unsandboxed run rather than against
// the fixture's recorded expectation is deliberate: it isolates the
// filter as the only variable, so a fixture that is already failing for
// unrelated reasons cannot be mistaken for a sandbox regression.
//
// Env-gated because it builds and links ~330 binaries twice. CI runs it
// on its own lane; locally, reach for it when changing the syscall
// inventory or the filter.
func TestSeccompFixtureCorpus(t *testing.T) {
	if os.Getenv("RUN_SECCOMP_CORPUS") != "1" {
		t.Skip("set RUN_SECCOMP_CORPUS=1 to run the sandbox over the whole fixture corpus (~330 fixtures, built twice)")
	}
	root := conformanceCases
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no fixtures under %s — the gate would pass vacuously", root)
	}

	ran := 0
	for _, name := range names {
		dir, err := filepath.Abs(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("abs %s: %v", name, err)
		}
		f := loadFixture(t, dir)
		if f.compileError {
			continue // never produces a binary
		}
		ran++
		t.Run(name, func(t *testing.T) {
			plainBin := buildSandboxedPath(t, f.mainPath, false, nil)
			sandboxBin := buildSandboxedPath(t, f.mainPath, true, nil)

			plainOut, plainCode := runSandboxCandidate(plainBin, f.stdin)
			sandboxOut, sandboxCode := runSandboxCandidate(sandboxBin, f.stdin)

			if plainCode != sandboxCode {
				t.Errorf("exit code differs: plain %d, sandboxed %d — the filter is missing a syscall this program legitimately makes (a SIGSYS shows up as %d)",
					plainCode, sandboxCode, 128+int(syscall.SIGSYS))
			}
			if plainOut != sandboxOut {
				t.Errorf("stdout differs:\n plain     = %q\n sandboxed = %q", plainOut, sandboxOut)
			}
		})
	}
	if ran == 0 {
		t.Fatal("every fixture was a compile-error case — nothing was actually run under the filter")
	}
	t.Logf("%d runnable fixtures exercised under the seccomp filter", ran)
}

// runSandboxCandidate runs bin with stdin and returns its stdout and an
// exit code, rendering a signal death as 128+signo so a SIGSYS kill is
// comparable against an ordinary exit rather than collapsing to -1.
func runSandboxCandidate(bin, stdin string) (string, int) {
	cmd := exec.Command(bin)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, _ := cmd.Output()
	code := 0
	if ps := cmd.ProcessState; ps != nil {
		if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			code = 128 + int(ws.Signal())
		} else {
			code = ps.ExitCode()
		}
	}
	return string(out), code
}
