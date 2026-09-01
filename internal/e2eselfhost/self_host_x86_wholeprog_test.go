package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostX86WholeProgramMatchesGNUAs closes what docs/TEST-GATES.md
// listed under "what nothing gates": every other e2eselfhost program test
// hands `-emit asm` text to gcc, so examples/self_host/x86_native.fern never
// saw a real program. The testl/testq sign-flag bug (#6544) is the worked
// example — invisible on the hand-written snippets that did exist, wrong on
// a program. This is the x86 twin of
// TestSelfHostArm64WholeProgramMatchesNative (#7898).
//
// # Why GNU as, and not internal/native/x86_64
//
// The arm64 twin compares the two assemblers directly because both read the
// same GAS AArch64 syntax. On x86 they do not: the emitter and
// x86_native.fern speak AT&T while internal/native/x86_64 is Intel-dialect,
// so there is no text both can read. GNU as reads what the emitter actually
// writes and is the ground truth the rest of the assembler suite is pinned
// against, so it is the stronger oracle here rather than a fallback.
//
// # Why the comparison is per-instruction and not byte-for-byte
//
// A single instruction the two encode to different lengths shifts every later
// address, so byte equality reports one real fact as thousands of
// shifted-window differences and can never point at the instruction that
// actually diverged.
//
// So the streams are compared as DECODED INSTRUCTIONS, which is what the
// gate is for: a dropped, added, or substituted instruction, or a wrong
// register or immediate, fails — and that is the #6544 class. Encoding
// LENGTH is reported separately: the byte totals are logged, and when they
// differ every instruction that encodes to a different size is named, which
// is the list #7949's remaining gap has to be worked from.
func TestSelfHostX86WholeProgramMatchesGNUAs(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	as, objcopy, objdump := findGNUToolsX86(t)

	// Import-free on purpose: asm_ir_run has no module loader. The emitter
	// DCEs the runtime down to what the program touches, so this is
	// deliberately written to touch a spread of it — refcounted arrays,
	// nested arrays, string concat and slicing, f64 arithmetic and
	// comparison, i64 division and remainder, a closure through a function
	// value, and recursion. Widen the program before reaching for a heavier
	// driver.
	const program = `
function fib(n: i32): i32 {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
function double(n: i32): i32 { return n * 2; }
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < 8) { xs = xs.append(fib(i)); i = i + 1; }
    var rows: i32[][] = [];
    rows = rows.append(xs);
    var s: string = "";
    var j: i32 = 0;
    while (j < xs.len()) { s = s + "ab"; j = j + 1; }
    var mid: string = slice_unchecked(s, 2, 6) + "";
    if (mid.len() != 4 || mid == "zzzz") { return 2; }
    var f: f64 = 3.0;
    var g: f64 = f * f + 4.0;
    var h: f64 = g / 2.0 - 1.5;
    var big: i64 = 1234567890123;
    var q: i64 = big / 1000;
    var r: i64 = big - q * 1000;
    var doubled: i32 = apply(double, xs[5]);
    if (h < 0.0 || r != 123 || doubled < 0) { return 3; }
    if (g > 12.5 && s.len() == 16 && rows[0].len() == 8) { return xs[7]; }
    return 1;
}
`
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	emitBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "asm_ir_run")
	asm := string(runCaptureEnv(t, runner, emitBin, []byte(program), nil, "-target", "x86-64-linux"))
	if len(asm) == 0 {
		t.Fatal("the x86 emitter produced no asm")
	}
	t.Logf("whole-program asm: %d lines", strings.Count(asm, "\n"))

	got := assembleSelfHostX86(t, buildX86AsmBenchDriver(t, gcc), runner, asm)
	want := gnuAsTextX86(t, as, objcopy, dir, asm)

	gotI := decodeX86(t, objdump, dir, "selfhost.bin", got)
	wantI := decodeX86(t, objdump, dir, "gas.bin", want)
	t.Logf("self-host %d bytes / %d insns; gas %d bytes / %d insns (%+d bytes)",
		len(got), len(gotI), len(want), len(wantI), len(got)-len(want))

	if len(gotI) != len(wantI) {
		t.Fatalf("instruction count differs: self-host %d, gas %d — one of them dropped or added an instruction",
			len(gotI), len(wantI))
	}

	bad := 0
	for i := range wantI {
		g, w := gotI[i], wantI[i]
		if g.mnem != w.mnem {
			if bad < 20 {
				t.Errorf("insn %d: mnemonic differs — self-host %q, gas %q", i, g.mnem, w.mnem)
			}
			bad++
			continue
		}
		// A branch operand is an ADDRESS, and the two layouts need not agree
		// byte for byte, so compare where it lands: the index of the
		// instruction it targets must be the same in both streams.
		if g.target >= 0 || w.target >= 0 {
			if g.target != w.target {
				if bad < 20 {
					t.Errorf("insn %d (%s): branch target differs — self-host insn %d, gas insn %d",
						i, w.mnem, g.target, w.target)
				}
				bad++
			}
			continue
		}
		// A rip-relative operand likewise names an address the two lay out
		// differently; everything else must match text for text.
		if strings.Contains(w.ops, "(%rip)") || strings.Contains(g.ops, "(%rip)") {
			continue
		}
		if g.ops != w.ops {
			if bad < 20 {
				t.Errorf("insn %d (%s): operands differ — self-host %q, gas %q", i, w.mnem, g.ops, w.ops)
			}
			bad++
		}
	}
	if bad > 20 {
		t.Errorf("... and %d more (%d instructions differ of %d)", bad-20, bad, len(wantI))
	}

	// The streams agree instruction for instruction, so anything left in the
	// byte total is an encoding the two picked different-sized forms for.
	// Name them, so a residual gap is a list of sites to work rather than a
	// number.
	if len(got) != len(want) {
		shown := 0
		for i := range wantI {
			if gotI[i].size == wantI[i].size {
				continue
			}
			if shown < 20 {
				t.Logf("insn %d (%s %s): self-host %d bytes, gas %d bytes",
					i, wantI[i].mnem, wantI[i].ops, gotI[i].size, wantI[i].size)
			}
			shown++
		}
		t.Logf("%d instructions encode to a different length", shown)
	}
}

// x86Insn is one decoded instruction: its mnemonic, its operand text, its
// encoded length in bytes, and — for a direct branch — the INDEX of the
// instruction it targets, or -1.
type x86Insn struct {
	mnem   string
	ops    string
	size   int
	target int
}

func findGNUToolsX86(t *testing.T) (as, objcopy, objdump string) {
	t.Helper()
	look := func(name string) string {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s not on PATH", name)
		}
		return p
	}
	as, objcopy, objdump = look("as"), look("objcopy"), look("objdump")
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.s")
	if err := os.WriteFile(src, []byte("\t.text\n\tretq\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(as, "--64", src, "-o", filepath.Join(dir, "probe.o")).Run(); err != nil {
		t.Skipf("as does not assemble x86-64: %v", err)
	}
	if err := exec.Command(objdump, "-D", "-b", "binary", "-m", "i386:x86-64", src).Run(); err != nil {
		t.Skipf("objdump does not disassemble i386:x86-64: %v", err)
	}
	return as, objcopy, objdump
}

// gnuAsTextX86 assembles src with GNU as and returns the raw .text bytes.
func gnuAsTextX86(t *testing.T, as, objcopy, dir, src string) []byte {
	t.Helper()
	sPath := filepath.Join(dir, "whole.s")
	oPath := filepath.Join(dir, "whole.o")
	binPath := filepath.Join(dir, "whole.text")
	if err := os.WriteFile(sPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(as, "--64", sPath, "-o", oPath).CombinedOutput(); err != nil {
		// gas failing on the emitter's own output is a finding in its own
		// right: the oracle has to be able to read what we write.
		t.Fatalf("gas rejected the emitter's own output: %v\n%s", err, out)
	}
	if out, err := exec.Command(objcopy, "-O", "binary", "--only-section=.text", oPath, binPath).CombinedOutput(); err != nil {
		t.Fatalf("objcopy: %v\n%s", err, out)
	}
	text, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// decodeX86 disassembles a raw .text image and returns one entry per
// instruction, with direct-branch targets resolved from an address to an
// instruction index so the two streams stay comparable under different
// layouts.
func decodeX86(t *testing.T, objdump, dir, name string, code []byte) []x86Insn {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, code, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(objdump, "-D", "-b", "binary", "-m", "i386:x86-64", "-M", "att", p).Output()
	if err != nil {
		t.Fatalf("objdump: %v", err)
	}
	var insns []x86Insn
	var addrs []int
	idxOf := map[int]int{}
	for _, ln := range strings.Split(string(out), "\n") {
		colon := strings.Index(ln, ":\t")
		if colon < 0 {
			continue
		}
		addr, err := strconv.ParseInt(strings.TrimSpace(ln[:colon]), 16, 64)
		if err != nil {
			continue
		}
		rest := ln[colon+2:]
		tab := strings.Index(rest, "\t")
		if tab < 0 {
			continue // a bytes-only continuation line
		}
		text := strings.TrimSpace(rest[tab+1:])
		if text == "" || strings.HasPrefix(text, "(bad)") {
			continue
		}
		mnem, ops := text, ""
		if sp := strings.IndexAny(text, " \t"); sp > 0 {
			mnem, ops = text[:sp], strings.TrimSpace(text[sp+1:])
		}
		idxOf[int(addr)] = len(insns)
		if n := len(insns); n > 0 {
			insns[n-1].size = int(addr) - addrs[n-1]
		}
		addrs = append(addrs, int(addr))
		insns = append(insns, x86Insn{mnem: mnem, ops: ops, target: -1})
	}
	if n := len(insns); n > 0 {
		insns[n-1].size = len(code) - addrs[n-1]
	}
	// Second pass: a direct branch's operand is a bare hex address; turn it
	// into the index of the instruction living there.
	for i := range insns {
		if !isDirectBranchX86(insns[i].mnem) {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimPrefix(insns[i].ops, "0x"), 16, 64)
		if err != nil {
			continue // indirect / register operand: leave it to the text compare
		}
		if idx, ok := idxOf[int(v)]; ok {
			insns[i].target = idx
		} else {
			insns[i].target = -2 // lands mid-instruction: a finding either way
		}
	}
	_ = addrs
	return insns
}

// isDirectBranchX86 reports whether a mnemonic takes a direct code target,
// whose operand is an address rather than a value.
func isDirectBranchX86(mnem string) bool {
	switch mnem {
	case "jmp", "call", "loop", "loope", "loopne", "jrcxz", "jecxz":
		return true
	}
	return strings.HasPrefix(mnem, "j")
}
