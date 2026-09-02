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
