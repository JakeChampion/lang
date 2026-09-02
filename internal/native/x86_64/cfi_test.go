package x86_64_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/native/x86_64"
)

// The .eh_frame differential: the same `.cfi_*` source through GNU as and
// through this assembler, compared BYTE FOR BYTE.
//
// Every field of the CIE is a choice the DWARF spec leaves open — augmentation
// string, code and data alignment factors, return-address column, FDE pointer
// encoding — so a hand-derived implementation can be internally consistent and
// still unwind nothing, because the consumer is libgcc/libunwind reading what
// gas conventionally produces. Pinning against gas is the only check that
// means anything here, which is why this test, and not a unit test over the
// opcode encoders, is the gate for #7901.
//
// One field cannot be compared directly. An FDE's initial_location is pcrel to
// its own slot; in gas's relocatable object it is left zero with an
// R_X86_64_PC32 in .rela.eh_frame, while this assembler emits final images and
// computes the displacement. So the comparison masks those four bytes and
// checks them separately against the layout they are supposed to describe.

// ehFrameFromGas assembles src with GNU as and returns its .eh_frame bytes.
func ehFrameFromGas(t *testing.T, as, objcopy, src string) []byte {
	t.Helper()
	dir := t.TempDir()
	sPath := filepath.Join(dir, "cfi.s")
	oPath := filepath.Join(dir, "cfi.o")
	binPath := filepath.Join(dir, "cfi.ehframe")
	if err := os.WriteFile(sPath, []byte(".intel_syntax noprefix\n"+src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(as, "--64", sPath, "-o", oPath).CombinedOutput(); err != nil {
		t.Fatalf("gas rejected the source: %v\n%s", err, out)
	}
	if out, err := exec.Command(objcopy, "-O", "binary", "--only-section=.eh_frame", oPath, binPath).CombinedOutput(); err != nil {
		t.Fatalf("objcopy: %v\n%s", err, out)
	}
	b, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fdeLocSlots returns the offsets of every FDE initial_location field in an
// .eh_frame image, walking the entry chain rather than guessing: an entry is
// [length][id], id == 0 marks a CIE, and a zero length terminates.
func fdeLocSlots(t *testing.T, b []byte) []int {
	t.Helper()
	var out []int
	for off := 0; off+8 <= len(b); {
		n := int(ehLE32(b, off))
		if n == 0 {
			break
		}
		if id := ehLE32(b, off+4); id != 0 {
			out = append(out, off+8)
		}
		off += 4 + n
	}
	return out
}

func ehLE32(b []byte, at int) uint32 {
	return uint32(b[at]) | uint32(b[at+1])<<8 | uint32(b[at+2])<<16 | uint32(b[at+3])<<24
}

// sameEhFrame compares our final-image .eh_frame against gas's relocatable
// contribution. Two differences are expected and are the only ones allowed:
//
//   - initial_location. pcrel to its own slot; gas leaves it zero with an
//     R_X86_64_PC32 in .rela.eh_frame, we compute it. Masked here and checked
//     by TestEhFrameInitialLocationIsPCRel instead.
//   - the terminator. A linked .eh_frame ends with a zero-length entry, which
//     the LINKER appends when it concatenates object contributions — so gas's
//     .o does not carry one and our final image must.
func sameEhFrame(t *testing.T, want, got []byte) bool {
	t.Helper()
	w := maskLocs(want, fdeLocSlots(t, want))
	g := maskLocs(got, fdeLocSlots(t, got))
	if len(g) != len(w)+4 {
		return false
	}
	if !bytes.Equal(g[len(w):], []byte{0, 0, 0, 0}) {
		return false
	}
	return bytes.Equal(g[:len(w)], w)
}

// maskLocs zeroes every FDE initial_location so the rest can be compared.
func maskLocs(b []byte, slots []int) []byte {
	out := append([]byte(nil), b...)
	for _, s := range slots {
		if s+4 <= len(out) {
			copy(out[s:s+4], []byte{0, 0, 0, 0})
		}
	}
	return out
}

func TestEhFrameMatchesGNUAs(t *testing.T) {
	as, objcopy := findX86Binutils(t)

	cases := map[string]string{
		// The textbook frame-pointer prologue: gcc -O0 emits exactly this.
		"frame_pointer": "" +
			"\t.text\n\t.globl f\nf:\n\t.cfi_startproc\n" +
			"\tpush rbp\n\t.cfi_def_cfa_offset 16\n\t.cfi_offset 6, -16\n" +
			"\tmov rbp, rsp\n\t.cfi_def_cfa_register 6\n" +
			"\tmov eax, 7\n\tpop rbp\n\t.cfi_def_cfa 7, 8\n\tret\n\t.cfi_endproc\n",

		// Leaf with only a stack adjustment — no saved registers at all.
		"leaf_sub": "" +
			"\t.text\n\t.globl g\ng:\n\t.cfi_startproc\n" +
			"\tsub rsp, 24\n\t.cfi_def_cfa_offset 32\n" +
			"\txor eax, eax\n\tadd rsp, 24\n\t.cfi_def_cfa_offset 8\n\tret\n\t.cfi_endproc\n",

		// Callee-saved registers named rather than numbered. rbx is DWARF 3
		// and r12 is 12, so this also pins that the numbering is DWARF's and
		// not the ModRM order, where rbx would be 3 but rsp and rbp swap.
		"named_regs": "" +
			"\t.text\n\t.globl h\nh:\n\t.cfi_startproc\n" +
			"\tpush rbx\n\t.cfi_def_cfa_offset 16\n\t.cfi_offset rbx, -16\n" +
			"\tpush r12\n\t.cfi_def_cfa_offset 24\n\t.cfi_offset r12, -24\n" +
			"\tmov eax, 1\n\tpop r12\n\t.cfi_restore r12\n" +
			"\tpop rbx\n\t.cfi_restore rbx\n\t.cfi_def_cfa_offset 8\n\tret\n\t.cfi_endproc\n",

		// remember/restore around an epilogue in the middle of a function.
		"remember_state": "" +
			"\t.text\n\t.globl k\nk:\n\t.cfi_startproc\n" +
			"\tpush rbp\n\t.cfi_def_cfa_offset 16\n\t.cfi_offset 6, -16\n" +
			"\tmov rbp, rsp\n\t.cfi_def_cfa_register 6\n" +
			"\t.cfi_remember_state\n\tpop rbp\n\t.cfi_def_cfa 7, 8\n\tret\n" +
			"\t.cfi_restore_state\n\tmov eax, 2\n\tpop rbp\n\t.cfi_def_cfa 7, 8\n\tret\n\t.cfi_endproc\n",

		// Two functions: proves the CIE is shared and that each FDE's CIE
		// pointer is the distance back to it, which differs per FDE.
		"two_procs": "" +
			"\t.text\n\t.globl a1\na1:\n\t.cfi_startproc\n" +
			"\tpush rbp\n\t.cfi_def_cfa_offset 16\n\tpop rbp\n\t.cfi_def_cfa_offset 8\n\tret\n\t.cfi_endproc\n" +
			"\t.globl a2\na2:\n\t.cfi_startproc\n" +
			"\tsub rsp, 8\n\t.cfi_def_cfa_offset 16\n\tadd rsp, 8\n\t.cfi_def_cfa_offset 8\n\tret\n\t.cfi_endproc\n",

		// A span long enough that the advance needs advance_loc1 rather than
		// the packed 6-bit form.
		"long_advance": "" +
			"\t.text\n\t.globl m\nm:\n\t.cfi_startproc\n" +
			"\tpush rbp\n\t.cfi_def_cfa_offset 16\n" +
			pad64() +
			"\tpop rbp\n\t.cfi_def_cfa_offset 8\n\tret\n\t.cfi_endproc\n",
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			want := ehFrameFromGas(t, as, objcopy, src)
			if len(want) == 0 {
				t.Fatal("gas produced no .eh_frame — the case carries no CFI")
			}
			_, _, got, err := x86_64.AssembleProgramEhFrame(src, 0, 0)
			if err != nil {
				t.Fatalf("AssembleProgramEhFrame: %v", err)
			}
			if len(got) == 0 {
				t.Fatal("no .eh_frame produced")
			}
			wantSlots := fdeLocSlots(t, want)
			gotSlots := fdeLocSlots(t, got)
			if len(wantSlots) != len(gotSlots) {
				t.Fatalf("FDE count differs: gas %d, ours %d\ngas:  %s\nours: %s",
					len(wantSlots), len(gotSlots), hex.EncodeToString(want), hex.EncodeToString(got))
			}
			if !sameEhFrame(t, want, got) {
				t.Errorf("eh_frame differs (initial_location masked, terminator allowed)\ngas:  %s\nours: %s",
					hex.EncodeToString(want), hex.EncodeToString(got))
			}
		})
	}
}

// pad64 is 64 bytes of one-byte instructions, to push a CFA advance past the
// 6-bit DW_CFA_advance_loc form and onto advance_loc1.
func pad64() string {
	s := ""
	for i := 0; i < 64; i++ {
		s += "\tnop\n"
	}
	return s
}

// TestEhFrameInitialLocationIsPCRel checks the field the byte comparison has
// to mask. The CIE advertises DW_EH_PE_pcrel|sdata4, so each FDE's
// initial_location must be the signed distance from that field's own address
// to the function it describes — the one part of the image gas cannot pin for
// us, since its object leaves it to a relocation.
func TestEhFrameInitialLocationIsPCRel(t *testing.T) {
	const src = "\t.text\n\t.globl f\nf:\n\t.cfi_startproc\n\tnop\n\tret\n\t.cfi_endproc\n" +
		"\t.globl g\ng:\n\t.cfi_startproc\n\tret\n\t.cfi_endproc\n"
	const textVAddr, ehVAddr = 0x401000, 0x402000
	text, _, eh, err := x86_64.AssembleProgramEhFrame(src, textVAddr, ehVAddr)
	if err != nil {
		t.Fatalf("AssembleProgramEhFrame: %v", err)
	}
	slots := fdeLocSlots(t, eh)
	if len(slots) != 2 {
		t.Fatalf("want 2 FDEs, got %d", len(slots))
	}
	// f is at .text+0 and g at .text+2 (nop, retq).
	for i, want := range []uint64{textVAddr, textVAddr + 2} {
		at := slots[i]
		got := uint64(int64(ehVAddr) + int64(at) + int64(int32(ehLE32(eh, at))))
		if got != want {
			t.Errorf("FDE %d initial_location resolves to %#x, want %#x", i, got, want)
		}
	}
	if len(text) != 3 {
		t.Fatalf("expected a 3-byte .text (nop, retq, retq), got %d", len(text))
	}
}

// TestCFIRejectsUnsupported pins that the directives this emitter cannot
// encode correctly are REFUSED rather than assembled into plausible-looking
// bytes. Silently wrong unwind data is worse than a build error: it is only
// read when something has already gone wrong.
func TestCFIRejectsUnsupported(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"startproc_simple", "\t.text\nf:\n\t.cfi_startproc simple\n\tret\n\t.cfi_endproc\n", "not supported"},
		{"rule_outside_proc", "\t.text\nf:\n\t.cfi_def_cfa_offset 16\n\tret\n", "outside"},
		{"endproc_unopened", "\t.text\nf:\n\tret\n\t.cfi_endproc\n", "without .cfi_startproc"},
		{"nested_startproc", "\t.text\nf:\n\t.cfi_startproc\n\t.cfi_startproc\n\tret\n\t.cfi_endproc\n", "inside an open"},
		{"unaligned_offset", "\t.text\nf:\n\t.cfi_startproc\n\t.cfi_offset 6, -12\n\tret\n\t.cfi_endproc\n", "not a multiple"},
		{"positive_offset", "\t.text\nf:\n\t.cfi_startproc\n\t.cfi_offset 6, 16\n\tret\n\t.cfi_endproc\n", "not supported"},
		{"unknown_register", "\t.text\nf:\n\t.cfi_startproc\n\t.cfi_offset xmm0, -16\n\tret\n\t.cfi_endproc\n", "unknown CFI register"},
		{"unknown_directive", "\t.text\nf:\n\t.cfi_startproc\n\t.cfi_lsda 0, x\n\tret\n\t.cfi_endproc\n", "unsupported CFI directive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, _, err := x86_64.AssembleProgramEhFrame(c.src, 0, 0)
			if err == nil {
				t.Fatalf("assembled without error; want %q", c.want)
			}
			if !bytes.Contains([]byte(err.Error()), []byte(c.want)) {
				t.Errorf("error = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

// TestEhFrameSurvivesRelaxation is the regression this feature's worst bug
// would need. A CFA rule's position is stored as the DISTANCE from the
// previous one, so shrinking a branch between two rules invalidates the
// delta — and the result is still a well-formed .eh_frame that unwinds at the
// wrong instruction. Assembling the same CFI around a branch that relaxes must
// therefore agree with gas, which relaxes it too.
func TestEhFrameSurvivesRelaxation(t *testing.T) {
	as, objcopy := findX86Binutils(t)
	src := "\t.text\n\t.globl f\nf:\n\t.cfi_startproc\n" +
		"\tpush rbp\n\t.cfi_def_cfa_offset 16\n" +
		"\tjmp .Lskip\n" + pad8() + ".Lskip:\n" +
		"\tpop rbp\n\t.cfi_def_cfa_offset 8\n\tret\n\t.cfi_endproc\n"
	want := ehFrameFromGas(t, as, objcopy, src)
	_, _, got, err := x86_64.AssembleProgramEhFrame(src, 0, 0)
	if err != nil {
		t.Fatalf("AssembleProgramEhFrame: %v", err)
	}
	if !sameEhFrame(t, want, got) {
		t.Errorf("eh_frame differs across a relaxed branch — the CFA deltas did not follow the new layout\ngas:  %s\nours: %s",
			hex.EncodeToString(want), hex.EncodeToString(got))
	}
}

func pad8() string {
	s := ""
	for i := 0; i < 8; i++ {
		s += fmt.Sprintf("\tnop\n")
	}
	return s
}

// debugFrameFromGas assembles src with `.cfi_sections .debug_frame` in force
// and returns the .debug_frame contribution.
func debugFrameFromGas(t *testing.T, as, objcopy, src string) []byte {
	t.Helper()
	dir := t.TempDir()
	sPath := filepath.Join(dir, "df.s")
	oPath := filepath.Join(dir, "df.o")
	binPath := filepath.Join(dir, "df.debugframe")
	if err := os.WriteFile(sPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(as, "--64", sPath, "-o", oPath).CombinedOutput(); err != nil {
		t.Fatalf("gas rejected the source: %v\n%s", err, out)
	}
	// Not `-O binary`: that keeps only ALLOC sections, and .debug_frame is
	// not one.
	if out, err := exec.Command(objcopy, "--dump-section", ".debug_frame="+binPath, oPath, oPath+".2").CombinedOutput(); err != nil {
		t.Fatalf("objcopy: %v\n%s", err, out)
	}
	b, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// debugFrameMaskLocs zeroes each FDE's 8-byte initial_location in a
// .debug_frame image: gas leaves it for a relocation, we write the address.
// The CIE id is 0xffffffff here, so an FDE is any entry whose id is not that.
func debugFrameMaskLocs(b []byte) []byte {
	out := append([]byte(nil), b...)
	for off := 0; off+8 <= len(out); {
		n := int(ehLE32(out, off))
		if n == 0 {
			break
		}
		if ehLE32(out, off+4) != 0xffffffff && off+16 <= len(out) {
			copy(out[off+8:off+16], make([]byte, 8))
		}
		off += 4 + n
	}
	return out
}

// TestDebugFrameMatchesGNUAs pins the debugger-facing container of the same
// rules .eh_frame carries. Every framing field differs from .eh_frame's —
// CIE id, augmentation, pointer width, entry alignment, terminator — and gas
// with `.cfi_sections .debug_frame` is the oracle for all of them at once.
func TestDebugFrameMatchesGNUAs(t *testing.T) {
	as, objcopy := findX86Binutils(t)
	cases := map[string]string{
		"frame_pointer": "\t.intel_syntax noprefix\n\t.text\n\t.cfi_sections .debug_frame\n\t.globl f\nf:\n\t.cfi_startproc\n" +
			"\tpush rbp\n\t.cfi_def_cfa_offset 16\n\t.cfi_offset rbp, -16\n" +
			"\tmov rbp, rsp\n\t.cfi_def_cfa_register rbp\n" +
			"\tpop rbp\n\t.cfi_def_cfa rsp, 8\n\tret\n\t.cfi_endproc\n",
		// Two FDEs of different lengths, so the entry padding rule is
		// exercised at more than one phase.
		"two_procs": "\t.intel_syntax noprefix\n\t.text\n\t.cfi_sections .debug_frame\n\t.globl f\nf:\n\t.cfi_startproc\n" +
			"\tpush rbp\n\t.cfi_def_cfa_offset 16\n\t.cfi_offset rbp, -16\n" +
			"\tmov rbp, rsp\n\t.cfi_def_cfa_register rbp\n" +
			"\tpop rbp\n\t.cfi_def_cfa rsp, 8\n\tret\n\t.cfi_endproc\n" +
			"\t.globl g\ng:\n\t.cfi_startproc\n\tsub rsp, 8\n\t.cfi_def_cfa_offset 16\n" +
			"\tadd rsp, 8\n\t.cfi_def_cfa_offset 8\n\tret\n\t.cfi_endproc\n" +
			"\t.globl h\nh:\n\t.cfi_startproc\n\tpush rbx\n\t.cfi_def_cfa_offset 16\n\t.cfi_offset rbx, -16\n" +
			"\tpush r12\n\t.cfi_def_cfa_offset 24\n\t.cfi_offset r12, -24\n" +
			"\tpop r12\n\t.cfi_def_cfa_offset 16\n\tpop rbx\n\t.cfi_def_cfa_offset 8\n\tret\n\t.cfi_endproc\n",
		"long_advance": "\t.intel_syntax noprefix\n\t.text\n\t.cfi_sections .debug_frame\n\t.globl m\nm:\n\t.cfi_startproc\n" +
			"\tsub rsp, 8\n\t.cfi_def_cfa_offset 16\n" + pad64() +
			"\tadd rsp, 8\n\t.cfi_def_cfa_offset 8\n\tret\n\t.cfi_endproc\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			want := debugFrameFromGas(t, as, objcopy, src)
			if len(want) == 0 {
				t.Fatal("gas produced no .debug_frame")
			}
			a, err := x86_64.ParseProgram(src)
			if err != nil {
				t.Fatal(err)
			}
			got, err := a.DebugFrame(0x401000)
			if err != nil {
				t.Fatalf("DebugFrame: %v", err)
			}
			if w, g := debugFrameMaskLocs(want), debugFrameMaskLocs(got); !bytes.Equal(w, g) {
				t.Errorf(".debug_frame differs (initial_location masked)\ngas:  %s\nours: %s",
					hex.EncodeToString(want), hex.EncodeToString(got))
			}
			// The field the mask hides: absolute, not pcrel.
			var locs []uint64
			for off := 0; off+8 <= len(got); {
				n := int(ehLE32(got, off))
				if ehLE32(got, off+4) != 0xffffffff {
					locs = append(locs, uint64(ehLE32(got, off+8))|uint64(ehLE32(got, off+12))<<32)
				}
				off += 4 + n
			}
			if len(locs) == 0 || locs[0] != 0x401000 {
				t.Errorf("first FDE initial_location = %#x, want the absolute .text address 0x401000", locs)
			}
		})
	}
}
