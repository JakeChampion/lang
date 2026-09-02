package elf_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/native/x86_64"
)

// ehSrc is a program that both carries CFI and reads .rodata, which is the
// pairing that matters: .eh_frame goes between .text and the data segment, so
// the rip-relative load has to resolve against where the data segment actually
// ended up. pad nops tune .text's size so the caller can put its end at a
// chosen distance from a page boundary.
func ehSrc(fns, pad int) string {
	var b strings.Builder
	b.WriteString(".text\n.globl _start\n_start:\n\tlea rax, [rip+msg]\n\tmov rax, [rax]\n")
	for i := 0; i < pad; i++ {
		b.WriteString("\tnop\n")
	}
	b.WriteString("\tmov rax, 60\n\txor edi, edi\n\tsyscall\n")
	for i := 0; i < fns; i++ {
		fmt.Fprintf(&b, ".globl f%d\nf%d:\n\t.cfi_startproc\n\tpush rbp\n\t.cfi_def_cfa_offset 16\n"+
			"\tmov rbp, rsp\n\t.cfi_def_cfa_register rbp\n\tpop rbp\n\t.cfi_def_cfa rsp, 8\n\tret\n\t.cfi_endproc\n", i, i)
	}
	b.WriteString(".section .rodata\nmsg:\n\t.quad 0x1122334455667788\n")
	return b.String()
}

// layoutWithEh runs the three steps an image with unwind data takes: settle
// .text, render .eh_frame at the address the map picks for it, then resolve
// against the data address that follows from its length.
func layoutWithEh(t *testing.T, src string) (image, text, ehFrame, data []byte, textVAddr, ehVAddr, dataVAddr uint64) {
	t.Helper()
	a, err := x86_64.ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	n, err := a.TextLen()
	if err != nil {
		t.Fatalf("TextLen: %v", err)
	}
	// .eh_frame's size does not depend on where it loads — every field it
	// carries is fixed-width — so rendering it once at the address the map
	// picks for a zero-length one would be circular. Rendering at the address
	// that follows from .text alone is not: that is where it goes.
	textVAddr, ehVAddr, _ = elf.SegmentAddrsWXEhX86(n, 1)
	ehFrame, err = a.EhFrame(textVAddr, ehVAddr)
	if err != nil {
		t.Fatalf("EhFrame: %v", err)
	}
	if len(ehFrame) == 0 {
		t.Fatal("no .eh_frame for a source carrying CFI")
	}
	textVAddr, ehVAddr, dataVAddr = elf.SegmentAddrsWXEhX86(n, len(ehFrame))
	text, data, err = a.BytesProgramWX(textVAddr, dataVAddr)
	if err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	return elf.StaticExecutableDataX86WXEhFrame(text, ehFrame, data), text, ehFrame, data, textVAddr, ehVAddr, dataVAddr
}

// phdr is one PT_LOAD read back out of the image.
type phdr struct {
	flags                     uint32
	off, vaddr, filesz, memsz uint64
}

func loads(t *testing.T, img []byte) []phdr {
	t.Helper()
	phoff := binary.LittleEndian.Uint64(img[32:])
	phnum := int(binary.LittleEndian.Uint16(img[56:]))
	out := make([]phdr, 0, phnum)
	for i := 0; i < phnum; i++ {
		p := img[phoff+uint64(i)*56:]
		if binary.LittleEndian.Uint32(p) != 1 { // PT_LOAD
			continue
		}
		out = append(out, phdr{
			flags:  binary.LittleEndian.Uint32(p[4:]),
			off:    binary.LittleEndian.Uint64(p[8:]),
			vaddr:  binary.LittleEndian.Uint64(p[16:]),
			filesz: binary.LittleEndian.Uint64(p[32:]),
			memsz:  binary.LittleEndian.Uint64(p[40:]),
		})
	}
	return out
}

// padForSlack returns the nop count that leaves .text ending `slack` bytes
// short of a 4 KiB page boundary. Guessing pad values does not work — the
// first version of this test guessed four and none of them crossed — so the
// sizes are derived from a measured build instead.
func padForSlack(t *testing.T, slack int) int {
	t.Helper()
	a, err := x86_64.ParseProgram(ehSrc(ehTestFDEs, 0))
	if err != nil {
		t.Fatal(err)
	}
	base, err := a.TextLen()
	if err != nil {
		t.Fatal(err)
	}
	end := uint64(elf.TextVAddrWX) + uint64(base)
	target := ((end + 0xfff) &^ 0xfff) - uint64(slack)
	for target < end {
		target += 0x1000
	}
	return int(target - end)
}

// ehTestFDEs is the number of CFI-carrying functions the fixtures emit. Three
// FDEs plus a CIE is a few hundred bytes — comfortably more than the small
// slack sizes below, so those cross a page and the large one does not.
const ehTestFDEs = 3

// TestEhFramePlacement pins where .eh_frame goes and what moves with it.
//
// The slack sizes put .text's end at a chosen distance from a 4 KiB boundary,
// so the small ones are the case #7901 called out as the reason this could
// not be done before the image map had one owner: .eh_frame does not fit
// before the next page, the data segment moves, and an assembler that had
// derived the data address from .text's size alone would have resolved every
// .rodata reference against the old one. Nothing would report it — the image
// stays well-formed and reads the wrong bytes.
func TestEhFramePlacement(t *testing.T) {
	for _, slack := range []int{8, 64, 512, 3000} {
		t.Run(fmt.Sprintf("slack%d", slack), func(t *testing.T) {
			pad := padForSlack(t, slack)
			img, text, ehFrame, data, textVAddr, ehVAddr, dataVAddr := layoutWithEh(t, ehSrc(ehTestFDEs, pad))

			// .eh_frame is 8-aligned and immediately after .text.
			if ehVAddr%8 != 0 {
				t.Errorf(".eh_frame at %#x is not 8-aligned", ehVAddr)
			}
			if gap := ehVAddr - (textVAddr + uint64(len(text))); gap >= 8 {
				t.Errorf(".eh_frame is %d bytes past the end of .text, want under 8", gap)
			}
			// .text's own address is what must NOT move: every PC-relative
			// fixup was resolved against it.
			if textVAddr != elf.TextVAddrWX {
				t.Errorf(".text at %#x, want %#x", textVAddr, uint64(elf.TextVAddrWX))
			}

			// The bytes are in the file where the map says.
			ehOff := ehVAddr - 0x400000
			if got := img[ehOff : ehOff+uint64(len(ehFrame))]; !bytes.Equal(got, ehFrame) {
				t.Errorf(".eh_frame is not at file offset %#x", ehOff)
			}

			ld := loads(t, img)
			if len(ld) != 2 {
				t.Fatalf("got %d PT_LOADs, want 2", len(ld))
			}
			code, rw := ld[0], ld[1]
			if code.flags != 5 { // PF_R|PF_X
				t.Errorf("code segment flags %#x, want R+X", code.flags)
			}
			// .eh_frame is alloc: it must be covered by the R+X segment, or an
			// unwinder at runtime reads unmapped memory.
			if end := ehVAddr + uint64(len(ehFrame)); end > code.vaddr+code.filesz {
				t.Errorf(".eh_frame ends at %#x, past the R+X segment's %#x", end, code.vaddr+code.filesz)
			}
			// The data segment starts past .eh_frame, on a page boundary.
			if rw.vaddr != dataVAddr {
				t.Errorf("data segment at %#x, want the %#x the assembler resolved against", rw.vaddr, dataVAddr)
			}
			if rw.vaddr%0x1000 != 0 {
				t.Errorf("data segment at %#x is not page-aligned", rw.vaddr)
			}
			if rw.vaddr < ehVAddr+uint64(len(ehFrame)) {
				t.Errorf("data segment at %#x overlaps .eh_frame ending at %#x", rw.vaddr, ehVAddr+uint64(len(ehFrame)))
			}

			// The payoff: the rip-relative load still names the real data.
			// `lea rax, [rip+msg]` is the first instruction, 7 bytes.
			disp := int32(binary.LittleEndian.Uint32(text[3:7]))
			target := uint64(int64(textVAddr) + 7 + int64(disp))
			if target != dataVAddr {
				t.Errorf("lea [rip+msg] resolves to %#x, want the data segment at %#x", target, dataVAddr)
			}
			if want := uint64(0x1122334455667788); binary.LittleEndian.Uint64(data) != want {
				t.Errorf("data blob starts %#x, want %#x", binary.LittleEndian.Uint64(data), want)
			}
			off := rw.off + (target - rw.vaddr)
			if got := binary.LittleEndian.Uint64(img[off:]); got != 0x1122334455667788 {
				t.Errorf("the address the code forms holds %#x, want 0x1122334455667788", got)
			}
		})
	}
}

// TestEhFramePushesDataSegment is the size threshold made explicit: the same
// program with more FDEs must move the data segment when they no longer fit
// in the slack before the next page. If no pad in the sweep above ever
// crossed a boundary, the placement test would be pinning the easy case only.
func TestEhFramePushesDataSegment(t *testing.T) {
	crossed := false
	for _, slack := range []int{8, 64, 512, 3000} {
		a, err := x86_64.ParseProgram(ehSrc(ehTestFDEs, padForSlack(t, slack)))
		if err != nil {
			t.Fatal(err)
		}
		n, err := a.TextLen()
		if err != nil {
			t.Fatal(err)
		}
		_, ehVAddr, _ := elf.SegmentAddrsWXEhX86(n, 1)
		eh, err := a.EhFrame(elf.TextVAddrWX, ehVAddr)
		if err != nil {
			t.Fatal(err)
		}
		_, withoutData := elf.SegmentAddrsWXX86(n)
		_, _, withData := elf.SegmentAddrsWXEhX86(n, len(eh))
		if withData != withoutData {
			crossed = true
		}
	}
	if !crossed {
		t.Fatal("no slack size put .eh_frame across a page boundary, so TestEhFramePlacement never exercises the case the image map exists for")
	}
}

// TestEhFrameEmptyIsUnchanged pins that adding the parameter changed nothing
// for the images that have no unwind data — every existing binary.
func TestEhFrameEmptyIsUnchanged(t *testing.T) {
	const src = ".text\n.globl _start\n_start:\n\tmov rax, 60\n\txor edi, edi\n\tsyscall\n" +
		".section .rodata\nmsg:\n\t.quad 0x99\n"
	text, data, err := x86_64.AssembleProgramWX(src, elf.SegmentAddrsWXX86)
	if err != nil {
		t.Fatal(err)
	}
	want := elf.StaticExecutableDataX86WX(text, data)
	for _, eh := range [][]byte{nil, {}} {
		if got := elf.StaticExecutableDataX86WXEhFrame(text, eh, data); !bytes.Equal(got, want) {
			t.Errorf("an image with a %v .eh_frame differs from one built without the parameter (%d vs %d bytes)", eh, len(got), len(want))
		}
	}
}
