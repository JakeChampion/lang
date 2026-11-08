package e2ecompiler

import (
	"encoding/binary"
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
    let fd: Socket = tcp_listen(0);
    if (fd.fd < 0) { return 1; }
    let port: i32 = tcp_local_port(fd);
    tcp_close(fd);
    if (port <= 0) { return 2; }
    return 0;
}
`

// sandboxRuleProg reaches every call the filter checks an argument of: socket
// and setsockopt through tcp_listen, ioctl through isatty, prctl through
// disable_core_dumps, fcntl through a pipe's non-blocking flag, and clone
// through proc_fork on arm64. It is compiled, never run.
const sandboxRuleProg = `function main(): i32 {
    let fd: Socket = tcp_listen(0);
    if (fd.fd < 0) { return 1; }
    tcp_close(fd);
    if (isatty(0)) { return 2; }
    if (!disable_core_dumps()) { return 3; }
    let Ok(q) = pipe() else { return 4; };
    let Ok(_) = q.w.set_nonblocking(true) else { return 5; };
    q.r.close();
    q.w.close();
    let pid: i32 = proc_fork();
    if (pid == 0) { exit(0); }
    match (read_file("/proc/self/stat")) { Ok(_) => {}, Err(_) => { return 6; } }
    return 0;
}
`

// sandboxRule is one argument rule of the filter, with an argument that
// passes and arguments that the filter must kill.
type sandboxRule struct {
	nr   uint32
	arg  int
	good uint64
	bad  []uint64
}

// sandboxTarget is one target the sandbox exists on, with the constants its
// filter and installer carry.
type sandboxTarget struct {
	name      string
	arch      uint32        // the AUDIT_ARCH_* word the filter checks first
	installer []uint32      // seccomp: issued before the filter applies, so never allowed
	program   []uint32      // calls sandboxRuleProg makes on this target that carry no rule
	denied    []uint32      // calls no runtime path makes: execve and ptrace
	rules     []sandboxRule // the argument rules of asmcore.arg_rules for this target
	install   string        // the call of the installer in _start
	mainCall  string        // the call of main, which the installer must precede
	lenInsn   string        // the installer's load of the filter length, before the count
}

// The ioctl requests the runtime passes: TCGETS, TCSETS, TCSETSW, TCSETSF,
// TIOCOUTQ, TIOCGWINSZ, TIOCSWINSZ and FICLONE. TIOCSTI is the denied probe.
var sandboxIoctl = sandboxRule{arg: 1, good: 21505, bad: []uint64{21522, 0x5401 << 32}}

var sandboxTargets = []sandboxTarget{
	{
		name:      e2eharness.TargetX86_64Linux,
		arch:      0xC000003E,
		installer: []uint32{317},
		program:   []uint32{3, 49, 50, 57, 231},
		denied:    []uint32{59, 101},
		rules: []sandboxRule{
			{nr: 9, arg: 2, good: 3, bad: []uint64{7, 4}},
			{nr: 10, arg: 2, good: 3, bad: []uint64{7, 4}},
			{nr: 329, arg: 2, good: 3, bad: []uint64{7, 4}},
			{nr: 16, arg: sandboxIoctl.arg, good: sandboxIoctl.good, bad: sandboxIoctl.bad},
			{nr: 41, arg: 0, good: 2, bad: []uint64{16, 17, 1<<32 | 2}},
			{nr: 54, arg: 1, good: 1, bad: []uint64{270, 0}},
			{nr: 54, arg: 2, good: 2, bad: []uint64{25, 0, 1<<32 | 2}},
			{nr: 72, arg: 1, good: 3, bad: []uint64{6, 8, 1030, 1<<32 | 3}},
			{nr: 157, arg: 0, good: 4, bad: []uint64{35, 22, 38, 0x59616d61}},
			{nr: 257, arg: 2, good: 577, bad: []uint64{0x200000, 0x400000 | 0x10000, 0x2000, 0x8000}},
		},
		install:  "    call __fern_seccomp_install\n",
		mainCall: "    call __fn_main\n",
		lenInsn:  "    movq $",
	},
	{
		name:      e2eharness.TargetArm64Linux,
		arch:      0xC00000B7,
		installer: []uint32{277},
		program:   []uint32{57, 200, 201, 94},
		denied:    []uint32{221, 117},
		rules: []sandboxRule{
			{nr: 222, arg: 2, good: 3, bad: []uint64{7, 4}},
			{nr: 226, arg: 2, good: 3, bad: []uint64{7, 4}},
			{nr: 288, arg: 2, good: 3, bad: []uint64{7, 4}},
			{nr: 29, arg: sandboxIoctl.arg, good: sandboxIoctl.good, bad: sandboxIoctl.bad},
			{nr: 198, arg: 0, good: 2, bad: []uint64{16, 17, 1<<32 | 2}},
			{nr: 208, arg: 1, good: 1, bad: []uint64{270, 0}},
			{nr: 208, arg: 2, good: 2, bad: []uint64{25, 0, 1<<32 | 2}},
			{nr: 25, arg: 1, good: 3, bad: []uint64{6, 8, 1030, 1<<32 | 3}},
			{nr: 167, arg: 0, good: 4, bad: []uint64{35, 22, 38, 0x59616d61}},
			{nr: 220, arg: 0, good: 17, bad: []uint64{0x10000000 | 17, 0x20000 | 17, 17 | 1<<32}},
			{nr: 56, arg: 2, good: 577, bad: []uint64{0x200000, 0x400000 | 0x4000, 0x2000, 0x20000}},
		},
		install:  "    bl __fern_seccomp_install\n",
		mainCall: "    bl __fn_main\n",
		lenInsn:  "    mov x0, #",
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

const (
	sandboxKill  = 0x80000000
	sandboxAllow = 0x7FFF0000
)

// runSandboxFilter runs the classic BPF program over one seccomp_data record,
// as the kernel does, and returns the action word.
func runSandboxFilter(t *testing.T, f []sandboxBPF, arch, nr uint32, args [6]uint64) uint32 {
	t.Helper()
	var data [64]byte
	binary.LittleEndian.PutUint32(data[0:], nr)
	binary.LittleEndian.PutUint32(data[4:], arch)
	for i, a := range args {
		binary.LittleEndian.PutUint64(data[16+8*i:], a)
	}
	const ldWAbs, jeqK, jsetK, retK = 0x20, 0x15, 0x45, 0x06
	var acc uint32
	for pc, steps := 0, 0; ; steps++ {
		if pc < 0 || pc >= len(f) || steps > len(f) {
			t.Fatalf("filter ran off its end at pc %d after %d steps", pc, steps)
		}
		in := f[pc]
		switch in.code {
		case ldWAbs:
			if in.k+4 > uint32(len(data)) {
				t.Fatalf("insn %d loads past seccomp_data: %+v", pc, in)
			}
			acc = binary.LittleEndian.Uint32(data[in.k:])
			pc++
		case jeqK:
			if acc == in.k {
				pc += 1 + in.jt
			} else {
				pc += 1 + in.jf
			}
		case jsetK:
			if acc&in.k != 0 {
				pc += 1 + in.jt
			} else {
				pc += 1 + in.jf
			}
		case retK:
			return in.k
		default:
			t.Fatalf("insn %d has code %#x, which the filter does not use", pc, in.code)
		}
	}
}

func TestSelfHostSandboxFilterShape(t *testing.T) {
	for _, tgt := range sandboxTargets {
		t.Run(tgt.name, func(t *testing.T) {
			checkSandboxFilterShape(t, tgt)
		})
	}
}

func checkSandboxFilterShape(t *testing.T, tgt sandboxTarget) {
	asm := sandboxAsm(t, tgt.name, sandboxRuleProg, true)
	f := parseSandboxFilter(t, asm)
	if len(f) < 7 {
		t.Fatalf("filter has %d instructions, too few to be well-formed: %+v", len(f), f)
	}
	const ldWAbs, jeqK, retK = 0x20, 0x15, 0x06
	if f[0] != (sandboxBPF{code: ldWAbs, k: 4}) {
		t.Errorf("insn 0 = %+v, want a load of seccomp_data.arch", f[0])
	}
	if f[1] != (sandboxBPF{code: jeqK, jt: 1, k: tgt.arch}) {
		t.Errorf("insn 1 = %+v, want JEQ on the audit arch %#x skipping the kill", f[1], tgt.arch)
	}
	if f[2] != (sandboxBPF{code: retK, k: sandboxKill}) {
		t.Errorf("insn 2 = %+v, want RET KILL_PROCESS on another arch", f[2])
	}
	if f[3] != (sandboxBPF{code: ldWAbs, k: 0}) {
		t.Errorf("insn 3 = %+v, want a load of seccomp_data.nr", f[3])
	}
	if f[len(f)-1] != (sandboxBPF{code: retK, k: sandboxAllow}) {
		t.Errorf("final insn = %+v, want RET ALLOW", f[len(f)-1])
	}
	// The installer hands the kernel the count of the instructions emitted.
	install := strings.Index(asm, "__fern_seccomp_install:\n")
	if install < 0 {
		t.Fatal("no __fern_seccomp_install in the emitted asm")
	}
	if !strings.Contains(asm[install:], tgt.lenInsn+strconv.Itoa(len(f))+"\n") && !strings.Contains(asm[install:], tgt.lenInsn+strconv.Itoa(len(f))+",") {
		t.Errorf("the installer does not load the filter length %d", len(f))
	}
	// The list of allowed numbers is ascending and unique, and it ends at the
	// first RET KILL: the fall-through denies.
	allowed := map[uint32]bool{}
	killAt := -1
	for i := 4; i < len(f)-1; i++ {
		if f[i].code == retK && f[i].k == sandboxKill {
			killAt = i
			break
		}
		if f[i].code != jeqK || f[i].jf != 0 {
			t.Fatalf("insn %d = %+v, want a JEQ falling through to the next comparison", i, f[i])
		}
		if i > 4 && f[i].k <= f[i-1].k {
			t.Errorf("insn %d allows %d after %d: the list is not ascending and unique", i, f[i].k, f[i-1].k)
		}
		allowed[f[i].k] = true
	}
	if killAt < 0 {
		t.Fatalf("no RET KILL_PROCESS after the list: the fall-through must deny: %+v", f)
	}
	run := func(nr uint32, args [6]uint64) uint32 {
		return runSandboxFilter(t, f, tgt.arch, nr, args)
	}
	if got := run(tgt.program[0], [6]uint64{}); got != sandboxAllow {
		t.Errorf("call %d from another architecture word: filter on this arch gave %#x", tgt.program[0], got)
	}
	if got := runSandboxFilter(t, f, tgt.arch^1, tgt.program[0], [6]uint64{}); got != sandboxKill {
		t.Errorf("call %d from another architecture = %#x, want KILL", tgt.program[0], got)
	}
	for _, nr := range tgt.program {
		if got := run(nr, [6]uint64{}); got != sandboxAllow {
			t.Errorf("syscall %d is issued by the program but the filter gives %#x: the binary would be killed on a legitimate path", nr, got)
		}
	}
	for _, nr := range append(append([]uint32{}, tgt.installer...), tgt.denied...) {
		if allowed[nr] {
			t.Errorf("syscall %d is on the list: the installer's seccomp call runs before the filter applies, and no runtime path makes the others", nr)
		}
		if got := run(nr, [6]uint64{}); got != sandboxKill {
			t.Errorf("syscall %d = %#x, want KILL", nr, got)
		}
	}
	for _, r := range tgt.rules {
		if !allowed[r.nr] {
			// pkey_mprotect is the one call with a rule that the program does
			// not reach. Off the list, it is killed whatever its arguments.
			if got := run(r.nr, sandboxGoodArgs(tgt.rules, r.nr)); got != sandboxKill {
				t.Errorf("syscall %d is off the list but the filter gives %#x, want KILL", r.nr, got)
			}
			continue
		}
		args := sandboxGoodArgs(tgt.rules, r.nr)
		if got := run(r.nr, args); got != sandboxAllow {
			t.Errorf("syscall %d with argument %d = %#x: filter gave %#x, want ALLOW", r.nr, r.arg, r.good, got)
		}
		for _, bad := range r.bad {
			args[r.arg] = bad
			if got := run(r.nr, args); got != sandboxKill {
				t.Errorf("syscall %d with argument %d = %#x: filter gave %#x, want KILL", r.nr, r.arg, bad, got)
			}
		}
	}
}

// sandboxGoodArgs is the argument vector that satisfies every rule of `nr`,
// so a bad value in one argument is the only reason a run is killed.
func sandboxGoodArgs(rules []sandboxRule, nr uint32) [6]uint64 {
	var args [6]uint64
	for _, r := range rules {
		if r.nr == nr {
			args[r.arg] = r.good
		}
	}
	return args
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
