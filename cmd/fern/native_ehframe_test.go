package main

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestNativeLinkPlacesEhFrame is the end of the chain #7901 builds: the
// emitter emits `.cfi_*`, the assembler renders them, and the IN-PROCESS
// linker places the result — no gcc anywhere. Until this was wired, the
// default path recorded the unwind data and dropped it on the floor, so the
// only binaries carrying CFI were the ones built through `-cc gcc`.
//
// The image has no section headers, so readelf cannot decode it; the check
// walks the ELF instead. What matters is that .eh_frame exists, that it is
// covered by the R+X PT_LOAD (it is alloc — unwinding happens at runtime),
// and that the program still runs.
func TestNativeLinkPlacesEhFrame(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("runs the produced x86-64 Linux binary directly")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "p.fern")
	const prog = `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }
function main(): i32 { return fib(9); }`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "p")
	if code, err := run(src, out, "x86-64-linux", "", "", "", false, true, "", false, false, false, nil, false, "", false, nil); err != nil || code != 0 {
		t.Fatalf("build: code=%d err=%v", code, err)
	}

	cmd := exec.Command(out)
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != 34 {
		t.Errorf("built program exited %d, want 34 (fib(9))", got)
	}

	img, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	off, ok := findCIE(img)
	if !ok {
		t.Fatal("no .eh_frame CIE in the linked image — the in-process linker is discarding the unwind data the emitter produced")
	}
	// The CIE gas emits for this profile, and the one internal/native/x86_64
	// declares: version 1, augmentation "zR", code align 1, data align -8,
	// return-address column 16.
	if got := img[off+8 : off+16]; string(got) != "\x01zR\x00\x01\x78\x10\x01" {
		t.Errorf("CIE header is % x, want 01 7a 52 00 01 78 10 01", got)
	}
	// Alloc: the R+X segment must cover it.
	codeOff, codeSz, ok := firstLoad(img)
	if !ok {
		t.Fatal("no PT_LOAD in the image")
	}
	if uint64(off) < codeOff || uint64(off) >= codeOff+codeSz {
		t.Errorf(".eh_frame at %#x is outside the R+X segment [%#x, %#x) — an unwinder would read unmapped memory", off, codeOff, codeOff+codeSz)
	}

	// The discoverability half. A running program's unwinder reaches
	// .eh_frame only through PT_GNU_EH_FRAME: it walks the program headers
	// with dl_iterate_phdr and reads .eh_frame's address out of the segment
	// that header covers. Nothing scans for the section, so up to here the
	// unwind data is complete, mapped, and unreachable from inside the
	// process.
	hdrOff, hdrSz, ok := ehFrameHdrSeg(img)
	if !ok {
		t.Fatal("no PT_GNU_EH_FRAME in the linked image — a backtrace from inside this program stops at the first frame however complete the CFI is")
	}
	hdr := img[hdrOff : hdrOff+hdrSz]
	if got := hdr[:4]; string(got) != "\x01\x1b\x03\x3b" {
		t.Fatalf(".eh_frame_hdr opens % x, want 01 1b 03 3b", got)
	}
	// File offsets equal vaddr offsets from baseVAddr in this image, so an
	// address is its offset plus the load base.
	const base = 0x400000
	hdrVAddr := base + hdrOff
	ptr := uint64(int64(hdrVAddr+4) + int64(int32(binary.LittleEndian.Uint32(hdr[4:]))))
	if want := base + uint64(off); ptr != want {
		t.Errorf("eh_frame_ptr resolves to %#x, want the .eh_frame at %#x", ptr, want)
	}
	n := int(binary.LittleEndian.Uint32(hdr[8:]))
	if n == 0 {
		t.Fatal("the search table is empty, so the program carries FDEs nothing can find")
	}
	if want := uint64(12 + 8*n); hdrSz != want {
		t.Errorf(".eh_frame_hdr is %d bytes for %d FDEs, want %d", hdrSz, n, want)
	}
	for i := 0; i < n; i++ {
		row := hdr[12+8*i:]
		fn := uint64(int64(hdrVAddr) + int64(int32(binary.LittleEndian.Uint32(row))))
		fde := uint64(int64(hdrVAddr) + int64(int32(binary.LittleEndian.Uint32(row[4:]))))
		if fn < base+codeOff || fn >= base+codeOff+codeSz {
			t.Errorf("row %d names a function at %#x, outside the R+X segment", i, fn)
		}
		if fde < ptr || fde >= base+codeOff+codeSz {
			t.Errorf("row %d names an FDE at %#x, outside .eh_frame", i, fde)
		}
	}
}

// ehFrameHdrSeg returns the file offset and size of the PT_GNU_EH_FRAME
// segment — the .eh_frame_hdr an unwinder reads.
func ehFrameHdrSeg(img []byte) (off, size uint64, ok bool) {
	if len(img) < 64 {
		return 0, 0, false
	}
	phoff := binary.LittleEndian.Uint64(img[32:])
	phnum := int(binary.LittleEndian.Uint16(img[56:]))
	for i := 0; i < phnum; i++ {
		p := img[phoff+uint64(i)*56:]
		if binary.LittleEndian.Uint32(p) == 0x6474e550 { // PT_GNU_EH_FRAME
			return binary.LittleEndian.Uint64(p[8:]), binary.LittleEndian.Uint64(p[32:]), true
		}
	}
	return 0, 0, false
}

// findCIE locates the first .eh_frame CIE by its fixed prefix: a length, a
// zero CIE id, version 1, then the "zR" augmentation string.
func findCIE(img []byte) (int, bool) {
	for i := 0; i+16 < len(img); i += 4 {
		if binary.LittleEndian.Uint32(img[i+4:]) == 0 && img[i+8] == 1 &&
			img[i+9] == 'z' && img[i+10] == 'R' && img[i+11] == 0 &&
			binary.LittleEndian.Uint32(img[i:]) != 0 {
			return i, true
		}
	}
	return 0, false
}

// firstLoad returns the file offset and size of the first PT_LOAD.
func firstLoad(img []byte) (off, size uint64, ok bool) {
	if len(img) < 64 {
		return 0, 0, false
	}
	phoff := binary.LittleEndian.Uint64(img[32:])
	phnum := int(binary.LittleEndian.Uint16(img[56:]))
	for i := 0; i < phnum; i++ {
		p := img[phoff+uint64(i)*56:]
		if binary.LittleEndian.Uint32(p) == 1 { // PT_LOAD
			return binary.LittleEndian.Uint64(p[8:]), binary.LittleEndian.Uint64(p[32:]), true
		}
	}
	return 0, 0, false
}
