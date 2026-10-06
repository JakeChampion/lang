package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The seccomp sandbox (FERN_SANDBOX=1, #6071) on the self-host's x86-64 output.
// internal/e2e/seccomp_test.go runs sandboxed programs on a real kernel; these
// read what the compiler emits and what it refuses.

// sandboxSocketProg reaches the socket helpers, Fern bodies over the raw floor,
// so its allowlist has to carry their literal syscall numbers.
const sandboxSocketProg = `function main(): i32 {
    let fd: i32 = tcp_listen(0);
    if (fd < 0) { return 1; }
    let port: i32 = tcp_local_port(fd);
    tcp_close(fd);
    if (port <= 0) { return 2; }
    return 0;
}
`

// sandboxCompile runs the host self-host CLI on src for x86-64 Linux with
// `args`, FERN_SANDBOX set as `sandbox` says, and returns the combined output
// and whether it succeeded.
func sandboxCompile(t *testing.T, src string, sandbox bool, args ...string) (string, bool) {
	t.Helper()
	h := selfHostCLIForHost(t)
	srcPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(h.cli, append(append([]string{"-target", "x86-64-linux"}, args...), srcPath, h.stdlib)...)
	var env []string
	if sandbox {
		env = append(env, "FERN_SANDBOX=1")
	}
	cmd.Env = e2eharness.ChildEnv(env...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// sandboxAsm emits src's x86-64 assembly.
func sandboxAsm(t *testing.T, src string, sandbox bool) string {
	t.Helper()
	asmPath := filepath.Join(t.TempDir(), "out.s")
	if out, ok := sandboxCompile(t, src, sandbox, "-emit", "asm", "-o", asmPath); !ok {
		t.Fatalf("-emit asm (sandbox %v):\n%s", sandbox, out)
	}
	b, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type sandboxBPF struct {
	code, jt, jf int
	k            uint32
}

// parseSandboxFilter decodes the `.Lseccomp_filter` block, two `.long`s per
// sock_filter: { u16 code; u8 jt; u8 jf } and k.
func parseSandboxFilter(t *testing.T, asm string) []sandboxBPF {
	t.Helper()
	at := strings.Index(asm, "\n.Lseccomp_filter:\n")
	if at < 0 {
		t.Fatal("no .Lseccomp_filter block in the emitted asm")
	}
	var out []sandboxBPF
	for _, line := range strings.Split(asm[at+len("\n.Lseccomp_filter:\n"):], "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), ".long ")
		if !ok {
			break
		}
		parts := strings.Split(rest, ",")
		if len(parts) != 2 {
			t.Fatalf("filter line %q: want two words", line)
		}
		var w [2]uint32
		for i, p := range parts {
			n, err := strconv.ParseInt(strings.TrimSpace(p), 0, 64)
			if err != nil {
				t.Fatalf("filter line %q: %v", line, err)
			}
			w[i] = uint32(n)
		}
		out = append(out, sandboxBPF{code: int(w[0] & 0xffff), jt: int(w[0] >> 16 & 0xff), jf: int(w[0] >> 24), k: w[1]})
	}
	return out
}

// TestSelfHostSandboxFilterShape pins the filter's structure: the arch guard,
// an ascending allowlist whose every comparison jumps exactly to ALLOW, the
// deny-by-default fall-through, and the numbers the program's own syscalls
// need, without the prctl and seccomp that install it.
func TestSelfHostSandboxFilterShape(t *testing.T) {
	f := parseSandboxFilter(t, sandboxAsm(t, sandboxSocketProg, true))
	if len(f) < 7 {
		t.Fatalf("filter has %d instructions, too few to be well-formed: %+v", len(f), f)
	}
	const ldWAbs, jeqK, retK = 0x20, 0x15, 0x06
	const kill, allow = 0x80000000, 0x7FFF0000
	if f[0] != (sandboxBPF{code: ldWAbs, k: 4}) {
		t.Errorf("insn 0 = %+v, want a load of seccomp_data.arch", f[0])
	}
	if f[1] != (sandboxBPF{code: jeqK, jt: 1, k: 0xC000003E}) {
		t.Errorf("insn 1 = %+v, want JEQ AUDIT_ARCH_X86_64 skipping the kill", f[1])
	}
	if f[2] != (sandboxBPF{code: retK, k: kill}) {
		t.Errorf("insn 2 = %+v, want RET KILL_PROCESS on another arch", f[2])
	}
	if f[3] != (sandboxBPF{code: ldWAbs, k: 0}) {
		t.Errorf("insn 3 = %+v, want a load of seccomp_data.nr", f[3])
	}
	last := len(f) - 1
	if f[last] != (sandboxBPF{code: retK, k: allow}) {
		t.Errorf("final insn = %+v, want RET ALLOW", f[last])
	}
	if f[last-1] != (sandboxBPF{code: retK, k: kill}) {
		t.Errorf("penultimate insn = %+v, want RET KILL_PROCESS: the fall-through must deny", f[last-1])
	}
	allowed := map[uint32]bool{}
	for i := 4; i < last-1; i++ {
		if f[i].code != jeqK || f[i].jf != 0 {
			t.Fatalf("insn %d = %+v, want a JEQ falling through to the next comparison", i, f[i])
		}
		if target := i + 1 + f[i].jt; target != last {
			t.Errorf("insn %d (allow %d) jumps to %d, want %d (the ALLOW)", i, f[i].k, target, last)
		}
		if i > 4 && f[i].k <= f[i-1].k {
			t.Errorf("insn %d allows %d after %d: the list is not ascending and unique", i, f[i].k, f[i-1].k)
		}
		allowed[f[i].k] = true
	}
	// close, socket, bind, listen, getsockname, exit.
	for _, nr := range []uint32{3, 41, 49, 50, 51, 60} {
		if !allowed[nr] {
			t.Errorf("syscall %d is issued by the program but not allowed: the binary would be killed on a legitimate path", nr)
		}
	}
	for _, nr := range []uint32{157, 317} {
		if allowed[nr] {
			t.Errorf("syscall %d (prctl / seccomp) is allowed: both run before the filter applies, so allowing them only lets hijacked code install a filter of its own", nr)
		}
	}
}

// TestSelfHostSandboxOffEmitsNothing: without FERN_SANDBOX the output carries
// no trace of the sandbox, and with it `_start` calls the installer.
func TestSelfHostSandboxOffEmitsNothing(t *testing.T) {
	off := sandboxAsm(t, sandboxSocketProg, false)
	for _, needle := range []string{"__fern_seccomp_install", ".Lseccomp_filter"} {
		if strings.Contains(off, needle) {
			t.Errorf("sandbox-off asm contains %q", needle)
		}
	}
	on := sandboxAsm(t, sandboxSocketProg, true)
	if !strings.Contains(on, "_start:\n    call __fern_seccomp_install\n") {
		t.Error("sandbox-on asm does not call __fern_seccomp_install first thing in _start")
	}
}

// TestSelfHostSandboxRefusesRunTimeSyscallNumber: a raw syscall whose number
// is computed at run time cannot be allowlisted, so the sandbox refuses the
// program rather than install a filter that kills it at that call.
func TestSelfHostSandboxRefusesRunTimeSyscallNumber(t *testing.T) {
	const src = `function main(): i32 {
    let r: i64 = __syscall3(args().len() + 38, 0, 0, 0);
    if (r > 0) { return 0; }
    return 1;
}
`
	exe := filepath.Join(t.TempDir(), "prog")
	if out, ok := sandboxCompile(t, src, false, "-o", exe); !ok {
		t.Fatalf("control compile without the sandbox failed:\n%s", out)
	}
	out, ok := sandboxCompile(t, src, true, "-o", exe)
	if ok {
		t.Fatal("FERN_SANDBOX=1 compiled a program whose syscall number is a run-time value")
	}
	if !strings.Contains(out, "__syscall3 passes the syscall number at run time") {
		t.Errorf("refusal does not name the call:\n%s", out)
	}
}

// TestSelfHostSandboxRefusesPerModuleEmit: a per-module unit cannot see the
// syscalls its siblings issue, so the sandbox refuses that emit.
func TestSelfHostSandboxRefusesPerModuleEmit(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	driverBin := buildSelfHostBin(t, x86gcc, writeSelfHostModloadProject(t), "asm_modload_run.fern", "sandboxpermoduledriver")
	proj := t.TempDir()
	mustWrite(t, proj, "leaf.fern", "pub function seven(): i32 { return 7; }\n")
	mustWrite(t, proj, "main.fern", "import \"./leaf\";\n\nfunction main(): i32 { return leaf.seven(); }\n")
	copyStdlibTree(t, proj)
	cmd := runX86_64Bin(x86runner, driverBin, filepath.Join(proj, "main.fern"), "-target", "x86-64-linux", "-per-module-emit", "0")
	cmd.Env = e2eharness.ChildEnv("FERN_SANDBOX=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("FERN_SANDBOX=1 emitted a per-module unit")
	}
	if !strings.Contains(string(out), "FERN_SANDBOX=1 needs the whole-program emit") {
		t.Errorf("refusal does not say why:\n%s", out)
	}
}

// TestSelfHostSandboxNoBareSyscallEmit keeps the allowlist exact: the x86-64 emitters
// write a `syscall` only through EmitState's syscall and raw_syscall, which
// record it. A `syscall` written any other way is one the filter would kill.
func TestSelfHostSandboxNoBareSyscallEmit(t *testing.T) {
	files, err := filepath.Glob("../../compiler/*.fern")
	if err != nil || len(files) == 0 {
		t.Fatalf("no self-host sources: %v", err)
	}
	bare := regexp.MustCompile(`\bsyscall\\n`)
	recorder := regexp.MustCompile(`^pub function \(s: EmitState\) (raw_)?syscall\(`)
	var offenders []string
	for _, f := range files {
		if strings.Contains(filepath.Base(f), "arm64") {
			continue // arm64 issues `svc`, and the sandbox is x86-64 only
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fn := ""
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "function ") || strings.HasPrefix(line, "pub function ") {
				fn = line
			}
			if bare.MatchString(line) && !(filepath.Base(f) == "asmcore.fern" && recorder.MatchString(fn)) {
				offenders = append(offenders, filepath.Base(f)+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
	}
	if len(offenders) > 0 {
		t.Errorf("a syscall written outside EmitState's recorders is missing from the sandbox allowlist; use s.syscall(nr), or s.raw_syscall(callee) for a run-time number:\n  %s", strings.Join(offenders, "\n  "))
	}
}
