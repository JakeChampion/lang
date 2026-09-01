package arm64_test

import (
	"bytes"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// The aarch64 .eh_frame differential, the twin of the x86-64 one.
//
// It exists because the two CIEs differ in EVERY field the DWARF spec leaves
// to the producer, and a wrong value in any of them is silent: the image stays
// well-formed and unwinds nothing.
//
//	                        x86-64                     arm64
//	code alignment          1                          4
//	return address column   16 (rip)                   30 (LR)
//	initial rules           def_cfa rsp+8, RA at -8    def_cfa sp+0, no RA rule
//
// The code alignment is the one with teeth beyond the CIE: an advance is
// encoded in INSTRUCTIONS here, so every FDE delta is a quarter of the byte
// distance. Reusing the x86 encoder unchanged would have produced advances
// four times too long, on an image that decodes cleanly.

func findAarch64Binutils(t *testing.T) (as, objcopy string) {
	t.Helper()
	var err error
	if as, err = exec.LookPath("aarch64-linux-gnu-as"); err != nil {
		t.Skip("aarch64-linux-gnu-as not on PATH")
	}
	if objcopy, err = exec.LookPath("aarch64-linux-gnu-objcopy"); err != nil {
		t.Skip("aarch64-linux-gnu-objcopy not on PATH")
	}
	return as, objcopy
}

func ehFrameFromAarch64Gas(t *testing.T, as, objcopy, src string) []byte {
	t.Helper()
	dir := t.TempDir()
	sPath := filepath.Join(dir, "cfi.s")
	oPath := filepath.Join(dir, "cfi.o")
	binPath := filepath.Join(dir, "cfi.ehframe")
	if err := os.WriteFile(sPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(as, sPath, "-o", oPath).CombinedOutput(); err != nil {
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

func a64LE32(b []byte, at int) uint32 {
	return uint32(b[at]) | uint32(b[at+1])<<8 | uint32(b[at+2])<<16 | uint32(b[at+3])<<24
}

// a64FDELocSlots walks the entry chain and returns each FDE's
// initial_location offset. An entry is [length][id]; id == 0 marks a CIE and a
// zero length terminates.
func a64FDELocSlots(b []byte) []int {
	var out []int
	for off := 0; off+8 <= len(b); {
		n := int(a64LE32(b, off))
		if n == 0 {
			break
		}
		if a64LE32(b, off+4) != 0 {
			out = append(out, off+8)
		}
		off += 4 + n
	}
	return out
}

func a64MaskLocs(b []byte, slots []int) []byte {
	out := append([]byte(nil), b...)
	for _, s := range slots {
		if s+4 <= len(out) {
			copy(out[s:s+4], []byte{0, 0, 0, 0})
		}
	}
	return out
}

// a64SameEhFrame compares our final-image .eh_frame against gas's relocatable
// contribution, allowing the two differences that are expected: gas leaves
// initial_location to a relocation, and the zero-length terminator is appended
// by the LINKER when it concatenates object contributions.
func a64SameEhFrame(want, got []byte) bool {
	w := a64MaskLocs(want, a64FDELocSlots(want))
	g := a64MaskLocs(got, a64FDELocSlots(got))
	if len(g) != len(w)+4 || !bytes.Equal(g[len(w):], []byte{0, 0, 0, 0}) {
		return false
	}
	return bytes.Equal(g[:len(w)], w)
}

func TestArm64EhFrameMatchesGNUAs(t *testing.T) {
	as, objcopy := findAarch64Binutils(t)

	cases := map[string]string{
		// The standard aarch64 frame: stp of fp/lr with a pre-index, then
		// fp := sp. gcc -O0 emits exactly this.
		"frame_pointer": "\t.text\n\t.globl f\nf:\n\t.cfi_startproc\n" +
			"\tstp x29, x30, [sp, #-16]!\n\t.cfi_def_cfa_offset 16\n" +
			"\t.cfi_offset 29, -16\n\t.cfi_offset 30, -8\n" +
			"\tmov x29, sp\n\t.cfi_def_cfa_register 29\n" +
			"\tmov w0, #7\n\tldp x29, x30, [sp], #16\n" +
			"\t.cfi_restore 30\n\t.cfi_restore 29\n\t.cfi_def_cfa 31, 0\n" +
			"\tret\n\t.cfi_endproc\n",

		// ABI aliases rather than numbers: gas takes fp/lr/sp and so must we.
		"named_regs": "\t.text\n\t.globl g\ng:\n\t.cfi_startproc\n" +
			"\tstp x29, x30, [sp, #-16]!\n\t.cfi_def_cfa_offset 16\n" +
			"\t.cfi_offset x29, -16\n\t.cfi_offset x30, -8\n" +
			"\tmov x29, sp\n\t.cfi_def_cfa_register x29\n" +
			"\tldp x29, x30, [sp], #16\n\t.cfi_def_cfa sp, 0\n" +
			"\tret\n\t.cfi_endproc\n",

		// Callee-saved pair in addition to the frame record.
		"saved_pair": "\t.text\n\t.globl h\nh:\n\t.cfi_startproc\n" +
			"\tstp x29, x30, [sp, #-32]!\n\t.cfi_def_cfa_offset 32\n" +
			"\t.cfi_offset 29, -32\n\t.cfi_offset 30, -24\n" +
			"\tstp x19, x20, [sp, #16]\n\t.cfi_offset 19, -16\n\t.cfi_offset 20, -8\n" +
			"\tmov x29, sp\n\t.cfi_def_cfa_register 29\n" +
			"\tldp x19, x20, [sp, #16]\n\t.cfi_restore 20\n\t.cfi_restore 19\n" +
			"\tldp x29, x30, [sp], #32\n\t.cfi_def_cfa 31, 0\n" +
			"\tret\n\t.cfi_endproc\n",

		// remember/restore around an early return.
		"remember_state": "\t.text\n\t.globl k\nk:\n\t.cfi_startproc\n" +
			"\tstp x29, x30, [sp, #-16]!\n\t.cfi_def_cfa_offset 16\n" +
			"\tmov x29, sp\n\t.cfi_def_cfa_register 29\n\t.cfi_remember_state\n" +
			"\tldp x29, x30, [sp], #16\n\t.cfi_def_cfa 31, 0\n\tret\n" +
			"\t.cfi_restore_state\n\tmov w0, #2\n" +
			"\tldp x29, x30, [sp], #16\n\t.cfi_def_cfa 31, 0\n\tret\n\t.cfi_endproc\n",

		// Two functions sharing one CIE.
		"two_procs": "\t.text\n\t.globl a1\na1:\n\t.cfi_startproc\n" +
			"\tsub sp, sp, #16\n\t.cfi_def_cfa_offset 16\n" +
			"\tadd sp, sp, #16\n\t.cfi_def_cfa_offset 0\n\tret\n\t.cfi_endproc\n" +
			"\t.globl a2\na2:\n\t.cfi_startproc\n" +
			"\tsub sp, sp, #32\n\t.cfi_def_cfa_offset 32\n" +
			"\tadd sp, sp, #32\n\t.cfi_def_cfa_offset 0\n\tret\n\t.cfi_endproc\n",

		// Long enough that the advance leaves the packed 6-bit form. With a
		// code alignment of 4 that takes 64 INSTRUCTIONS, not 64 bytes —
		// the distinction this profile exists to get right.
		"long_advance": "\t.text\n\t.globl m\nm:\n\t.cfi_startproc\n" +
			"\tsub sp, sp, #16\n\t.cfi_def_cfa_offset 16\n" +
			a64Nops(80) +
			"\tadd sp, sp, #16\n\t.cfi_def_cfa_offset 0\n\tret\n\t.cfi_endproc\n",
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			want := ehFrameFromAarch64Gas(t, as, objcopy, src)
			if len(want) == 0 {
				t.Fatal("gas produced no .eh_frame — the case carries no CFI")
			}
			_, _, got, err := arm64.AssembleProgramEhFrame(src, 0, 0)
			if err != nil {
				t.Fatalf("AssembleProgramEhFrame: %v", err)
			}
			if !a64SameEhFrame(want, got) {
				t.Errorf("eh_frame differs (initial_location masked, terminator allowed)\ngas:  %s\nours: %s",
					hex.EncodeToString(want), hex.EncodeToString(got))
			}
		})
	}
}

func a64Nops(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += "\tnop\n"
	}
	return s
}

// TestArm64EhFrameAdvanceIsInstructions pins the code-alignment factor by its
// consequence rather than by reading it back out of the CIE. A 4-byte step
// must encode as DW_CFA_advance_loc 1; taking x86's factor of 1 would encode 4
// and describe a rule that takes effect four instructions late.
func TestArm64EhFrameAdvanceIsInstructions(t *testing.T) {
	const src = "\t.text\n\t.globl f\nf:\n\t.cfi_startproc\n" +
		"\tsub sp, sp, #16\n\t.cfi_def_cfa_offset 16\n\tret\n\t.cfi_endproc\n"
	_, _, eh, err := arm64.AssembleProgramEhFrame(src, 0, 0)
	if err != nil {
		t.Fatalf("AssembleProgramEhFrame: %v", err)
	}
	// The FDE payload begins after length, CIE pointer, initial_location,
	// range and the zero augmentation length: the first CFA opcode.
	slots := a64FDELocSlots(eh)
	if len(slots) != 1 {
		t.Fatalf("want 1 FDE, got %d", len(slots))
	}
	// slots[0] is initial_location; skip it, the range, and the
	// augmentation length byte to reach the first CFA opcode.
	first := eh[slots[0]+4+4+1]
	if want := byte(0x40 | 1); first != want {
		t.Errorf("first CFA opcode = %#x, want %#x (DW_CFA_advance_loc 1 = one instruction)", first, want)
	}
}

// TestArm64CFIRejectsUnsupported mirrors the x86 refusals, plus the one that
// is specific to a fixed-width ISA.
func TestArm64CFIRejectsUnsupported(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"startproc_simple", "\t.text\nf:\n\t.cfi_startproc simple\n\tret\n\t.cfi_endproc\n", "not supported"},
		{"rule_outside_proc", "\t.text\nf:\n\t.cfi_def_cfa_offset 16\n\tret\n", "outside"},
		{"endproc_unopened", "\t.text\nf:\n\tret\n\t.cfi_endproc\n", "without .cfi_startproc"},
		{"unaligned_offset", "\t.text\nf:\n\t.cfi_startproc\n\t.cfi_offset 29, -12\n\tret\n\t.cfi_endproc\n", "not a multiple"},
		{"unknown_register", "\t.text\nf:\n\t.cfi_startproc\n\t.cfi_offset q0, -16\n\tret\n\t.cfi_endproc\n", "unknown CFI register"},
		{"unclosed_proc", "\t.text\nf:\n\t.cfi_startproc\n\tret\n", "without .cfi_endproc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, _, err := arm64.AssembleProgramEhFrame(c.src, 0, 0)
			if err == nil {
				t.Fatalf("assembled without error; want %q", c.want)
			}
			if !bytes.Contains([]byte(err.Error()), []byte(c.want)) {
				t.Errorf("error = %v, want it to mention %q", err, c.want)
			}
		})
	}
}
