package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const sandboxSocketProg = `function main(): i32 {
    let fd: i32 = tcp_listen(0);
    if (fd < 0) { return 1; }
    let port: i32 = tcp_local_port(fd);
    tcp_close(fd);
    if (port <= 0) { return 2; }
    return 0;
}
`

// sandboxTarget is one target the sandbox exists on, with the constants its
// filter and installer carry.
type sandboxTarget struct {
	name      string
	arch      uint32   // the AUDIT_ARCH_* word the filter checks first
	installer []uint32 // prctl and seccomp: issued before the filter applies, so never allowed
	program   []uint32 // the calls sandboxSocketProg makes on this target
	install   string   // the call of the installer in _start
	mainCall  string   // the call of main, which the installer must precede
}

var sandboxTargets = []sandboxTarget{
	{
		name:      e2eharness.TargetX86_64Linux,
		arch:      0xC000003E,
		installer: []uint32{157, 317},
		program:   []uint32{3, 41, 49, 50, 51, 231},
		install:   "    call __fern_seccomp_install\n",
		mainCall:  "    call __fn_main\n",
	},
	{
		name:      e2eharness.TargetArm64Linux,
		arch:      0xC00000B7,
		installer: []uint32{167, 277},
		program:   []uint32{57, 94, 198, 200, 201, 204},
		install:   "    bl __fern_seccomp_install\n",
		mainCall:  "    bl __fn_main\n",
	},
}

func sandboxCompile(t *testing.T, target string, src string, sandbox bool, args ...string) (string, bool) {
	t.Helper()
	return sandboxCompileWith(t, selfHostCLIForHost(t).cli, target, src, sandbox, args...)
}

func sandboxCompileWith(t *testing.T, compiler string, target string, src string, sandbox bool, args ...string) (string, bool) {
	t.Helper()
	h := selfHostCLIForHost(t)
	srcPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(compiler, append(append([]string{"-target", target}, args...), srcPath, h.stdlib)...)
	var env []string
	if sandbox {
		env = append(env, "FERN_SANDBOX=1")
	}
	cmd.Env = e2eharness.ChildEnv(env...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func sandboxAsm(t *testing.T, target string, src string, sandbox bool) string {
	t.Helper()
	asmPath := filepath.Join(t.TempDir(), "out.s")
	if out, ok := sandboxCompile(t, target, src, sandbox, "-emit", "asm", "-o", asmPath); !ok {
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

func TestSelfHostSandboxFilterShape(t *testing.T) {
	for _, tgt := range sandboxTargets {
		t.Run(tgt.name, func(t *testing.T) {
			checkSandboxFilterShape(t, tgt)
		})
	}
}

func checkSandboxFilterShape(t *testing.T, tgt sandboxTarget) {
	f := parseSandboxFilter(t, sandboxAsm(t, tgt.name, sandboxSocketProg, true))
	if len(f) < 7 {
		t.Fatalf("filter has %d instructions, too few to be well-formed: %+v", len(f), f)
	}
	const ldWAbs, jeqK, retK = 0x20, 0x15, 0x06
	const kill, allow = 0x80000000, 0x7FFF0000
	if f[0] != (sandboxBPF{code: ldWAbs, k: 4}) {
		t.Errorf("insn 0 = %+v, want a load of seccomp_data.arch", f[0])
	}
	if f[1] != (sandboxBPF{code: jeqK, jt: 1, k: tgt.arch}) {
		t.Errorf("insn 1 = %+v, want JEQ on the audit arch %#x skipping the kill", f[1], tgt.arch)
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
	for _, nr := range tgt.program {
		if !allowed[nr] {
			t.Errorf("syscall %d is issued by the program but not allowed: the binary would be killed on a legitimate path", nr)
		}
	}
	for _, nr := range tgt.installer {
		if allowed[nr] {
			t.Errorf("syscall %d (prctl / seccomp) is allowed: both run before the filter applies, so allowing them only lets hijacked code install a filter of its own", nr)
		}
	}
}

func TestSelfHostSandboxOffEmitsNothing(t *testing.T) {
	for _, tgt := range sandboxTargets {
		t.Run(tgt.name, func(t *testing.T) {
			off := sandboxAsm(t, tgt.name, sandboxSocketProg, false)
			for _, needle := range []string{"__fern_seccomp_install", ".Lseccomp_filter"} {
				if strings.Contains(off, needle) {
					t.Errorf("sandbox-off asm contains %q", needle)
				}
			}
			on := sandboxAsm(t, tgt.name, sandboxSocketProg, true)
			start := strings.Index(on, "\n_start:\n")
			install := strings.Index(on, tgt.install)
			main := strings.Index(on, tgt.mainCall)
			if start < 0 || install < start || main < install {
				t.Errorf("sandbox-on asm must call __fern_seccomp_install in _start before main: _start at %d, installer at %d, main at %d", start, install, main)
			}
		})
	}
}

// Darwin shares the arm64 emitter and has no seccomp, so the flag is refused
// there rather than ignored.
func TestSelfHostSandboxRefusesDarwin(t *testing.T) {
	asmPath := filepath.Join(t.TempDir(), "out.s")
	if out, ok := sandboxCompile(t, "arm64-darwin", sandboxSocketProg, false, "-emit", "asm", "-o", asmPath); !ok {
		t.Fatalf("control compile without the sandbox failed:\n%s", out)
	}
	out, ok := sandboxCompile(t, "arm64-darwin", sandboxSocketProg, true, "-emit", "asm", "-o", asmPath)
	if ok {
		t.Fatal("FERN_SANDBOX=1 compiled an arm64-darwin program, which carries no filter")
	}
	if !strings.Contains(out, "FERN_SANDBOX=1 has no sandbox on arm64-darwin") {
		t.Errorf("refusal does not name the target:\n%s", out)
	}
}

func TestSelfHostSandboxRefusesRunTimeSyscallNumber(t *testing.T) {
	const src = `function main(): i32 {
  unsafe {
      let r: i64 = __syscall3(args().len() + 38, 0, 0, 0);
      if (r > 0) { return 0; }
      return 1;
  }
}
`
	for _, tgt := range sandboxTargets {
		t.Run(tgt.name, func(t *testing.T) {
			exe := filepath.Join(t.TempDir(), "prog")
			if out, ok := sandboxCompileWith(t, e2eharness.RuntimeCLI(t), tgt.name, src, false, "-o", exe); !ok {
				t.Fatalf("control compile without the sandbox failed:\n%s", out)
			}
			out, ok := sandboxCompileWith(t, e2eharness.RuntimeCLI(t), tgt.name, src, true, "-o", exe)
			if ok {
				t.Fatal("FERN_SANDBOX=1 compiled a program whose syscall number is a run-time value")
			}
			if !strings.Contains(out, "__syscall3 passes the syscall number at run time") {
				t.Errorf("refusal does not name the call:\n%s", out)
			}
		})
	}
}

func TestSelfHostSandboxRefusesPerModuleEmit(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	driverBin := buildSelfHostBin(t, x86gcc, writeSelfHostModloadProject(t), "drivers/asm_modload_run.fern", "sandboxpermoduledriver")
	proj := t.TempDir()
	mustWrite(t, proj, "leaf.fern", "pub function seven(): i32 { return 7; }\n")
	mustWrite(t, proj, "main.fern", "import \"./leaf\";\n\nfunction main(): i32 { return leaf.seven(); }\n")
	copyStdlibTree(t, proj)
	for _, tgt := range sandboxTargets {
		t.Run(tgt.name, func(t *testing.T) {
			cmd := runX86_64Bin(x86runner, driverBin, filepath.Join(proj, "main.fern"), "-target", tgt.name, "-per-module-emit", "0")
			cmd.Env = e2eharness.ChildEnv("FERN_SANDBOX=1")
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatal("FERN_SANDBOX=1 emitted a per-module unit")
			}
			if !strings.Contains(string(out), "FERN_SANDBOX=1 needs the whole-program emit") {
				t.Errorf("refusal does not say why:\n%s", out)
			}
		})
	}
}

func TestSelfHostSandboxNoBareSyscallEmit(t *testing.T) {
	files, err := filepath.Glob("../../../compiler/*.fern")
	inDrivers, _ := filepath.Glob("../../../compiler/drivers/*.fern")
	files = append(files, inDrivers...)
	if err != nil || len(files) == 0 {
		t.Fatalf("no self-host sources: %v", err)
	}
	// The Darwin form `svc #0x80` is outside the sandbox and does not match.
	bare := regexp.MustCompile(`\bsyscall\\n|\bsvc #0\\n`)
	recorder := regexp.MustCompile(`^pub function \(s: EmitState\) (raw_)?(syscall|svc)\(`)
	var offenders []string
	for _, f := range files {
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
		t.Errorf("a syscall written outside EmitState's recorders is missing from the sandbox allowlist; use s.syscall(nr) or s.svc(nr), or s.raw_syscall(callee) / s.raw_svc(callee) for a run-time number:\n  %s", strings.Join(offenders, "\n  "))
	}
}
