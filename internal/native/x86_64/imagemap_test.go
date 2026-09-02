package x86_64

import (
	"encoding/binary"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
)

// lea rax, [rip+msg] is 7 bytes: REX.W 8D /r with a disp32 tail, so the
// address it forms is (end of the instruction) + disp32.
const ripRefSrc = ".text\n.globl _start\n_start:\n" +
	"\tlea rax, [rip+msg]\n" +
	"\tret\n" +
	".section .rodata\nmsg:\n\t.quad 0x1122334455667788\n"

// TestRodataVAddrIsObeyed is the gate on #8034: the assembler must resolve
// .rodata references against the address it is GIVEN, not against a page
// rule of its own. Nothing else fails when the two disagree — the binary
// stays well-formed and simply reads the wrong bytes — so the check moves
// the data segment off the page-boundary default and follows the disp32 to
// see where it actually points.
func TestRodataVAddrIsObeyed(t *testing.T) {
	for _, dataVAddr := range []uint64{elf.TextVAddrWX + 0x4000, elf.TextVAddrWX + 0x12300} {
		a, err := ParseProgram(ripRefSrc)
		if err != nil {
			t.Fatal(err)
		}
		text, _, err := a.BytesProgramWX(elf.TextVAddrWX, dataVAddr)
		if err != nil {
			t.Fatal(err)
		}
		const leaLen = 7
		disp := int32(binary.LittleEndian.Uint32(text[leaLen-4 : leaLen]))
		got := uint64(int64(elf.TextVAddrWX) + leaLen + int64(disp))
		if got != dataVAddr {
			t.Errorf("lea [rip+msg] resolved to %#x, want the given data address %#x", got, dataVAddr)
		}
	}
}

// TestSegmentAddrsWXMatchesDefault pins the default the image writer picks:
// .text at TextVAddrWX, data on the first 4 KiB page boundary past its end
// (x86-64's only page size). A change here changes every x86-64 W^X binary.
func TestSegmentAddrsWXMatchesDefault(t *testing.T) {
	for _, textLen := range []int{0, 1, 0xffc, 0x1000, 0x1004} {
		textVAddr, dataVAddr := elf.SegmentAddrsWXX86(textLen)
		if textVAddr != elf.TextVAddrWX {
			t.Errorf("textLen %d: .text at %#x, want %#x", textLen, textVAddr, uint64(elf.TextVAddrWX))
		}
		end := uint64(elf.TextVAddrWX) + uint64(textLen)
		if want := (end + 0xfff) &^ 0xfff; dataVAddr != want {
			t.Errorf("textLen %d: data at %#x, want %#x", textLen, dataVAddr, want)
		}
	}
}
