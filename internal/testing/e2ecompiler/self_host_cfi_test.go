package e2ecompiler

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	cfiTextVAddr = 0x400000
	cfiHdrVAddr  = 0x400080
	cfiEhVAddr   = 0x400100
)

func gasCfi(t *testing.T, o gnuAsOracle, src string) (eh, hdr, dbg []byte) {
	t.Helper()
	dir := t.TempDir()
	sPath, oPath, exe := filepath.Join(dir, "cfi.s"), filepath.Join(dir, "cfi.o"), filepath.Join(dir, "cfi")
	if err := os.WriteFile(sPath, []byte(".cfi_sections .eh_frame, .debug_frame\n"+src), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(o.as, append(append([]string{}, o.asArgs...), sPath, "-o", oPath)...).CombinedOutput(); err != nil {
		t.Fatalf("GNU as rejects the case: %v\n%s", err, msg)
	}
	if msg, err := exec.Command(o.ld, "-static", "--eh-frame-hdr", "--no-check-sections", "-e", fmt.Sprint(cfiTextVAddr),
		fmt.Sprintf("-Ttext=%#x", cfiTextVAddr),
		fmt.Sprintf("--section-start=.eh_frame_hdr=%#x", cfiHdrVAddr),
		fmt.Sprintf("--section-start=.eh_frame=%#x", cfiEhVAddr),
		oPath, "-o", exe).CombinedOutput(); err != nil {
		t.Fatalf("ld: %v\n%s", err, msg)
	}
	read := func(file, section string) []byte {
		out := filepath.Join(dir, filepath.Base(file)+section)
		if msg, err := exec.Command(o.objcopy, "--dump-section", section+"="+out, file, out+".tmp").CombinedOutput(); err != nil {
			t.Fatalf("objcopy %s: %v\n%s", section, err, msg)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	eh = read(oPath, ".eh_frame")
	linked := read(exe, ".eh_frame")
	for off := 0; off+12 <= len(eh); {
		n := int(binary.LittleEndian.Uint32(eh[off:]))
		if binary.LittleEndian.Uint32(eh[off+4:]) != 0 {
			copy(eh[off+8:off+12], linked[off+8:off+12])
		}
		off += 4 + n
	}
	return append(eh, 0, 0, 0, 0), read(exe, ".eh_frame_hdr"), read(exe, ".debug_frame")
}

func checkCfiAgainstGas(t *testing.T, o gnuAsOracle, bin string, runner []string, src string) {
	t.Helper()
	wantEh, wantHdr, wantDbg := gasCfi(t, o, src)
	if len(wantEh) == 0 || len(wantDbg) == 0 {
		t.Fatal("GNU as rendered no CFI — the case carries none")
	}
	out := runX86BenchDriver(t, bin, runner, src, "-ehframe")
	if refused := asmRefusals(out); len(refused) > 0 {
		t.Fatalf("the self-host assembler refused: %v", refused)
	}
	if got := parseDumpLines(out, "eh"); string(got) != string(wantEh) {
		t.Errorf(".eh_frame differs\nself-host % x\nGNU as    % x", got, wantEh)
	}
	if got := parseDumpLines(out, "hdr"); string(got) != string(wantHdr) {
		t.Errorf(".eh_frame_hdr differs\nself-host % x\nGNU ld    % x", got, wantHdr)
	}
	if got := parseDumpLines(out, "dbg"); string(got) != string(wantDbg) {
		t.Errorf(".debug_frame differs\nself-host % x\nGNU as    % x", got, wantDbg)
	}
}

func parseDumpLines(out, tag string) []byte {
	var b []byte
	for _, ln := range strings.Split(out, "\n") {
		var idx, val int
		if _, err := fmt.Sscanf(ln, tag+" %d %d", &idx, &val); err == nil {
			b = append(b, byte(val))
		}
	}
	return b
}

func TestSelfHostCfiMatchesGasX86_64(t *testing.T) {
	gas := gnuX86Oracle(t)
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)

	cases := []struct{ name, src string }{
		{"frame_pointer",
			".text\n__fn_f:\n.cfi_startproc\npushq %rbp\n.cfi_def_cfa_offset 16\n.cfi_offset %rbp, -16\nmovq %rsp, %rbp\n.cfi_def_cfa_register %rbp\nmovl $7, %eax\npopq %rbp\n.cfi_def_cfa %rsp, 8\nret\n.cfi_endproc\n"},
		{"two_procs_long",
			".text\n__fn_a:\n.cfi_startproc\npushq %rbp\n.cfi_def_cfa_offset 16\n.cfi_offset %rbp, -16\nmovq %rsp, %rbp\n.cfi_def_cfa_register %rbp\n" + strings.Repeat("nop\n", 80) + "popq %rbp\n.cfi_def_cfa %rsp, 8\nret\n.cfi_endproc\n" +
				"__fn_b:\n.cfi_startproc\nsubq $8, %rsp\n.cfi_def_cfa_offset 16\naddq $8, %rsp\n.cfi_def_cfa_offset 8\nret\n.cfi_endproc\n"},
		{"three_procs_middle_bare",
			".text\n__fn_a:\n.cfi_startproc\npushq %rbp\n.cfi_def_cfa_offset 16\n.cfi_offset %rbp, -16\npopq %rbp\n.cfi_def_cfa_offset 8\nret\n.cfi_endproc\n" +
				"__fn_b:\n.cfi_startproc\nret\n.cfi_endproc\n" +
				"__fn_c:\n.cfi_startproc\nsubq $8, %rsp\n.cfi_def_cfa_offset 16\naddq $8, %rsp\n.cfi_def_cfa_offset 8\nret\n.cfi_endproc\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkCfiAgainstGas(t, gas, bin, runner, c.src) })
	}

	if refused := refusalsForX86(t, bin, runner, ".text\nf:\n.cfi_startproc\n.cfi_escape 0x2e\nret\n.cfi_endproc\n"); len(refused) == 0 {
		t.Error(".cfi_escape was accepted; the recorder cannot express it")
	}
	if refused := refusalsForX86(t, bin, runner, ".text\nf:\n.cfi_def_cfa_offset 16\nret\n"); len(refused) == 0 {
		t.Error("a rule outside .cfi_startproc was accepted")
	}
}

func TestSelfHostCfiMatchesGasArm64(t *testing.T) {
	gas := gnuArm64Oracle(t)
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	cases := []struct{ name, src string }{
		{"frame_pointer",
			".text\n__fn_f:\n.cfi_startproc\nstp x29, x30, [sp, #-16]!\n.cfi_def_cfa_offset 16\n.cfi_offset x29, -16\n.cfi_offset x30, -8\nmov x29, sp\n.cfi_def_cfa_register x29\nmov w0, #7\nldp x29, x30, [sp], #16\n.cfi_def_cfa sp, 0\nret\n.cfi_endproc\n"},
		{"two_procs_pool",
			".text\n__fn_a:\n.cfi_startproc\nstp x29, x30, [sp, #-16]!\n.cfi_def_cfa_offset 16\n.cfi_offset x29, -16\n.cfi_offset x30, -8\nmov x29, sp\n.cfi_def_cfa_register x29\nldr x0, =0x123456789\n" + strings.Repeat("nop\n", 70) + "ldp x29, x30, [sp], #16\n.cfi_def_cfa sp, 0\nret\n.cfi_endproc\n.ltorg\n" +
				"__fn_b:\n.cfi_startproc\nsub sp, sp, #32\n.cfi_def_cfa_offset 32\nadd sp, sp, #32\n.cfi_def_cfa_offset 0\nret\n.cfi_endproc\n"},
		{"three_procs_middle_bare",
			".text\n__fn_a:\n.cfi_startproc\nstp x29, x30, [sp, #-16]!\n.cfi_def_cfa_offset 16\n.cfi_offset x29, -16\nldp x29, x30, [sp], #16\n.cfi_def_cfa_offset 0\nret\n.cfi_endproc\n" +
				"__fn_b:\n.cfi_startproc\nret\n.cfi_endproc\n" +
				"__fn_c:\n.cfi_startproc\nsub sp, sp, #32\n.cfi_def_cfa_offset 32\nadd sp, sp, #32\n.cfi_def_cfa_offset 0\nret\n.cfi_endproc\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkCfiAgainstGas(t, gas, bin, runner, c.src) })
	}
}

func TestSelfHostCfiArm64Darwin(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	cases := []struct{ name, src, want string }{
		{"frame_pointer",
			".text\n__fn_f:\n.cfi_startproc\nstp x29, x30, [sp, #-16]!\n.cfi_def_cfa_offset 16\n.cfi_offset x29, -16\n.cfi_offset x30, -8\nmov x29, sp\n.cfi_def_cfa_register x29\nmov w0, #7\nldp x29, x30, [sp], #16\n.cfi_def_cfa sp, 0\nret\n.cfi_endproc\n",
			"1000000000000000017a520001781e01100c1f002800000018000000e4feffffffffffff140000000000000000440e109d029e01440d1d480c1f00000000000000000000"},
		{"two_procs_long",
			".text\n__fn_a:\n.cfi_startproc\nstp x29, x30, [sp, #-16]!\n.cfi_def_cfa_offset 16\n.cfi_offset x29, -16\n.cfi_offset x30, -8\nmov x29, sp\n.cfi_def_cfa_register x29\n" + strings.Repeat("nop\n", 70) + "ldp x29, x30, [sp], #16\n.cfi_def_cfa sp, 0\nret\n.cfi_endproc\n" +
				"__fn_b:\n.cfi_startproc\nsub sp, sp, #32\n.cfi_def_cfa_offset 32\nadd sp, sp, #32\n.cfi_def_cfa_offset 0\nret\n.cfi_endproc\n",
			"1000000000000000017a520001781e01100c1f002800000018000000e4feffffffffffff280100000000000000440e109d029e01440d1d031c010c1f000000001c00000044000000e0ffffffffffffff0c0000000000000000440e20440e000000000000"},
		{"three_procs",
			".text\n__fn_a:\n.cfi_startproc\nsub sp, sp, #16\n.cfi_def_cfa_offset 16\nadd sp, sp, #16\n.cfi_def_cfa_offset 0\nret\n.cfi_endproc\n" +
				"__fn_b:\n.cfi_startproc\nstp x29, x30, [sp, #-16]!\n.cfi_def_cfa_offset 16\n.cfi_offset x30, -8\nldp x29, x30, [sp], #16\n.cfi_def_cfa_offset 0\nret\n.cfi_endproc\n" +
				"__fn_c:\n.cfi_startproc\nnop\n.cfi_remember_state\nnop\n.cfi_restore_state\nret\n.cfi_endproc\n",
			"1000000000000000017a520001781e01100c1f002000000018000000e4feffffffffffff0c0000000000000000440e10440e000000000000240000003c000000ccfeffffffffffff0c0000000000000000440e109e01440e00000000000000001c00000064000000b0feffffffffffff0c0000000000000000440a440b00000000000000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, err := hex.DecodeString(c.want)
			if err != nil {
				t.Fatal(err)
			}
			out := runX86BenchDriver(t, bin, runner, c.src, "-ehframe-darwin")
			if refused := asmRefusals(out); len(refused) > 0 {
				t.Fatalf("the self-host assembler refused: %v", refused)
			}
			if got := parseDumpLines(out, "eh"); string(got) != string(want) {
				t.Errorf("__eh_frame differs\nself-host % x\nllvm-mc   % x", got, want)
			}
			if elf := parseDumpLines(runX86BenchDriver(t, bin, runner, c.src, "-ehframe"), "eh"); string(elf) == string(want) {
				t.Error("the ELF and Mach-O profiles rendered identical bytes for this case, so it cannot tell them apart")
			}
		})
	}
}
