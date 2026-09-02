package elf_test

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
)

// The .eh_frame_hdr half of #7901. Placing .eh_frame and giving it a section
// header makes it findable by a DEBUGGER; a running program's unwinder never
// looks at section headers. It calls dl_iterate_phdr, finds PT_GNU_EH_FRAME,
// and reads .eh_frame's address out of the first bytes of the segment that
// header covers. Without this the CFI is complete, correctly placed, and
// unreachable from inside the process.
//
// Two bases are in play and swapping them yields a header that decodes
// without complaint and indexes garbage: eh_frame_ptr is pcrel (from its own
// field), every table row is datarel (from the start of the section).

// segment is one program header of any type, read back out of an image.
type segment struct {
	typ, flags                uint32
	off, vaddr, filesz, align uint64
}

func segments(t *testing.T, img []byte) []segment {
	t.Helper()
	phoff := binary.LittleEndian.Uint64(img[32:])
	phnum := int(binary.LittleEndian.Uint16(img[56:]))
	out := make([]segment, 0, phnum)
	for i := 0; i < phnum; i++ {
		p := img[phoff+uint64(i)*56:]
		out = append(out, segment{
			typ:    binary.LittleEndian.Uint32(p),
			flags:  binary.LittleEndian.Uint32(p[4:]),
			off:    binary.LittleEndian.Uint64(p[8:]),
			vaddr:  binary.LittleEndian.Uint64(p[16:]),
			filesz: binary.LittleEndian.Uint64(p[32:]),
			align:  binary.LittleEndian.Uint64(p[48:]),
		})
	}
	return out
}

const ptGNUEhFrame = 0x6474e550

// TestPTGNUEhFrameCoversTheHeader pins the program header itself: the segment
// an unwinder will find has to be the .eh_frame_hdr bytes exactly, at the
// address the image map chose.
func TestPTGNUEhFrameCoversTheHeader(t *testing.T) {
	img, _, u, _, m := layoutWithEh(t, ehSrc(ehTestFDEs, 0))

	var found []segment
	for _, s := range segments(t, img) {
		if s.typ == ptGNUEhFrame {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d PT_GNU_EH_FRAME program headers, want exactly 1 — an unwinder finds .eh_frame through this and nothing else", len(found))
	}
	s := found[0]
	if s.vaddr != m.EhHdr {
		t.Errorf("PT_GNU_EH_FRAME p_vaddr %#x, want the %#x the image map chose for .eh_frame_hdr", s.vaddr, m.EhHdr)
	}
	if s.off != m.EhHdr-0x400000 {
		t.Errorf("PT_GNU_EH_FRAME p_offset %#x, want %#x", s.off, m.EhHdr-0x400000)
	}
	if s.filesz != uint64(len(u.Hdr)) {
		t.Errorf("PT_GNU_EH_FRAME p_filesz %d, want the header's %d bytes", s.filesz, len(u.Hdr))
	}
	if s.flags != 4 { // PF_R
		t.Errorf("PT_GNU_EH_FRAME p_flags %#x, want 4 (PF_R)", s.flags)
	}
	if s.align != 4 {
		t.Errorf("PT_GNU_EH_FRAME p_align %d, want 4 — the table is sdata4 throughout", s.align)
	}
}

// fdeAt is one entry found by walking .eh_frame independently of the code that
// wrote it: where the entry itself lives, and the function it describes.
type fdeAt struct {
	entry uint64 // vaddr of the FDE's length field
	fn    uint64 // the function its initial_location names
}

// walkEhFrame decodes the entry chain from the on-disk format: each entry is a
// 4-byte length followed by that many bytes, whose first word is 0 for the CIE
// and a back-pointer for an FDE. An FDE's initial_location is a pcrel sdata4
// measured from its own field.
func walkEhFrame(t *testing.T, eh []byte, ehVAddr uint64) []fdeAt {
	t.Helper()
	var out []fdeAt
	for off := 0; off+4 <= len(eh); {
		n := int(binary.LittleEndian.Uint32(eh[off:]))
		if n == 0 {
			return out // terminator
		}
		if off+4+n > len(eh) {
			t.Fatalf("entry at %#x claims %d bytes, past the end of a %d-byte .eh_frame", off, n, len(eh))
		}
		body := eh[off+4 : off+4+n]
		if binary.LittleEndian.Uint32(body) != 0 { // an FDE, not the CIE
			field := ehVAddr + uint64(off) + 8
			disp := int32(binary.LittleEndian.Uint32(body[4:]))
			out = append(out, fdeAt{entry: ehVAddr + uint64(off), fn: uint64(int64(field) + int64(disp))})
		}
		off += 4 + n
	}
	t.Fatal(".eh_frame ended without a zero-length terminator")
	return nil
}

// decodeHdr reads back a .eh_frame_hdr the way an unwinder does.
func decodeHdr(t *testing.T, hdr []byte, hdrVAddr uint64) (ehFramePtr uint64, table []fdeAt) {
	t.Helper()
	if len(hdr) < 12 {
		t.Fatalf(".eh_frame_hdr is %d bytes, too short to hold even the prefix", len(hdr))
	}
	if got := [4]byte{hdr[0], hdr[1], hdr[2], hdr[3]}; got != [4]byte{1, 0x1b, 0x03, 0x3b} {
		t.Fatalf(".eh_frame_hdr opens %v, want [1 27 3 59] — version 1, eh_frame_ptr pcrel|sdata4, fde_count udata4, table datarel|sdata4", got)
	}
	ehFramePtr = uint64(int64(hdrVAddr+4) + int64(int32(binary.LittleEndian.Uint32(hdr[4:]))))
	n := int(binary.LittleEndian.Uint32(hdr[8:]))
	if want := 12 + 8*n; len(hdr) != want {
		t.Fatalf(".eh_frame_hdr is %d bytes for %d FDEs, want %d", len(hdr), n, want)
	}
	for i := 0; i < n; i++ {
		row := hdr[12+8*i:]
		table = append(table, fdeAt{
			fn:    uint64(int64(hdrVAddr) + int64(int32(binary.LittleEndian.Uint32(row)))),
			entry: uint64(int64(hdrVAddr) + int64(int32(binary.LittleEndian.Uint32(row[4:])))),
		})
	}
	return ehFramePtr, table
}

// TestEhFrameHdrTableMatchesFDEs is the content gate: every row of the search
// table has to name a real FDE and the function that FDE describes, and the
// rows have to be in ascending function order because the table is binary
// searched. A row that is merely well-formed sends an unwinder to the wrong
// frame, which is only ever read once something has already gone wrong.
func TestEhFrameHdrTableMatchesFDEs(t *testing.T) {
	_, _, u, _, m := layoutWithEh(t, ehSrc(ehTestFDEs, 0))

	ehFramePtr, table := decodeHdr(t, u.Hdr, m.EhHdr)
	if ehFramePtr != m.EhFrame {
		t.Errorf("eh_frame_ptr resolves to %#x, want .eh_frame at %#x", ehFramePtr, m.EhFrame)
	}
	want := walkEhFrame(t, u.Frame, m.EhFrame)
	if len(want) != ehTestFDEs {
		t.Fatalf(".eh_frame holds %d FDEs, want %d — the fixture is not exercising what it claims", len(want), ehTestFDEs)
	}
	if len(table) != len(want) {
		t.Fatalf("search table has %d rows for %d FDEs", len(table), len(want))
	}
	for i, w := range want {
		if table[i] != w {
			t.Errorf("row %d: table says function %#x described by FDE %#x, .eh_frame says function %#x described by FDE %#x",
				i, table[i].fn, table[i].entry, w.fn, w.entry)
		}
		if i > 0 && table[i].fn <= table[i-1].fn {
			t.Errorf("row %d function %#x does not follow row %d's %#x — the table is binary searched, so it must ascend",
				i, table[i].fn, i-1, table[i-1].fn)
		}
	}
}

// TestEhFrameHdrGateDetectsAWrongBase proves the check above discriminates.
// The two pointer bases are one line apart in the renderer and interchanging
// them produces a header that still decodes; if reading a row against the
// wrong base happened to agree with the right one, the test above would pass
// on a broken header.
func TestEhFrameHdrGateDetectsAWrongBase(t *testing.T) {
	_, _, u, _, m := layoutWithEh(t, ehSrc(ehTestFDEs, 0))
	_, right := decodeHdr(t, u.Hdr, m.EhHdr)
	// pcrel, the encoding the eh_frame_ptr field uses, measured per row from
	// the row's own position — the mistake this guards against.
	for i := range right {
		row := u.Hdr[12+8*i:]
		at := m.EhHdr + uint64(12+8*i)
		wrong := fdeAt{
			fn:    uint64(int64(at) + int64(int32(binary.LittleEndian.Uint32(row)))),
			entry: uint64(int64(at+4) + int64(int32(binary.LittleEndian.Uint32(row[4:])))),
		}
		if wrong == right[i] {
			t.Fatalf("row %d decodes identically datarel and pcrel (%#x), so TestEhFrameHdrTableMatchesFDEs cannot tell the bases apart", i, wrong.fn)
		}
	}
}

// TestEhFrameHdrEncodingMatchesLd pins the four encoding bytes against the
// linker rather than the DWARF spec's prose, which leaves every one of them
// open: an unwinder reads what ld conventionally produces, so agreeing with
// the spec and disagreeing with ld unwinds nothing.
func TestEhFrameHdrEncodingMatchesLd(t *testing.T) {
	gcc, err := exec.LookPath("x86_64-linux-gnu-gcc")
	if err != nil {
		if gcc, err = exec.LookPath("gcc"); err != nil {
			t.Skip("no x86-64 gcc to link an oracle with")
		}
		// A gcc named `gcc` on an aarch64 host is an aarch64 gcc.
		if out, err := exec.Command(gcc, "-dumpmachine").Output(); err != nil || len(out) < 6 || string(out[:6]) != "x86_64" {
			t.Skip("the available gcc does not target x86-64")
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "p.c")
	bin := filepath.Join(dir, "p")
	if err := os.WriteFile(src, []byte("int f(int n){return n<=1?n:f(n-1)+f(n-2);}\nint main(void){return f(3);}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(gcc, "-O1", "-static", "-Wl,--eh-frame-hdr", "-o", bin, src).CombinedOutput(); err != nil {
		t.Skipf("cannot link an oracle: %v\n%s", err, out)
	}
	img, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	var ld []byte
	for _, s := range segments(t, img) {
		if s.typ == ptGNUEhFrame {
			ld = img[s.off : s.off+4]
		}
	}
	if ld == nil {
		t.Skip("the oracle binary carries no PT_GNU_EH_FRAME")
	}
	_, _, u, _, _ := layoutWithEh(t, ehSrc(ehTestFDEs, 0))
	for i, name := range []string{"version", "eh_frame_ptr_enc", "fde_count_enc", "table_enc"} {
		if u.Hdr[i] != ld[i] {
			t.Errorf(".eh_frame_hdr %s = %#x, ld emits %#x", name, u.Hdr[i], ld[i])
		}
	}
}

// TestUnwindImagesAreAllOrNothing pins that a half-populated Unwind places
// nothing: a header describing an absent .eh_frame would send an unwinder to
// whatever follows .text.
func TestUnwindImagesAreAllOrNothing(t *testing.T) {
	text := []byte{0x90, 0x90, 0x90, 0x90}
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	for _, u := range []elf.Unwind{{Hdr: []byte{1, 0x1b, 3, 0x3b, 0, 0, 0, 0, 0, 0, 0, 0}}, {Frame: []byte{4, 0, 0, 0, 0, 0, 0, 0}}} {
		img := elf.StaticExecutableDataX86WXEhFrame(text, u, data)
		for _, s := range segments(t, img) {
			if s.typ == ptGNUEhFrame {
				t.Errorf("Unwind{Hdr:%d, Frame:%d} produced a PT_GNU_EH_FRAME anyway", len(u.Hdr), len(u.Frame))
			}
		}
	}
}
