package elf_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
	"github.com/jakechampion/lang/internal/native/elf"
)

// ehSrcArm64 mirrors ehSrc for aarch64: an adrp/add pair into .rodata, tunable
// padding, and CFI-carrying functions.
func ehSrcArm64(fns, pad int) string {
	var b strings.Builder
	b.WriteString(".text\n.globl _start\n_start:\n\tadrp x0, msg\n\tadd x0, x0, #:lo12:msg\n")
	for i := 0; i < pad; i++ {
		b.WriteString("\tnop\n")
	}
	b.WriteString("\tmov x8, #93\n\tmov x0, #0\n\tsvc #0\n")
	for i := 0; i < fns; i++ {
		fmt.Fprintf(&b, ".globl f%d\nf%d:\n\t.cfi_startproc\n\t.cfi_def_cfa_offset 16\n"+
			"\t.cfi_offset 29, -16\n\tret\n\t.cfi_endproc\n", i, i)
	}
	b.WriteString(".section .rodata\nmsg:\n\t.quad 0x1122334455667788\n")
	return b.String()
}

// arm64AdrpTarget decodes the address the adrp+add pair at the start of .text
// forms: adrp carries a signed 21-bit page delta split across immlo (30..29)
// and immhi (23..5); the add supplies the low 12 bits.
func arm64AdrpTarget(text []byte, textVAddr uint64) uint64 {
	adrp := binary.LittleEndian.Uint32(text[0:4])
	add := binary.LittleEndian.Uint32(text[4:8])
	immlo := (adrp >> 29) & 0x3
	immhi := (adrp >> 5) & 0x7ffff
	page := int64(int32(immhi<<13|immlo<<11)) >> 11
	return uint64(int64(textVAddr&^0xfff)+page*0x1000) + uint64((add>>10)&0xfff)
}

// TestEhFramePlacementArm64 is TestEhFramePlacement's aarch64 half. The
// placement rule is shared, so what this adds is the arm64 page size (64 KiB,
// not 4 KiB) and the adrp/add fixup, which resolves through a different path
// from x86-64's rip-relative disp32.
//
// 64 KiB of slack to walk would mean ~16k padding instructions per subtest, so
// this pins the placement and the fixup rather than sweeping the boundary —
// TestEhFramePushesDataSegment covers the crossing on the shared rule.
func TestEhFramePlacementArm64(t *testing.T) {
	a, err := arm64.ParseProgram(ehSrcArm64(3, 4))
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
	m := elf.SegmentMapWXEhArm64(n, hdrLen, 1)
	ehFrame, err := a.EhFrame(m.Text, m.EhFrame)
	if err != nil {
		t.Fatalf("EhFrame: %v", err)
	}
	hdr, err := a.EhFrameHdr(m.Text, m.EhFrame, m.EhHdr)
	if err != nil {
		t.Fatalf("EhFrameHdr: %v", err)
	}
	u := elf.Unwind{Hdr: hdr, Frame: ehFrame}
	m = elf.SegmentMapWXEhArm64(n, len(hdr), len(ehFrame))
	text, data, err := a.BytesProgramWX(m.Text, m.Data)
	if err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	img := elf.StaticExecutableDataWXEhFrame(text, u, data)
	textVAddr, dataVAddr := m.Text, m.Data

	if textVAddr != elf.TextVAddrWX {
		t.Errorf(".text at %#x, want %#x", textVAddr, uint64(elf.TextVAddrWX))
	}
	if m.EhHdr%4 != 0 {
		t.Errorf(".eh_frame_hdr at %#x is not 4-aligned", m.EhHdr)
	}
	if gap := m.EhHdr - (textVAddr + uint64(len(text))); gap >= 4 {
		t.Errorf(".eh_frame_hdr is %d bytes past the end of .text, want under 4", gap)
	}
	if m.EhFrame%8 != 0 {
		t.Errorf(".eh_frame at %#x is not 8-aligned", m.EhFrame)
	}
	if gap := m.EhFrame - (m.EhHdr + uint64(len(hdr))); gap >= 8 {
		t.Errorf(".eh_frame is %d bytes past the end of .eh_frame_hdr, want under 8", gap)
	}
	// arm64 images align segments to the 64 KiB max page, so they load on
	// 4/16/64 KiB-page kernels alike.
	if dataVAddr%0x10000 != 0 {
		t.Errorf("data segment at %#x is not 64 KiB-aligned", dataVAddr)
	}
	if dataVAddr < m.EhFrame+uint64(len(ehFrame)) {
		t.Errorf("data segment at %#x overlaps .eh_frame ending at %#x", dataVAddr, m.EhFrame+uint64(len(ehFrame)))
	}
	hdrOff := m.EhHdr - 0x400000
	if got := img[hdrOff : hdrOff+uint64(len(hdr))]; !bytes.Equal(got, hdr) {
		t.Errorf(".eh_frame_hdr is not at file offset %#x", hdrOff)
	}
	ehOff := m.EhFrame - 0x400000
	if got := img[ehOff : ehOff+uint64(len(ehFrame))]; !bytes.Equal(got, ehFrame) {
		t.Errorf(".eh_frame is not at file offset %#x", ehOff)
	}
	ld := loads(t, img)
	if len(ld) != 2 {
		t.Fatalf("got %d PT_LOADs, want 2", len(ld))
	}
	if end := m.EhFrame + uint64(len(ehFrame)); end > ld[0].vaddr+ld[0].filesz {
		t.Errorf(".eh_frame ends at %#x, past the R+X segment's %#x", end, ld[0].vaddr+ld[0].filesz)
	}
	if got := arm64AdrpTarget(text, textVAddr); got != dataVAddr {
		t.Errorf("adrp/add resolves to %#x, want the data segment at %#x", got, dataVAddr)
	}
	off := ld[1].off + (dataVAddr - ld[1].vaddr)
	if got := binary.LittleEndian.Uint64(img[off:]); got != 0x1122334455667788 {
		t.Errorf("the address the code forms holds %#x, want 0x1122334455667788", got)
	}
}
