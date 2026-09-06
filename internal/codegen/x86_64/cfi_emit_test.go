package x86_64

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// cfiSrc has three functions that survive to the emitter — recursion keeps
// them from being folded into their callers, which a first version of this
// test learned the hard way: with two straight-line helpers the program
// emitted ONE function and the test would have passed while covering a single
// prologue.
const cfiSrc = `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }
function fact(n: i32): i32 { if (n < 2) { return 1; } return n * fact(n - 1); }
function main(): i32 { return fib(9) + fact(1); }`

// TestFunctionsCarryCFI pins the unwind rules the frame prologue describes
// (#7901). The sequence is gas's own for the same three instructions, checked
// against `as --64` and read back with `readelf --debug-dump=frames`: the CFA
// is at rsp+8 on entry (the CIE's initial rule), rsp+16 once rbp is pushed,
// then tracked through rbp until the epilogue hands it back to rsp.
func TestFunctionsCarryCFI(t *testing.T) {
	asm := compile(t, cfiSrc)

	for _, fn := range []string{"fib", "fact", "main"} {
		body, ok := fnBodyOf(asm, fn)
		if !ok {
			t.Fatalf("no emitted body for %q — the fixture no longer produces three functions, so this test would cover fewer prologues than it claims", fn)
		}
		for _, want := range []string{
			".cfi_startproc",
			".cfi_def_cfa_offset 16",
			".cfi_offset rbp, -16",
			".cfi_def_cfa_register rbp",
			".cfi_def_cfa rsp, 8",
			".cfi_endproc",
		} {
			if strings.Count(body, want) != 1 {
				t.Errorf("%s: want exactly one %q, got %d", fn, want, strings.Count(body, want))
			}
		}
		// Order is the whole content of an FDE: the rules are deltas from the
		// previous one, so a swapped pair unwinds at the wrong instruction
		// while staying well-formed.
		if i, j := strings.Index(body, ".cfi_offset rbp"), strings.Index(body, ".cfi_def_cfa_register rbp"); i > j {
			t.Errorf("%s: .cfi_offset must precede .cfi_def_cfa_register", fn)
		}
		if i, j := strings.Index(body, ".cfi_def_cfa_register rbp"), strings.Index(body, ".cfi_def_cfa rsp, 8"); i > j {
			t.Errorf("%s: the epilogue's .cfi_def_cfa must follow the prologue's .cfi_def_cfa_register", fn)
		}
	}

	if a, b := strings.Count(asm, ".cfi_startproc"), strings.Count(asm, ".cfi_endproc"); a != b {
		t.Errorf("%d .cfi_startproc vs %d .cfi_endproc — an unbalanced pair is rejected by the assembler", a, b)
	}
}

// jmpToNextLabel finds a `jmp L` that reaches `L:` without any instruction in
// between — a jump the peephole's P2 rewrite is supposed to have deleted.
//
// Directives are skipped when looking ahead, and that is the whole point: when
// P2 stops firing it is BECAUSE something was emitted between the jump and its
// label, so a scan that required them on adjacent lines would miss exactly the
// regression it exists to catch. The first version of this did require
// adjacency and was vacuous — a directive planted at the hazard point left the
// dead jump in place and the check still passed.
var (
	jmpRe       = regexp.MustCompile(`^\tjmp (\S+)$`)
	directiveRe = regexp.MustCompile(`^\t\.`)
)

func jmpToNextLabel(asm string) (string, bool) {
	lines := strings.Split(asm, "\n")
	for i := 0; i < len(lines); i++ {
		m := jmpRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if directiveRe.MatchString(lines[j]) {
				continue // emits no bytes, so the jump is still a fall-through
			}
			if lines[j] == m[1]+":" {
				return m[1], true
			}
			break
		}
	}
	return "", false
}

// TestPeepholeStillFiresWithCFI is the gate the prologue comment refers to.
//
// Every emission helper funnels through `put`, so a `.cfi_*` line is an
// ordinary entry in the peephole window, and the rewrites match CONTIGUOUS
// entries. P2 deletes a `jmp L` immediately followed by `L:` — which is
// exactly the epilogue shape, since OpReturn emits `jmp retLabel` and the last
// one lands right before the label. Put a directive between them and P2 stops
// firing: a dead jump in a large fraction of functions, correct but bigger and
// slower, and nothing else would fail.
func TestPeepholeStillFiresWithCFI(t *testing.T) {
	asm := compile(t, cfiSrc)
	if !strings.Contains(asm, ".cfi_startproc") {
		t.Fatal("no CFI in the emitted asm — this test cannot say anything about their interaction")
	}
	if lbl, found := jmpToNextLabel(asm); found {
		t.Errorf("`jmp %s` is immediately followed by `%s:` — the peephole's dead-jump rewrite stopped firing, most likely because a directive was inserted between them", lbl, lbl)
	}
}

// TestPeepholeGateDetectsADeadJump keeps the check above honest: it must fail
// on input that actually contains the pattern, or it proves nothing.
func TestPeepholeGateDetectsADeadJump(t *testing.T) {
	if _, found := jmpToNextLabel("\tjmp .L1\n.L1:\n\tret\n"); !found {
		t.Error("the dead-jump scan missed a dead jump")
	}
	if _, found := jmpToNextLabel("\tjmp .L1\n\tnop\n.L1:\n\tret\n"); found {
		t.Error("the dead-jump scan fired on a jump that is not dead")
	}
	// The shape a lost P2 actually leaves: a directive between the two.
	if _, found := jmpToNextLabel("\tjmp .L1\n\t.cfi_def_cfa_offset 16\n.L1:\n\tret\n"); !found {
		t.Error("the dead-jump scan missed a jump separated from its label by a directive — the exact residue of P2 no longer firing")
	}
}

// TestEmittedCFIDecodesAsUnwindData is the end-to-end check: gas has to accept
// the directives, and a real DWARF consumer has to find one FDE per function
// in the linked binary. Everything above is text; this is the only assertion
// that the bytes mean anything.
func TestEmittedCFIDecodesAsUnwindData(t *testing.T) {
	gcc := findX86Gcc(t)
	readelf, err := exec.LookPath("readelf")
	if err != nil {
		t.Skip("readelf not on PATH")
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "p.s")
	if err := os.WriteFile(asmPath, []byte(compile(t, cfiSrc)), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "p")
	if out, err := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); err != nil {
		t.Fatalf("gas rejected the emitted CFI: %v\n%s", err, out)
	}
	out, err := exec.Command(readelf, "--debug-dump=frames", binPath).Output()
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out), " FDE "); n != 3 {
		t.Errorf("got %d FDEs, want 3 (one per user function)\n%s", n, out)
	}
	for _, want := range []string{
		"DW_CFA_def_cfa_offset: 16",
		"DW_CFA_offset: r6 (rbp) at cfa-16",
		"DW_CFA_def_cfa_register: r6 (rbp)",
		"DW_CFA_def_cfa: r7 (rsp) ofs 8",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("decoded .eh_frame is missing %q", want)
		}
	}
}

// findX86Gcc locates a gcc that actually targets x86-64, verified by
// assembling an Intel-syntax probe rather than by trusting the name.
//
// `gcc` on an aarch64 runner is the NATIVE aarch64 gcc, which happily
// accepts the file and then reports "unknown mnemonic `push`" several
// hundred times. This test did exactly that on test-units-aarch64 — the
// only failure in 6460 — because it looked the tool up by name and I had
// only ever run it on amd64. internal/native/x86_64's gas differential has
// probed for this since it landed; this is the same guard.
func findX86Gcc(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"x86_64-linux-gnu-gcc", "gcc", "clang"} {
		bin, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		dir := t.TempDir()
		probe := filepath.Join(dir, "probe.s")
		if err := os.WriteFile(probe, []byte(".intel_syntax noprefix\n.text\n.globl _start\n_start:\n\tpush rbp\n\tret\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if exec.Command(bin, "-c", "-o", filepath.Join(dir, "probe.o"), probe).Run() == nil {
			return bin
		}
	}
	t.Skip("no gcc/clang on PATH that assembles x86-64 Intel syntax")
	return ""
}

// adoptSrc reaches the runtime helpers the adoption checks look at: array
// append and drop (string elements, so the element walk is emitted), an
// aliased element store (the copy-on-write clone), string ordering and
// float→u64.
const adoptSrc = `function main(): i32 {
  var xs: string[] = [];
  xs = xs.append("a" + "b");
  xs = xs.append("c");
  var ys: string[] = xs;
  ys = ys.append("d");
  ys = ys.with(0, "z");
  var lt: boolean = xs[0] < ys[1];
  var f: f64 = 3.5;
  var u: u64 = f as u64;
  if (lt && u == 3u64) { return xs.len(); }
  return 1;
}`

// TestRuntimeUsesConditionalMoves pins the branch-free shapes the runtime
// helpers took once the assembler carried cmov/setcc/btc (#7887): the
// header-size clamp in the array free paths, the min/max in push and the
// clone, the string-order length clamp, the stat flags and the unsigned
// float saturation. A regression to the branchy form would still be correct,
// which is why nothing else notices it.
func TestRuntimeUsesConditionalMoves(t *testing.T) {
	asm := compile(t, adoptSrc)
	for _, want := range []string{
		"cmova r8, rsi",    // __fern_arr_dec: headerBytes = max(16, stride)
		"cmovg ecx, r13d",  // arr_push: headerBytes
		"cmovl r15, rcx",   // arr_push: newCap = max(2n, 4), 64-bit since #8587
		"cmovg r15d, r12d", // copy-on-write clone
		"cmovb r8d, edx",   // __fern_str_order: min(la, lb)
		"btc rax, 63",      // f64 → u64 saturation
		"sete al",          // __fern_rc_is_unique
	} {
		if !strings.Contains(asm, want) {
			t.Errorf("runtime asm no longer contains %q", want)
		}
	}
	for _, gone := range []string{".Larrdec_hdr", ".Lpush_cap_ok", ".Lpush_hdr_set", ".Lcow_hdr_set", ".Lsord_n"} {
		if strings.Contains(asm, gone) {
			t.Errorf("runtime asm still carries the branch label %q", gone)
		}
	}
}
