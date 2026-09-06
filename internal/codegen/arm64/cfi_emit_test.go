package arm64

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// cfiSrc is the x86-64 fixture's counterpart, and recursive for the same
// reason: straight-line helpers are inlined into main, so a fixture of them
// emits ONE function and a per-function check silently covers one prologue.
const cfiSrc = `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }
function fact(n: i32): i32 { if (n < 2) { return 1; } return n * fact(n - 1); }
function main(): i32 { return fib(9) + fact(1); }`

// TestFunctionsCarryCFI pins the unwind rules the arm64 frame prologue
// describes (#7901), pinned from `aarch64-linux-gnu-as` on the same
// instructions and read back with `readelf --debug-dump=frames`.
//
// Two rules differ from x86-64's and both matter. Every saved register needs
// one, so x30 gets its own — it is the register an unwinder returns through.
// And the epilogue restores `sp, 0`, NOT `sp, 8`: the CIE's initial rule here
// is CFA = sp+0 with no return-address rule at all, because on entry the
// return address is in x30 rather than on the stack.
func TestFunctionsCarryCFI(t *testing.T) {
	asm := compile(t, cfiSrc, Options{})

	for _, fn := range []string{"fib", "fact", "main"} {
		// fnBody t.Fatals when the function is absent, which is the check
		// that the fixture still emits three: straight-line helpers would be
		// inlined away and leave one prologue covered instead of three.
		body := fnBody(t, asm, fn)
		for _, want := range []string{
			".cfi_startproc",
			".cfi_def_cfa_offset 16",
			".cfi_offset x29, -16",
			".cfi_offset x30, -8",
			".cfi_def_cfa_register x29",
			".cfi_def_cfa sp, 0",
			".cfi_endproc",
		} {
			if strings.Count(body, want) != 1 {
				t.Errorf("%s: want exactly one %q, got %d", fn, want, strings.Count(body, want))
			}
		}
		if strings.Contains(body, ".cfi_def_cfa sp, 8") {
			t.Errorf("%s: epilogue restores sp, 8 — that is x86-64's rule; aarch64's CIE starts at sp+0", fn)
		}
		if i, j := strings.Index(body, ".cfi_offset x30"), strings.Index(body, ".cfi_def_cfa_register x29"); i > j {
			t.Errorf("%s: the saved-register rules must precede .cfi_def_cfa_register", fn)
		}
	}
	if a, b := strings.Count(asm, ".cfi_startproc"), strings.Count(asm, ".cfi_endproc"); a != b {
		t.Errorf("%d .cfi_startproc vs %d .cfi_endproc — an unbalanced pair is rejected by the assembler", a, b)
	}
}

var (
	brRe        = regexp.MustCompile(`^\tb (\S+)$`)
	directiveRe = regexp.MustCompile(`^\t\.`)
)

// branchToNextLabel finds a `b L` that reaches `L:` with no instruction in
// between. Directives are skipped when looking ahead, because when the
// peephole's dead-branch rewrite stops firing it is BECAUSE something was
// emitted between the two — a scan requiring adjacency would miss exactly the
// regression it exists for. (The x86-64 version of this shipped in that
// vacuous form for one iteration.)
func branchToNextLabel(asm string) (string, bool) {
	lines := strings.Split(asm, "\n")
	for i := 0; i < len(lines); i++ {
		m := brRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if directiveRe.MatchString(lines[j]) {
				continue
			}
			if lines[j] == m[1]+":" {
				return m[1], true
			}
			break
		}
	}
	return "", false
}

// TestPeepholeStillFiresWithCFI is the arm64 half of the hazard: emission
// funnels through `put`, the rewrites match contiguous window entries, and P2
// drops a `b L` immediately followed by `L:` — the epilogue's shape, since
// OpReturn emits `b retLabel`.
func TestPeepholeStillFiresWithCFI(t *testing.T) {
	asm := compile(t, cfiSrc, Options{})
	if !strings.Contains(asm, ".cfi_startproc") {
		t.Fatal("no CFI in the emitted asm — this test cannot say anything about their interaction")
	}
	if lbl, found := branchToNextLabel(asm); found {
		t.Errorf("`b %s` is immediately followed by `%s:` — the peephole's dead-branch rewrite stopped firing, most likely because a directive was inserted between them", lbl, lbl)
	}
}

func TestPeepholeGateDetectsADeadBranch(t *testing.T) {
	if _, found := branchToNextLabel("\tb .L1\n.L1:\n\tret\n"); !found {
		t.Error("the dead-branch scan missed a dead branch")
	}
	if _, found := branchToNextLabel("\tb .L1\n\tnop\n.L1:\n\tret\n"); found {
		t.Error("the dead-branch scan fired on a branch that is not dead")
	}
	if _, found := branchToNextLabel("\tb .L1\n\t.cfi_def_cfa sp, 0\n.L1:\n\tret\n"); !found {
		t.Error("the dead-branch scan missed a branch separated from its label by a directive — the residue of P2 no longer firing")
	}
}

// TestEmittedCFIDecodesAsUnwindData is the end-to-end check: aarch64 gas must
// accept the directives and a DWARF consumer must find one FDE per function.
func TestEmittedCFIDecodesAsUnwindData(t *testing.T) {
	gcc := findArm64Gcc(t)
	readelf, err := exec.LookPath("readelf")
	if err != nil {
		t.Skip("readelf not on PATH")
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "p.s")
	if err := os.WriteFile(asmPath, []byte(compile(t, cfiSrc, Options{})), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "p")
	if out, err := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); err != nil {
		t.Fatalf("aarch64 gas rejected the emitted CFI: %v\n%s", err, out)
	}
	out, err := exec.Command(readelf, "--debug-dump=frames", binPath).Output()
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out), " FDE "); n != 3 {
		t.Errorf("got %d FDEs, want 3 (one per user function)\n%s", n, out)
	}
	for _, want := range []string{
		"Return address column: 30",
		"DW_CFA_def_cfa: r31 (sp) ofs 0",
		"DW_CFA_offset: r29 (x29) at cfa-16",
		"DW_CFA_offset: r30 (x30) at cfa-8",
		"DW_CFA_def_cfa_register: r29 (x29)",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("decoded .eh_frame is missing %q", want)
		}
	}
}

// TestDarwinEmitsCFI: the Mach-O path carries the same directives as the
// ELF one, and the platform assembler accepts the emitted asm. The second
// half is the gate #8065 needed: an ELF-style `.L` local label is a real
// symbol on Mach-O, which atomizes the section and makes the distance a
// CFI advance measures non-constant — llvm-mc then rejects the epilogue rule
// with "invalid CFI advance_loc expression".
func TestDarwinEmitsCFI(t *testing.T) {
	asm := compile(t, cfiSrc, Options{Darwin: true})
	for _, d := range []string{".cfi_startproc", ".cfi_def_cfa_offset 16", ".cfi_offset x29, -16", ".cfi_offset x30, -8", ".cfi_def_cfa_register x29", ".cfi_def_cfa sp, 0", ".cfi_endproc"} {
		if strings.Count(asm, d) != 3 {
			t.Errorf("Darwin asm has %d of %q, want one per user function (3)", strings.Count(asm, d), d)
		}
	}

	mc, err := exec.LookPath("llvm-mc")
	if err != nil {
		t.Skip("llvm-mc not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "d.s")
	if err := os.WriteFile(p, []byte(asm), 0o644); err != nil {
		t.Fatal(err)
	}
	obj := filepath.Join(dir, "d.o")
	out, err := exec.Command(mc, "-triple=arm64-apple-darwin", "-filetype=obj", "-o", obj, p).CombinedOutput()
	if err != nil {
		t.Fatalf("the Darwin assembler rejects the emitted asm: %v\n%s", err, out)
	}
	dump, err := exec.LookPath("llvm-dwarfdump")
	if err != nil {
		t.Skip("llvm-dwarfdump not on PATH")
	}
	out, err = exec.Command(dump, "--eh-frame", obj).Output()
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out), " FDE "); n != 3 {
		t.Errorf("llvm-mc decoded %d FDEs from the emitted CFI, want 3\n%s", n, out)
	}
}

// findArm64Gcc locates a gcc that targets aarch64, verified by assembling a
// probe rather than by name: the cross-compiler is `aarch64-linux-gnu-gcc`
// on an x86-64 host but plain `gcc` on an aarch64 one, so a name-only lookup
// either misses the native tool or picks up the wrong architecture's. The
// x86-64 sibling of this test learned that the hard way on an aarch64
// runner.
func findArm64Gcc(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"aarch64-linux-gnu-gcc", "gcc", "clang"} {
		bin, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		dir := t.TempDir()
		probe := filepath.Join(dir, "probe.s")
		if err := os.WriteFile(probe, []byte(".text\n.globl _start\n_start:\n\tstp x29, x30, [sp, #-16]!\n\tret\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if exec.Command(bin, "-c", "-o", filepath.Join(dir, "probe.o"), probe).Run() == nil {
			return bin
		}
	}
	t.Skip("no gcc/clang on PATH that assembles aarch64")
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

// TestRuntimeUsesFusedForms pins the arm64 runtime shapes that use the
// instructions the assembler gained (#7887): madd for the element-address
// and allocation-size arithmetic, tbnz/tbz/cbnz for the low-bit tests in the
// trig and pow kernels, csel/cset/cneg for the clamps and flags. A
// regression to the two-instruction forms would still be correct, which is
// why nothing else notices it.
func TestRuntimeUsesFusedForms(t *testing.T) {
	asm := compile(t, adoptSrc, Options{})
	for _, want := range []string{
		"madd x0, x22, x20, x19", // &elem[i] in the array walks
		"madd x5, x4, x1, x3",    // __fern_arr_box: size = cap*stride + headerBytes
		"madd x0, x23, x21, x24", // arr_push allocSize, 64-bit since #8587
	} {
		if !strings.Contains(asm, want) {
			t.Errorf("runtime asm no longer contains %q", want)
		}
	}
	if strings.Contains(asm, "\tmul x0, x22, x20\n\tadd x0, x19, x0") {
		t.Error("runtime asm still computes &elem[i] as mul + add")
	}
}
