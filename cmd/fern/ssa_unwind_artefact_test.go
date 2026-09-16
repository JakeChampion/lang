package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestSSABackendsCarryUnwindData is the artefact gate on `-backend ssa`, and
// the one that would have caught #9495 and #9500 at the source: both SSA
// backends shipped binaries with no `.eh_frame` at all while the 348-program
// corpus differential reported 328 of 328 agreeing. That differential compares
// what programs DO. Nothing compared what the artefact CARRIES.
//
// It cannot be a backend dimension on TestEveryUserFunctionHasAnFDE, whose
// oracle is the `-g` symbol table: SSA refuses `-g` until it emits
// `.debug_line` (#9493). So this reads the image alone — which is enough to
// separate "unwind data present, mapped and reachable" from "absent", the
// distinction that was missed.
func TestSSABackendsCarryUnwindData(t *testing.T) {
	for _, tc := range []struct {
		target string
		// The CIE the target's profile declares, as internal/native/cfi
		// writes it: version 1, "zR", then code alignment, data alignment
		// (-8 as the LEB 0x78) and the return-address column.
		cie string
	}{
		{"x86-64-linux", "\x01zR\x00\x01\x78\x10\x01"},
		{"arm64-linux", "\x01zR\x00\x04\x78\x1e\x01"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "p.fern")
			const prog = `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }
function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); }
function main(): i32 { return fib(9) + fact(1); }`
			if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "p")
			if code, err := run(src, out, tc.target, "ssa", "", "", false, true, "", false, false, false, nil, false, "", false, nil); err != nil || code != 0 {
				t.Fatalf("build: code=%d err=%v", code, err)
			}
			img, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}

			off, ok := findCIE(img)
			if !ok {
				t.Fatal("no .eh_frame CIE in the -backend ssa image: nothing can walk out of a frame in a binary this backend produced")
			}
			if got := string(img[off+8 : off+16]); got != tc.cie {
				t.Errorf("CIE header is % x, want % x — the emitter and the target's profile disagree", got, tc.cie)
			}
			// Alloc: unwinding happens at runtime, so the R+X segment has to
			// cover the section.
			codeOff, codeSz, ok := firstLoad(img)
			if !ok {
				t.Fatal("no PT_LOAD in the image")
			}
			if uint64(off) < codeOff || uint64(off) >= codeOff+codeSz {
				t.Errorf(".eh_frame at %#x is outside the R+X segment [%#x, %#x) — an unwinder would read unmapped memory", off, codeOff, codeOff+codeSz)
			}

			// Reachable: a running program finds .eh_frame only through
			// PT_GNU_EH_FRAME. Without it the unwind data is complete, mapped
			// and unreachable from inside the process.
			hdrOff, hdrSz, ok := ehFrameHdrSeg(img)
			if !ok {
				t.Fatal("no PT_GNU_EH_FRAME in the -backend ssa image: a backtrace stops at the first frame however complete the CFI is")
			}
			hdr := img[hdrOff : hdrOff+hdrSz]
			if got := string(hdr[:4]); got != "\x01\x1b\x03\x3b" {
				t.Fatalf(".eh_frame_hdr opens % x, want 01 1b 03 3b", got)
			}
			n := int(binary.LittleEndian.Uint32(hdr[8:]))
			if n == 0 {
				t.Fatal("the search table is empty, so the image carries FDEs nothing can find")
			}
			// The three functions the fixture declares each need one, and the
			// runtime helpers this backend lifts bring more, so the floor is
			// what is checked rather than the exact count.
			if n < 3 {
				t.Errorf("%d FDEs for a program with 3 functions", n)
			}
			if want := uint64(12 + 8*n); hdrSz != want {
				t.Errorf(".eh_frame_hdr is %d bytes for %d FDEs, want %d", hdrSz, n, want)
			}
			// Every row has to name a function in the R+X segment and an FDE
			// at or after the CIE; a row pointing elsewhere unwinds with
			// whatever bytes happen to be there.
			const base = 0x400000
			hdrVAddr := base + hdrOff
			for i := 0; i < n; i++ {
				row := hdr[12+8*i:]
				fn := uint64(int64(hdrVAddr) + int64(int32(binary.LittleEndian.Uint32(row))))
				fde := uint64(int64(hdrVAddr) + int64(int32(binary.LittleEndian.Uint32(row[4:]))))
				if fn < base+codeOff || fn >= base+codeOff+codeSz {
					t.Errorf("row %d names a function at %#x, outside the R+X segment", i, fn)
				}
				if fde < base+uint64(off) || fde >= base+codeOff+codeSz {
					t.Errorf("row %d names an FDE at %#x, outside .eh_frame", i, fde)
				}
			}
		})
	}
}
