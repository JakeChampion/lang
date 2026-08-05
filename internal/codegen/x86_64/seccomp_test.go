package x86_64

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// --- Seccomp sandbox (#6071) --------------------------------------
//
// ast.SandboxEnabled (FERN_SANDBOX=1) installs a seccomp-bpf filter at
// `_start` permitting exactly the syscalls the emitted binary can
// issue. The allowlist comes from the backend's recorded set (see
// syscall_inventory_test.go), so the risk is not that it is too loose
// — it is that it is too TIGHT, which is a crash rather than a
// warning.
//
// These tests decode the emitted BPF program and assert its shape.
// That the kernel enforces a correct filter is the kernel's business;
// the e2e sibling proves the filter actually loads at runtime
// (Seccomp: 2 in /proc/<pid>/status).

// bpfInsn is one decoded sock_filter.
type bpfInsn struct {
	code   int
	jt, jf int
	k      uint32
}

// parseSeccompFilter decodes the `.Lseccomp_filter` .rodata block back
// into instructions. Reading the emitted directives rather than
// re-deriving them from the generator is deliberate: it is the bytes
// the kernel will see that matter.
func parseSeccompFilter(t *testing.T, asm string) []bpfInsn {
	t.Helper()
	lines := strings.Split(asm, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), ".Lseccomp_filter:") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatal("no .Lseccomp_filter block in the emitted asm")
	}
	var out []bpfInsn
	var cur bpfInsn
	field := 0
	for _, l := range lines[start:] {
		s := strings.TrimSpace(l)
		if strings.HasPrefix(s, ".text") {
			break
		}
		val := func(prefix string) (int, bool) {
			if !strings.HasPrefix(s, prefix) {
				return 0, false
			}
			rest := strings.TrimSpace(strings.TrimPrefix(s, prefix))
			if i := strings.Index(rest, "//"); i >= 0 {
				rest = strings.TrimSpace(rest[:i])
			}
			n, err := strconv.ParseUint(rest, 10, 64)
			if err != nil {
				t.Fatalf("bad filter operand %q: %v", s, err)
			}
			return int(n), true
		}
		if n, ok := val(".2byte"); ok && field == 0 {
			cur = bpfInsn{code: n}
			field = 1
			continue
		}
		if n, ok := val(".byte"); ok {
			if field == 1 {
				cur.jt = n
				field = 2
			} else if field == 2 {
				cur.jf = n
				field = 3
			}
			continue
		}
		if n, ok := val(".4byte"); ok && field == 3 {
			cur.k = uint32(n)
			out = append(out, cur)
			field = 0
			continue
		}
	}
	return out
}

// emitSandboxed emits asm for src with the sandbox on, returning the
// asm and the recorded syscall set.
func emitSandboxed(t *testing.T, src string, sandbox bool) (string, []int) {
	t.Helper()
	prev := ast.SandboxEnabled
	t.Cleanup(func() { ast.SandboxEnabled = prev })
	ast.SandboxEnabled = sandbox
	asm, syscalls := emitAsmAndSyscalls(t, src)
	ast.SandboxEnabled = prev
	return asm, syscalls
}

// TestSeccompFilterShape decodes the emitted BPF and pins every part
// of it that a mistake would silently break: the arch guard, the
// deny-by-default fall-through, and — the one most likely to be wrong
// — that each allow-comparison's jump offset actually lands on the
// ALLOW instruction rather than somewhere in the middle of the
// comparison chain.
func TestSeccompFilterShape(t *testing.T) {
	asm, _ := emitSandboxed(t, syscallProbeSrc, true)
	f := parseSeccompFilter(t, asm)
	if len(f) < 6 {
		t.Fatalf("filter has %d instructions, too few to be well-formed: %+v", len(f), f)
	}
	if f[0].code != bpfLdWAbs || f[0].k != seccompDataArch {
		t.Errorf("insn 0 = %+v, want a load of seccomp_data.arch — without the arch guard a 32-bit caller could reach a different syscall under the same number", f[0])
	}
	if f[1].code != bpfJeqK || f[1].k != auditArchX8664 {
		t.Errorf("insn 1 = %+v, want JEQ AUDIT_ARCH_X86_64", f[1])
	}
	if f[2].code != bpfRetK || f[2].k != seccompRetKillProcess {
		t.Errorf("insn 2 = %+v, want RET KILL_PROCESS on arch mismatch", f[2])
	}
	if f[3].code != bpfLdWAbs || f[3].k != seccompDataNr {
		t.Errorf("insn 3 = %+v, want a load of seccomp_data.nr", f[3])
	}
	last := len(f) - 1
	if f[last].code != bpfRetK || f[last].k != seccompRetAllow {
		t.Errorf("final insn = %+v, want RET ALLOW", f[last])
	}
	if f[last-1].code != bpfRetK || f[last-1].k != seccompRetKillProcess {
		t.Errorf("penultimate insn = %+v, want RET KILL_PROCESS — the fall-through must DENY, or the filter permits everything it does not mention", f[last-1])
	}
	// Every comparison must jump exactly to ALLOW when it matches.
	for i := 4; i < last-1; i++ {
		if f[i].code != bpfJeqK {
			t.Fatalf("insn %d = %+v, want a JEQ comparison", i, f[i])
		}
		if f[i].jf != 0 {
			t.Errorf("insn %d has jf=%d, want 0 (fall through to the next comparison)", i, f[i].jf)
		}
		if target := i + 1 + f[i].jt; target != last {
			t.Errorf("insn %d (allow %d) jumps to %d, want %d (the ALLOW) — an off-by-one here lands mid-chain and permits the wrong syscall", i, f[i].k, target, last)
		}
	}
}

// TestSeccompAllowlistMatchesRecordedSet pins the property the whole
// design rests on: the filter permits exactly what the backend
// recorded, no more and no less.
//
// The two deliberate exclusions are prctl and seccomp themselves. Both
// run before the filter takes effect, so excluding them costs nothing
// and denies hijacked control flow the ability to install its own
// filter. If that ever changes silently, this fails.
func TestSeccompAllowlistMatchesRecordedSet(t *testing.T) {
	asm, recorded := emitSandboxed(t, syscallProbeSrc, true)
	f := parseSeccompFilter(t, asm)

	inFilter := map[uint32]bool{}
	for i := 4; i < len(f)-2; i++ {
		inFilter[f[i].k] = true
	}
	want := map[uint32]bool{}
	for _, n := range recorded {
		if n == sysPrctl || n == sysSeccomp {
			continue
		}
		want[uint32(n)] = true
	}
	for n := range want {
		if !inFilter[n] {
			t.Errorf("syscall %d is emitted by the program but NOT permitted — the binary would be killed on a legitimate path", n)
		}
	}
	for n := range inFilter {
		if !want[n] {
			t.Errorf("syscall %d is permitted but not in the recorded set — the filter is looser than the program needs", n)
		}
	}
	if inFilter[uint32(sysPrctl)] || inFilter[uint32(sysSeccomp)] {
		t.Error("prctl/seccomp must not be permitted: both run before the filter takes effect, so allowing them only helps an attacker install their own filter")
	}
	if len(inFilter) == 0 {
		t.Fatal("filter permits nothing at all — every program at least exits")
	}
}

// TestSandboxOffEmitsNothing is the byte-identical-when-off proxy.
func TestSandboxOffEmitsNothing(t *testing.T) {
	off, _ := emitSandboxed(t, syscallProbeSrc, false)
	for _, needle := range []string{"__fern_seccomp_install", ".Lseccomp_filter"} {
		if strings.Contains(off, needle) {
			t.Errorf("flag-off asm contains %q; the sandbox must leave nothing behind", needle)
		}
	}
	on, _ := emitSandboxed(t, syscallProbeSrc, true)
	for _, needle := range []string{"__fern_seccomp_install", ".Lseccomp_filter", "call __fern_seccomp_install"} {
		if !strings.Contains(on, needle) {
			t.Errorf("flag-on asm is missing %q", needle)
		}
	}
}
