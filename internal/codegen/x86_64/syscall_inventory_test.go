package x86_64

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"path/filepath"
)

// --- Syscall inventory (#6071) ------------------------------------
//
// EmitWithSyscalls claims to return the EXACT set of syscalls a
// program's emitted text can issue. That claim is what the seccomp-bpf
// filter will be derived from, and an under-approximation there kills
// the process on a legitimate path — so the claim needs a gate, not a
// convention.
//
// Three tests hold it up:
//
//  1. TestNoBareSyscallEmit — nothing can emit `syscall` except the two
//     recording helpers and the raw-floor helper. This is the structural
//     half: it makes the set exact by construction rather than by
//     diligence, so a future syscall cannot be added without being
//     recorded.
//  2. TestSyscallSetMatchesEmittedAsm — the recorded set agrees with
//     what is actually in the asm text. This is the behavioural half:
//     it catches a helper called with the wrong number (the one way
//     emitSyscallPreloaded could lie).
//  3. TestRawSyscallRefusedUnderSandbox — the raw floor, whose number is
//     a run-time operand, is refused under the sandbox rather than
//     emitted past a filter that cannot name it.

// TestNoBareSyscallEmit is the essential structural gate: every
// syscall emission must go through emitSyscall or
// emitSyscallPreloaded, which record the number, or emitSyscallRaw,
// which marks the program as one the sandbox refuses. A bare
// `g.emit("syscall")` would issue a syscall the recorded set does not
// know about — invisible here, fatal once a filter is derived from it.
func TestNoBareSyscallEmit(t *testing.T) {
	src, err := os.ReadFile("x86_64.go")
	if err != nil {
		t.Fatalf("read backend source: %v", err)
	}
	lines := strings.Split(string(src), "\n")
	var offenders []string
	for i, l := range lines {
		if !strings.Contains(l, `g.emit("syscall")`) {
			continue
		}
		// The two recording helpers and the raw-floor helper are the
		// only legitimate emitters.
		if inFunc(lines, i, "func (g *generator) emitSyscall(") ||
			inFunc(lines, i, "func (g *generator) emitSyscallPreloaded(") ||
			inFunc(lines, i, "func (g *generator) emitSyscallRaw(") {
			continue
		}
		offenders = append(offenders, strings.TrimSpace(l)+" (line "+itoa(i+1)+")")
	}
	if len(offenders) > 0 {
		t.Errorf("bare syscall emission outside the recording helpers — the syscall set would silently miss these:\n  %s\n\nUse g.emitSyscall(n), g.emitSyscallPreloaded(n) when eax is loaded separately, or g.emitSyscallRaw(callee) for a run-time number.",
			strings.Join(offenders, "\n  "))
	}
}

// inFunc reports whether line i sits inside the function whose
// declaration line starts with decl (i.e. the nearest preceding
// top-level `func ` line is that one).
func inFunc(lines []string, i int, decl string) bool {
	for j := i; j >= 0; j-- {
		if strings.HasPrefix(lines[j], "func ") {
			return strings.HasPrefix(lines[j], decl)
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The raw floor's number is recorded when it is a literal — the shape every
// runtime helper written in Fern uses — so the seccomp allowlist stays
// exact. A number computed at run time can not be: a sandboxed build is
// refused, naming the callee, rather than handed a filter that kills the
// program at its first call, and without the sandbox the site records
// nothing.
func TestRawSyscallRefusedUnderSandbox(t *testing.T) {
	const literal = `function main(): i32 {
    var closed: i64 = __syscall3(3, 0 - 1, 0, 0);
    if (closed != 0 - 9) { return 1; }
    return 0;
}
`
	_, syscalls := emitAsmAndSyscalls(t, literal)
	recorded := false
	for _, n := range syscalls {
		recorded = recorded || n == 3
	}
	if !recorded {
		t.Fatal("a raw syscall with a literal number was not recorded")
	}
	const src = `function main(): i32 {
    var nr: i32 = __load_i32(__alloc(4)) + 3;
    var closed: i64 = __syscall3(nr, 0 - 1, 0, 0);
    if (closed != 0 - 9) { return 1; }
    return 0;
}
`
	asm, syscalls := emitAsmAndSyscalls(t, src)
	if !strings.Contains(asm, "\tsyscall\n") {
		t.Fatal("the raw floor emitted no syscall instruction")
	}
	for _, n := range syscalls {
		if n == 3 {
			t.Fatal("the raw site's number is a run-time operand; nothing about it can be recorded")
		}
	}
	was := ast.SandboxEnabled
	ast.SandboxEnabled = true
	defer func() { ast.SandboxEnabled = was }()
	_, _, err := emitProgram(t, src)
	if err == nil || !strings.Contains(err.Error(), "__syscall3") {
		t.Fatalf("sandboxed build of a raw-floor program: err = %v, want a refusal naming __syscall3", err)
	}
}

// emitAsmAndSyscalls compiles src through the standard pipeline and
// returns the asm plus the recorded syscall set.
func emitAsmAndSyscalls(t *testing.T, src string) (string, []int) {
	t.Helper()
	asm, syscalls, err := emitProgram(t, src)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm, syscalls
}

// emitProgram runs src through the front end and EmitWithSyscalls,
// handing back the emitter's own error.
func emitProgram(t *testing.T, src string) (string, []int, error) {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
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
	return EmitWithSyscalls(prog, info, Options{})
}

var movEaxRe = regexp.MustCompile(`(?m)^\tmov eax, (\d+)$`)

// TestSyscallSetMatchesEmittedAsm is the behavioural half: every
// `mov eax, N` that immediately precedes a `syscall` in the emitted
// text must appear in the recorded set. This is what catches an
// emitSyscallPreloaded call passing the wrong number — the structural
// test cannot see that, because the call site looks correct.
//
// The converse (recorded ⊆ emitted) is deliberately NOT asserted: the
// `xor eax, eax` sites record read(0) without a matching `mov eax, 0`
// in the text, which is correct and would fail a strict equality.
func TestSyscallSetMatchesEmittedAsm(t *testing.T) {
	asm, syscalls := emitAsmAndSyscalls(t, syscallProbeSrc)
	recorded := map[int]bool{}
	for _, n := range syscalls {
		recorded[n] = true
	}
	lines := strings.Split(asm, "\n")
	seen := 0
	for i := 0; i+1 < len(lines); i++ {
		if strings.TrimSpace(lines[i+1]) != "syscall" {
			continue
		}
		m := movEaxRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		seen++
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if !recorded[n] {
			t.Errorf("asm issues syscall %d but the recorded set does not contain it (set: %v)", n, syscalls)
		}
	}
	if seen == 0 {
		t.Fatal("found no `mov eax, N` + `syscall` pairs in the emitted asm — the scan is not testing anything")
	}
}

// TestSyscallSetIsProgramSpecific pins the property that makes the set
// worth deriving a filter from: it describes THIS program, not the
// language. A program that never touches the filesystem must not carry
// openat; one that never forks must not carry execve. If treeshake
// stopped culling, this would silently widen every future filter.
func TestSyscallSetIsProgramSpecific(t *testing.T) {
	_, minimal := emitAsmAndSyscalls(t, `function main(): i32 { return 0; }`)
	// socket (41) is a Fern body's literal now (internal/fernrt), so it is
	// no longer a constant of this package; the number is what the filter
	// sees.
	for _, banned := range []int{sysExecve, sysFork, 41, sysGetrandom} {
		for _, n := range minimal {
			if n == banned {
				t.Errorf("a do-nothing program's syscall set contains %d; the set should be program-specific, not the language's whole surface (got %v)", banned, minimal)
			}
		}
	}
	// exit_group is the one syscall every program must be able to make.
	found := false
	for _, n := range minimal {
		if n == sysExitGroup {
			found = true
		}
	}
	if !found {
		t.Errorf("every program exits, so exit_group (%d) must be in the set; got %v", sysExitGroup, minimal)
	}
}

// syscallProbeSrc reaches a deliberately wide slice of the runtime so
// the asm scan has several distinct syscalls to check, not just write.
const syscallProbeSrc = `function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    print("hi");
    var n: i32 = xs.len();
    return n - 3;
}`
