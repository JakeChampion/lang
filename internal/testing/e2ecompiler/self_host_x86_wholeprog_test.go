package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostX86WholeProgramMatchesGNUAs(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	as, objcopy, objdump := findGNUToolsX86(t)

	const program = `
function fib(n: i32): i32 {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
function double(n: i32): i32 { return n * 2; }
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < 8) { xs = xs.append(fib(i)); i = i + 1; }
    let rows: i32[][] = [];
    rows = rows.append(xs);
    let s: string = "";
    let j: i32 = 0;
    while (j < xs.len()) { s = s + "ab"; j = j + 1; }
    let mid: string = slice_unchecked(s, 2, 6) + "";
    if (mid.len() != 4 || mid == "zzzz") { return 2; }
    let f: f64 = 3.0;
    let g: f64 = f * f + 4.0;
    let h: f64 = g / 2.0 - 1.5;
    let big: i64 = 1234567890123;
    let q: i64 = big / 1000;
    let r: i64 = big - q * 1000;
    let doubled: i32 = apply(double, xs[5]);
    if (h < 0.0 || r != 123 || doubled < 0) { return 3; }
    if (g > 12.5 && s.len() == 16 && rows[0].len() == 8) { return xs[7]; }
    return 1;
}
`
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	emitBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "asm_ir_run")
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

func gnuAsTextX86(t *testing.T, as, objcopy, dir, src string) []byte {
	t.Helper()
	sPath := filepath.Join(dir, "whole.s")
	oPath := filepath.Join(dir, "whole.o")
	binPath := filepath.Join(dir, "whole.text")
	if err := os.WriteFile(sPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(as, "--64", sPath, "-o", oPath).CombinedOutput(); err != nil {
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
			continue
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
	for i := range insns {
		if !isDirectBranchX86(insns[i].mnem) {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimPrefix(insns[i].ops, "0x"), 16, 64)
		if err != nil {
			continue
		}
		if idx, ok := idxOf[int(v)]; ok {
			insns[i].target = idx
		} else {
			insns[i].target = -2
		}
	}
	_ = addrs
	return insns
}

func isDirectBranchX86(mnem string) bool {
	switch mnem {
	case "jmp", "call", "loop", "loope", "loopne", "jrcxz", "jecxz":
		return true
	}
	return strings.HasPrefix(mnem, "j")
}
