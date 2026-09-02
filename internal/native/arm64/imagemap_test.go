package arm64

import (
	"encoding/binary"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
)

// bytesWX lays a out at the addresses the W^X image map picks for it — the
// two steps every caller takes: measure .text, then ask the image writer
// where .text and the data blob go.
func bytesWX(a *Assembler) (text, data []byte, err error) {
	n, err := a.TextLen()
	if err != nil {
		return nil, nil, err
	}
	textVAddr, dataVAddr := elf.SegmentAddrsWXArm64(n)
	return a.BytesProgramWX(textVAddr, dataVAddr)
}

const rodataRefSrc = ".text\n.globl _start\n_start:\n" +
	"\tadrp x0, msg\n" +
	"\tadd x0, x0, #:lo12:msg\n" +
	"\tret\n" +
	".section .rodata\nmsg:\n\t.quad 0x1122334455667788\n"

// adrpTarget decodes the page address the adrp+add pair at the start of
// .text resolves to: adrp contributes a signed 21-bit page delta split
// across immlo (bits 30..29) and immhi (bits 23..5), and the add supplies
// the low 12 bits.
func adrpTarget(t *testing.T, text []byte, textVAddr uint64) uint64 {
	t.Helper()
	adrp := binary.LittleEndian.Uint32(text[0:4])
	add := binary.LittleEndian.Uint32(text[4:8])
	immlo := (adrp >> 29) & 0x3
	immhi := (adrp >> 5) & 0x7ffff
	page := int64(int32(immhi<<13|immlo<<11)) >> 11 // sign-extend the 21-bit field
	lo12 := (add >> 10) & 0xfff
	return uint64(int64(textVAddr&^0xfff)+page*0x1000) + uint64(lo12)
}

// TestRodataVAddrIsObeyed is the gate on #8034: the assembler must resolve
// .rodata references against the address it is GIVEN, not against a page
// rule of its own. Nothing else fails when the two disagree — the binary
// stays well-formed and simply reads the wrong bytes — so the check moves
// the data segment off the page-boundary default and follows the adrp/add
// pair to see where it actually points.
func TestRodataVAddrIsObeyed(t *testing.T) {
	for _, dataVAddr := range []uint64{elf.TextVAddrWX + 0x40000, elf.TextVAddrWX + 0x123000} {
		a, err := ParseProgram(rodataRefSrc)
		if err != nil {
			t.Fatal(err)
		}
		text, _, err := a.BytesProgramWX(elf.TextVAddrWX, dataVAddr)
		if err != nil {
			t.Fatal(err)
		}
		if got := adrpTarget(t, text, elf.TextVAddrWX); got != dataVAddr {
			t.Errorf("adrp/add resolved to %#x, want the given data address %#x", got, dataVAddr)
		}
	}
}

// TestSegmentAddrsWXMatchesDefault pins the default the image writer picks:
// .text at TextVAddrWX, data on the first 64 KiB page boundary past its end.
// A change here changes every arm64 W^X binary's layout.
func TestSegmentAddrsWXMatchesDefault(t *testing.T) {
	for _, textLen := range []int{0, 4, 0xfffc, 0x10000, 0x10004} {
		textVAddr, dataVAddr := elf.SegmentAddrsWXArm64(textLen)
		if textVAddr != elf.TextVAddrWX {
			t.Errorf("textLen %d: .text at %#x, want %#x", textLen, textVAddr, uint64(elf.TextVAddrWX))
		}
		end := uint64(elf.TextVAddrWX) + uint64(textLen)
		if want := (end + 0xffff) &^ 0xffff; dataVAddr != want {
			t.Errorf("textLen %d: data at %#x, want %#x", textLen, dataVAddr, want)
		}
	}
}
