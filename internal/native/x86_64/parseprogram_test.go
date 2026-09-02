package x86_64_test

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/native/x86_64"
)

// bytesWX lays a out at the addresses the W^X image map picks for it — the
// two steps every caller takes: measure .text, then ask the image writer
// where .text and the data blob go.
func bytesWX(a *x86_64.Assembler) (text, data []byte, err error) {
	n, err := a.TextLen()
	if err != nil {
		return nil, nil, err
	}
	textVAddr, dataVAddr := elf.SegmentAddrsWXX86(n)
	return a.BytesProgramWX(textVAddr, dataVAddr)
}

const twoPhaseSrc = ".text\n.globl _start\n_start:\n\tmov rax, 60\n\tmov rdi, 0\n\tsyscall\n"

// TestParseProgramMatchesOneShot pins the two-phase API to the one-shot
// wrappers: parsing and then laying out must produce exactly what the
// corresponding AssembleProgram* produces, or the split has changed behaviour
// rather than just exposing it.
func TestParseProgramMatchesOneShot(t *testing.T) {
	for _, c := range []struct {
		name string
		lay  func(*x86_64.Assembler) ([]byte, []byte, error)
		one  func(string) ([]byte, []byte, error)
	}{
		{"contiguous", func(a *x86_64.Assembler) ([]byte, []byte, error) {
			return a.BytesProgram(elf.TextVAddr)
		}, func(src string) ([]byte, []byte, error) {
			return x86_64.AssembleProgram(src, elf.TextVAddr)
		}},
		{"wx", bytesWX, func(src string) ([]byte, []byte, error) {
			return x86_64.AssembleProgramWX(src, elf.SegmentAddrsWXX86)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, err := x86_64.ParseProgram(twoPhaseSrc)
			if err != nil {
				t.Fatalf("ParseProgram: %v", err)
			}
			gotText, gotRodata, err := c.lay(a)
			if err != nil {
				t.Fatalf("layout: %v", err)
			}
			wantText, wantRodata, err := c.one(twoPhaseSrc)
			if err != nil {
				t.Fatalf("one-shot: %v", err)
			}
			if !bytes.Equal(gotText, wantText) {
				t.Errorf(".text differs\ntwo-phase %x\none-shot  %x", gotText, wantText)
			}
			if !bytes.Equal(gotRodata, wantRodata) {
				t.Errorf(".rodata differs\ntwo-phase %x\none-shot  %x", gotRodata, wantRodata)
			}
		})
	}
}

// TestEhFrameAfterLayout is the reason the split exists. .eh_frame declares
// pcrel FDE pointers, so rendering it needs its own load address — which
// follows from len(.text) and is therefore unknowable until the layout has
// run. AssembleProgramEhFrame cannot express that: it takes ehVAddr up front.
func TestEhFrameAfterLayout(t *testing.T) {
	const src = ".text\n.globl f\nf:\n\t.cfi_startproc\n\tpush rbp\n\t.cfi_def_cfa_offset 16\n" +
		"\tmov rbp, rsp\n\t.cfi_def_cfa_register rbp\n\tpop rbp\n\t.cfi_def_cfa rsp, 8\n\tret\n\t.cfi_endproc\n"
	a, err := x86_64.ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	text, _, err := bytesWX(a)
	if err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	// The placement the ELF writer would use: 8-aligned, straight after .text.
	ehVAddr := (elf.TextVAddrWX + uint64(len(text)) + 7) &^ 7
	eh, err := a.EhFrame(elf.TextVAddrWX, ehVAddr)
	if err != nil {
		t.Fatalf("EhFrame: %v", err)
	}
	if len(eh) == 0 {
		t.Fatal("no .eh_frame for a source carrying CFI")
	}
	// The FDE's initial_location is the pcrel distance from its own field to
	// the function, so it must reflect the address computed above rather than
	// a zero the caller had to guess before assembling.
	slots := fdeLocSlots(t, eh)
	if len(slots) != 1 {
		t.Fatalf("want 1 FDE, got %d", len(slots))
	}
	got := int32(uint32(eh[slots[0]]) | uint32(eh[slots[0]+1])<<8 | uint32(eh[slots[0]+2])<<16 | uint32(eh[slots[0]+3])<<24)
	want := int32(elf.TextVAddrWX - (ehVAddr + uint64(slots[0])))
	if got != want {
		t.Errorf("initial_location = %d, want %d (pcrel to .text at %#x from %#x)", got, want, elf.TextVAddrWX, ehVAddr+uint64(slots[0]))
	}
}

// TestTextLabelVAddrs resolves labels off the assembler rather than through a
// dedicated wrapper return.
func TestTextLabelVAddrs(t *testing.T) {
	const src = ".text\n.globl a\na:\n\tret\n.globl b\nb:\n\tret\n"
	asm, err := x86_64.ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	if _, _, err := bytesWX(asm); err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	all := asm.TextLabelVAddrs(elf.TextVAddrWX)
	for _, name := range []string{"a", "b"} {
		v, ok := asm.TextLabelVAddr(name, elf.TextVAddrWX)
		if !ok {
			t.Errorf("%q not found", name)
			continue
		}
		if all[name] != v {
			t.Errorf("%q: TextLabelVAddrs says %#x, TextLabelVAddr says %#x", name, all[name], v)
		}
	}
	if _, ok := asm.TextLabelVAddr("nosuch", elf.TextVAddrWX); ok {
		t.Error("an undefined label resolved")
	}
}
