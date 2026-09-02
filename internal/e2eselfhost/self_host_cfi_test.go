package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
	"github.com/jakechampion/lang/internal/native/x86_64"
)

// The self-host CFI differential: the `.cfi_*` directives a program carries,
// recorded by the self-host assembler (cfi.fern) and rendered as .eh_frame +
// .eh_frame_hdr, must be byte-identical to what internal/native/cfi renders
// for the same program at the same addresses. Native is itself pinned to gas
// (TestEhFrameMatchesGNUAs and its arm64 twin), so this is what makes a
// self-host binary's unwind data gas's unwind data.
//
// Addresses are fixed on both sides — .text 0x400000, .eh_frame_hdr 0x400080,
// .eh_frame 0x400100 — because an FDE's initial_location is pcrel and the
// header's rows are datarel: rendering at different addresses would compare
// different bytes for the same rules.

const (
	cfiTextVAddr = 0x400000
	cfiHdrVAddr  = 0x400080
	cfiEhVAddr   = 0x400100
)

// parseEhDump reads the driver's `eh i b` / `hdr i b` lines.
func parseEhDump(out string) (eh, hdr []byte) {
	for _, ln := range strings.Split(out, "\n") {
		var idx, val int
		if _, err := fmt.Sscanf(ln, "eh %d %d", &idx, &val); err == nil {
			eh = append(eh, byte(val))
		} else if _, err := fmt.Sscanf(ln, "hdr %d %d", &idx, &val); err == nil {
			hdr = append(hdr, byte(val))
		}
	}
	return eh, hdr
}

func TestSelfHostCfiMatchesNativeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)

	// The AT&T text goes to the self-host assembler, the Intel text to
	// native; the instructions encode identically (the form differential
	// proves it), so the offsets the rules attach to agree.
	cases := []struct{ name, att, intel string }{
		{"frame_pointer",
			".text\n__fn_f:\n.cfi_startproc\npushq %rbp\n.cfi_def_cfa_offset 16\n.cfi_offset %rbp, -16\nmovq %rsp, %rbp\n.cfi_def_cfa_register %rbp\nmovl $7, %eax\npopq %rbp\n.cfi_def_cfa %rsp, 8\nret\n.cfi_endproc\n",
			".intel_syntax noprefix\n.text\n__fn_f:\n.cfi_startproc\npush rbp\n.cfi_def_cfa_offset 16\n.cfi_offset rbp, -16\nmov rbp, rsp\n.cfi_def_cfa_register rbp\nmov eax, 7\npop rbp\n.cfi_def_cfa rsp, 8\nret\n.cfi_endproc\n"},
		// Two functions sharing one CIE, one with a long body so the advance
		// leaves the packed 6-bit form.
		{"two_procs_long",
			".text\n__fn_a:\n.cfi_startproc\npushq %rbp\n.cfi_def_cfa_offset 16\n.cfi_offset %rbp, -16\nmovq %rsp, %rbp\n.cfi_def_cfa_register %rbp\n" + strings.Repeat("nop\n", 80) + "popq %rbp\n.cfi_def_cfa %rsp, 8\nret\n.cfi_endproc\n" +
				"__fn_b:\n.cfi_startproc\nsubq $8, %rsp\n.cfi_def_cfa_offset 16\naddq $8, %rsp\n.cfi_def_cfa_offset 8\nret\n.cfi_endproc\n",
			".intel_syntax noprefix\n.text\n__fn_a:\n.cfi_startproc\npush rbp\n.cfi_def_cfa_offset 16\n.cfi_offset rbp, -16\nmov rbp, rsp\n.cfi_def_cfa_register rbp\n" + strings.Repeat("nop\n", 80) + "pop rbp\n.cfi_def_cfa rsp, 8\nret\n.cfi_endproc\n" +
				"__fn_b:\n.cfi_startproc\nsub rsp, 8\n.cfi_def_cfa_offset 16\nadd rsp, 8\n.cfi_def_cfa_offset 8\nret\n.cfi_endproc\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := x86_64.ParseProgram(c.intel)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.TextLen(); err != nil {
				t.Fatal(err)
			}
			wantEh, err := a.EhFrame(cfiTextVAddr, cfiEhVAddr)
			if err != nil {
				t.Fatal(err)
			}
			wantHdr, err := a.EhFrameHdr(cfiTextVAddr, cfiEhVAddr, cfiHdrVAddr)
			if err != nil {
				t.Fatal(err)
			}
			out := runX86BenchDriver(t, bin, runner, c.att, "-ehframe")
			if refused := asmRefusals(out); len(refused) > 0 {
				t.Fatalf("the self-host assembler refused: %v", refused)
			}
			gotEh, gotHdr := parseEhDump(out)
			if string(gotEh) != string(wantEh) {
				t.Errorf(".eh_frame differs\nself-host % x\nnative    % x", gotEh, wantEh)
			}
			if string(gotHdr) != string(wantHdr) {
				t.Errorf(".eh_frame_hdr differs\nself-host % x\nnative    % x", gotHdr, wantHdr)
			}
			if len(wantEh) == 0 {
				t.Fatal("native rendered no .eh_frame — the case carries no CFI")
			}
		})
	}

	// A directive the recorder cannot express is a refusal, never a dropped
	// rule: the bytes would stay well-formed and unwind wrongly.
	if refused := refusalsForX86(t, bin, runner, ".text\nf:\n.cfi_startproc\n.cfi_escape 0x2e\nret\n.cfi_endproc\n"); len(refused) == 0 {
		t.Error(".cfi_escape was accepted; the recorder cannot express it")
	}
	if refused := refusalsForX86(t, bin, runner, ".text\nf:\n.cfi_def_cfa_offset 16\nret\n"); len(refused) == 0 {
		t.Error("a rule outside .cfi_startproc was accepted")
	}
}

func TestSelfHostCfiMatchesNativeArm64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	cases := []struct{ name, src string }{
		{"frame_pointer",
			".text\n__fn_f:\n.cfi_startproc\nstp x29, x30, [sp, #-16]!\n.cfi_def_cfa_offset 16\n.cfi_offset x29, -16\n.cfi_offset x30, -8\nmov x29, sp\n.cfi_def_cfa_register x29\nmov w0, #7\nldp x29, x30, [sp], #16\n.cfi_def_cfa sp, 0\nret\n.cfi_endproc\n"},
		// A literal pool between two functions, as the emitter lays out.
		{"two_procs_pool",
			".text\n__fn_a:\n.cfi_startproc\nstp x29, x30, [sp, #-16]!\n.cfi_def_cfa_offset 16\n.cfi_offset x29, -16\n.cfi_offset x30, -8\nmov x29, sp\n.cfi_def_cfa_register x29\nldr x0, =0x123456789\n" + strings.Repeat("nop\n", 70) + "ldp x29, x30, [sp], #16\n.cfi_def_cfa sp, 0\nret\n.cfi_endproc\n.ltorg\n" +
				"__fn_b:\n.cfi_startproc\nsub sp, sp, #32\n.cfi_def_cfa_offset 32\nadd sp, sp, #32\n.cfi_def_cfa_offset 0\nret\n.cfi_endproc\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := arm64.ParseProgram(c.src)
			if err != nil {
				t.Fatal(err)
			}
			wantEh, err := a.EhFrame(cfiTextVAddr, cfiEhVAddr)
			if err != nil {
				t.Fatal(err)
			}
			wantHdr, err := a.EhFrameHdr(cfiTextVAddr, cfiEhVAddr, cfiHdrVAddr)
			if err != nil {
				t.Fatal(err)
			}
			out := runX86BenchDriver(t, bin, runner, c.src, "-ehframe")
			if refused := asmRefusals(out); len(refused) > 0 {
				t.Fatalf("the self-host assembler refused: %v", refused)
			}
			gotEh, gotHdr := parseEhDump(out)
			if string(gotEh) != string(wantEh) {
				t.Errorf(".eh_frame differs\nself-host % x\nnative    % x", gotEh, wantEh)
			}
			if string(gotHdr) != string(wantHdr) {
				t.Errorf(".eh_frame_hdr differs\nself-host % x\nnative    % x", gotHdr, wantHdr)
			}
			if len(wantEh) == 0 {
				t.Fatal("native rendered no .eh_frame — the case carries no CFI")
			}
		})
	}
}
