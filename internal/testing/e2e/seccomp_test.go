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

	"github.com/jakechampion/lang/internal/tables/symname"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func buildSandboxed(t *testing.T, src string, sandbox bool) string {
	t.Helper()
	return buildSandboxedPatched(t, src, sandbox, nil)
}

func buildSandboxedPatched(t *testing.T, src string, sandbox bool, patch func(string) string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	return buildSandboxedPath(t, srcPath, sandbox, patch)
}

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
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	var env []string
	if sandbox {
		env = append(env, "FERN_SANDBOX=1")
	}
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", e2eharness.TargetX86_64Linux, "-emit", "asm", "-o", asmPath, srcPath, e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv(env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host -emit asm (sandbox %v): %v\n%s", sandbox, err, out)
	}
	if patch != nil {
		b, err := os.ReadFile(asmPath)
		if err != nil {
			t.Fatalf("read asm: %v", err)
		}
		asm := string(b)
		patched := patch(asm)
		if patched == asm {
			t.Fatal("asm patch matched nothing — the emitted shape changed and the test is no longer exercising what it claims")
		}
		if err := os.WriteFile(asmPath, []byte(patched), 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	return binPath
}

var seccompStatusRe = regexp.MustCompile(`(?m)^Seccomp:\s*(\d+)`)

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
		{"sandbox on", true, "2"},
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

func TestSeccompDoesNotBreakWorkingPrograms(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"arithmetic only", `function main(): i32 { let a: i32 = 6; return a * 7 - 42; }`},
		{"constant data, no heap", `enum Light { On, Off, Dim(i32) }
function level(l: Light): i32 {
    match (l) {
        On => { return 100; },
        Off => { return 0; },
        Dim(n) => { return n; }
    }
}
function main(): i32 { return level(Dim(42)) - 42; }`},
		{"heap + strings", `function main(): i32 {
    let xs: i32[] = [1, 2, 3, 4];
    let s: string = "a" + "b";
    print(s);
    return xs.len() - 4;
}`},
		{"loops and allocation churn", `function main(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let row: i32[] = [i, i + 1, i + 2];
        n = n + row.len();
        i = i + 1;
    }
    print("churned");
    return n - 600;
}`},
		{"clock", `function main(): i32 {
    let t: i64 = now_unix_ms();
    if (t > 0) { return 0; }
    return 1;
}`},
		{"sockets", `function main(): i32 {
    let fd: i32 = tcp_listen(0);
    if (fd < 0) { return 1; }
    let port: i32 = tcp_local_port(fd);
    tcp_close(fd);
    if (port <= 0) { return 2; }
    return 0;
}`},
		{"caught signal", `function main(): i32 {
    if (signal_catch(10) != 0) { return 1; }
    match (set_process_group(0, 0)) {
        Ok(_) => {},
        Err(_) => { return 2; }
    }
    match (signal_send(0, 10)) {
        Ok(_) => {},
        Err(_) => { return 3; }
    }
    let n: i32 = 0;
    while (n < 5000) {
        if (signal_taken(10)) {
            print("caught");
            return 0;
        }
        sleep_ms(1 as i64);
        n = n + 1;
    }
    return 4;
}`},
		{"raised signal", `function main(): i32 {
    if (signal_catch(10) != 0) { return 1; }
    if (signal_raise(10) != 0) { return 2; }
    if (!signal_taken(10)) { return 3; }
    print("raised");
    return 0;
}`},
		{"interrupting catch", `function main(): i32 {
    if (signal_catch_interrupting(10) != 0) { return 1; }
    if (signal_raise(10) != 0) { return 2; }
    if (!signal_taken(10)) { return 3; }
    print("caught");
    return 0;
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

			if plainCode != 0 {
				t.Fatalf("the unsandboxed program exits %d; every case here exits 0 when it works", plainCode)
			}
			if plainCode != sandboxCode {
				t.Errorf("exit code differs: plain %d, sandboxed %d — a sandboxed program dying where the plain one did not means the filter is missing a syscall the program legitimately makes", plainCode, sandboxCode)
			}
			if string(plainOut) != string(sandboxOut) {
				t.Errorf("stdout differs:\n plain     = %q\n sandboxed = %q", plainOut, sandboxOut)
			}
		})
	}
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

func injectExecve(asm string) string {
	label := symname.Fn("main") + ":\n"
	return strings.Replace(asm, label,
		label+"    movq $59, %rax\n    movq $0, %rdi\n    movq $0, %rsi\n    movq $0, %rdx\n    syscall\n", 1)
}

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
		if f.rejectionCase() {
			continue
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
