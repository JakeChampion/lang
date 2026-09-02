package elf_test

import (
	"bytes"
	goelf "debug/elf"
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

// layoutWithEh runs the steps an image with unwind data takes: settle .text,
// size .eh_frame_hdr from the FDE count, render both unwind images at the
// addresses the map picks for them, then resolve against the data address
// that follows from .eh_frame's length.
func layoutWithEh(t *testing.T, src string) (image, text []byte, u elf.Unwind, data []byte, m elf.ImageMap) {
	t.Helper()
	a, err := x86_64.ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	n, err := a.TextLen()
	if err != nil {
		t.Fatalf("TextLen: %v", err)
	}
	hdrLen := a.EhFrameHdrLen()
	if hdrLen == 0 {
		t.Fatal("no .eh_frame_hdr for a source carrying CFI")
	}
	// Neither unwind address depends on .eh_frame's length — .eh_frame_hdr's
	// size is fixed by the FDE count and comes first — so rendering at the
	// addresses that follow from .text and the FDE count alone is not
	// circular: that is where they go.
	m = elf.SegmentMapWXEhX86(n, hdrLen, 1)
	ehFrame, err := a.EhFrame(m.Text, m.EhFrame)
	if err != nil {
		t.Fatalf("EhFrame: %v", err)
	}
	hdr, err := a.EhFrameHdr(m.Text, m.EhFrame, m.EhHdr)
	if err != nil {
		t.Fatalf("EhFrameHdr: %v", err)
	}
	if len(hdr) != hdrLen {
		t.Fatalf("EhFrameHdr rendered %d bytes, but EhFrameHdrLen placed the map for %d", len(hdr), hdrLen)
	}
	u = elf.Unwind{Hdr: hdr, Frame: ehFrame}
	m = elf.SegmentMapWXEhX86(n, len(hdr), len(ehFrame))
	text, data, err = a.BytesProgramWX(m.Text, m.Data)
	if err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	return elf.StaticExecutableDataX86WXEhFrame(text, u, data), text, u, data, m
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
			img, text, u, data, m := layoutWithEh(t, ehSrc(ehTestFDEs, pad))

			// .eh_frame_hdr comes first, 4-aligned and immediately after
			// .text; .eh_frame follows it, 8-aligned.
			if m.EhHdr%4 != 0 {
				t.Errorf(".eh_frame_hdr at %#x is not 4-aligned", m.EhHdr)
			}
			if gap := m.EhHdr - (m.Text + uint64(len(text))); gap >= 4 {
				t.Errorf(".eh_frame_hdr is %d bytes past the end of .text, want under 4", gap)
			}
			if m.EhFrame%8 != 0 {
				t.Errorf(".eh_frame at %#x is not 8-aligned", m.EhFrame)
			}
			if gap := m.EhFrame - (m.EhHdr + uint64(len(u.Hdr))); gap >= 8 {
				t.Errorf(".eh_frame is %d bytes past the end of .eh_frame_hdr, want under 8", gap)
			}
			// .text's own address is what must NOT move: every PC-relative
			// fixup was resolved against it.
			if m.Text != elf.TextVAddrWX {
				t.Errorf(".text at %#x, want %#x", m.Text, uint64(elf.TextVAddrWX))
			}

			// The bytes are in the file where the map says.
			hdrOff := m.EhHdr - 0x400000
			if got := img[hdrOff : hdrOff+uint64(len(u.Hdr))]; !bytes.Equal(got, u.Hdr) {
				t.Errorf(".eh_frame_hdr is not at file offset %#x", hdrOff)
			}
			ehOff := m.EhFrame - 0x400000
			if got := img[ehOff : ehOff+uint64(len(u.Frame))]; !bytes.Equal(got, u.Frame) {
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
			// Both unwind sections are alloc: they must be covered by the R+X
			// segment, or an unwinder at runtime reads unmapped memory.
			if end := m.EhFrame + uint64(len(u.Frame)); end > code.vaddr+code.filesz {
				t.Errorf(".eh_frame ends at %#x, past the R+X segment's %#x", end, code.vaddr+code.filesz)
			}
			// The data segment starts past .eh_frame, on a page boundary.
			if rw.vaddr != m.Data {
				t.Errorf("data segment at %#x, want the %#x the assembler resolved against", rw.vaddr, m.Data)
			}
			if rw.vaddr%0x1000 != 0 {
				t.Errorf("data segment at %#x is not page-aligned", rw.vaddr)
			}
			if rw.vaddr < m.EhFrame+uint64(len(u.Frame)) {
				t.Errorf("data segment at %#x overlaps .eh_frame ending at %#x", rw.vaddr, m.EhFrame+uint64(len(u.Frame)))
			}

			// The payoff: the rip-relative load still names the real data.
			// `lea rax, [rip+msg]` is the first instruction, 7 bytes.
			disp := int32(binary.LittleEndian.Uint32(text[3:7]))
			target := uint64(int64(m.Text) + 7 + int64(disp))
			if target != m.Data {
				t.Errorf("lea [rip+msg] resolves to %#x, want the data segment at %#x", target, m.Data)
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
		m := elf.SegmentMapWXEhX86(n, a.EhFrameHdrLen(), 1)
		eh, err := a.EhFrame(m.Text, m.EhFrame)
		if err != nil {
			t.Fatal(err)
		}
		_, withoutData := elf.SegmentAddrsWXX86(n)
		withData := elf.SegmentMapWXEhX86(n, a.EhFrameHdrLen(), len(eh)).Data
		if withData != withoutData {
			crossed = true
		}
	}
	if !crossed {
		t.Fatal("no slack size put .eh_frame across a page boundary, so TestEhFramePlacement never exercises the case the image map exists for")
	}
}

// TestEhFrameEmptyIsUnchanged pins that the unwind-carrying entry point and
// the plain one agree on an image with no unwind data. The third program
// header is reserved either way — it holds a PT_NULL here — so .text sits at
// TextVAddrWX in both, which is the property that lets one address map serve
// every W^X image.
func TestEhFrameEmptyIsUnchanged(t *testing.T) {
	const src = ".text\n.globl _start\n_start:\n\tmov rax, 60\n\txor edi, edi\n\tsyscall\n" +
		".section .rodata\nmsg:\n\t.quad 0x99\n"
	text, data, err := x86_64.AssembleProgramWX(src, elf.SegmentAddrsWXX86)
	if err != nil {
		t.Fatal(err)
	}
	want := elf.StaticExecutableDataX86WX(text, data)
	// A half-populated Unwind is unwind data neither side can use: a header
	// describing an absent .eh_frame indexes nothing. It must place nothing
	// rather than half of it.
	for _, u := range []elf.Unwind{{}, {Hdr: []byte{}, Frame: []byte{}}, {Hdr: []byte{1, 2, 3, 4}}, {Frame: []byte{1, 2, 3, 4}}} {
		if got := elf.StaticExecutableDataX86WXEhFrame(text, u, data); !bytes.Equal(got, want) {
			t.Errorf("an image with Unwind{Hdr:%d bytes, Frame:%d bytes} differs from one built without unwind data (%d vs %d bytes)", len(u.Hdr), len(u.Frame), len(got), len(want))
		}
	}
}

// TestDebugImageCarriesEhFrameSection is the discoverability half. Placing
// .eh_frame in the R+X segment puts the bytes in the image; without a section
// header nothing can FIND them, because a debugger locates .eh_frame through
// the section table and the plain W^X image has none.
//
// The header is appended after the .debug_* ones so the fixed indices
// imageWXSymsLines relies on — and e_shstrndx = 4 — do not shift. Parsing the
// result with debug/elf checks that too: section names would come out wrong if
// the string-table index had moved.
func TestDebugImageCarriesEhFrameSection(t *testing.T) {
	_, _, u, data, _ := layoutWithEh(t, ehSrc(ehTestFDEs, 0))
	a, err := x86_64.ParseProgram(ehSrc(ehTestFDEs, 0))
	if err != nil {
		t.Fatal(err)
	}
	n, err := a.TextLen()
	if err != nil {
		t.Fatal(err)
	}
	m := elf.SegmentMapWXEhX86(n, len(u.Hdr), len(u.Frame))
	text, _, err := a.BytesProgramWX(m.Text, m.Data)
	if err != nil {
		t.Fatal(err)
	}
	syms := []elf.Sym{{Name: "_start", Value: m.Text, Size: 4}}
	img := elf.StaticExecutableDataX86WXSymsRows(text, u, data, syms, nil, "p.fern", "/tmp", m.Text+uint64(len(text)), nil)

	f, err := goelf.NewFile(bytes.NewReader(img))
	if err != nil {
		t.Fatalf("the -g image is not a readable ELF: %v", err)
	}
	for _, want := range []struct {
		name  string
		addr  uint64
		body  []byte
		align uint64
	}{
		{".eh_frame_hdr", m.EhHdr, u.Hdr, 4},
		{".eh_frame", m.EhFrame, u.Frame, 8},
	} {
		sec := f.Section(want.name)
		if sec == nil {
			var names []string
			for _, s := range f.Sections {
				names = append(names, s.Name)
			}
			t.Errorf("no %s section header — the bytes are in the image but nothing can find them; sections: %v", want.name, names)
			continue
		}
		if sec.Flags&goelf.SHF_ALLOC == 0 {
			t.Errorf("%s is not SHF_ALLOC — unwinding happens at runtime, so it has to be mapped", want.name)
		}
		if sec.Addr != want.addr {
			t.Errorf("%s section addr %#x, want the %#x the image map chose", want.name, sec.Addr, want.addr)
		}
		if sec.Size != uint64(len(want.body)) {
			t.Errorf("%s section size %d, want %d", want.name, sec.Size, len(want.body))
		}
		if sec.Addralign != want.align {
			t.Errorf("%s section align %d, want %d", want.name, sec.Addralign, want.align)
		}
		got, err := sec.Data()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want.body) {
			t.Errorf("%s section content is not the rendered image", want.name)
		}
	}
	// The sections the -g image already carried must survive the addition.
	for _, want := range []string{".text", ".symtab", ".strtab", ".debug_info"} {
		if f.Section(want) == nil {
			t.Errorf("%s went missing when the unwind sections were added", want)
		}
	}
}
