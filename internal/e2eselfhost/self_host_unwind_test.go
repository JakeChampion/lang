package e2eselfhost

import (
	goelf "debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostUnwindData is the self-host end of #7901: a binary the
// self-host compiler built carries the unwind data its emitters describe,
// placed where a runtime unwinder finds it. That means PT_GNU_EH_FRAME over
// an .eh_frame_hdr whose eh_frame_ptr resolves to the .eh_frame, and one FDE
// per user function starting exactly at the function's symbol — the symbol
// table (-g) being the oracle, by a different path from the CFI offsets.
//
// Both Linux targets, structurally, on any host: nothing here runs the
// binary. The bytes themselves are pinned to native's, and through it to
// gas, by TestSelfHostCfiMatchesNative.
func TestSelfHostUnwindData(t *testing.T) {
	gcc, _ := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(dir, "p.fern")
	if err := os.WriteFile(src, []byte(
		"function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }\n"+
			"function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); }\n"+
			"function main(): i32 { return fib(9) + fact(1); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdlib := langSrcAbs(t, "internal/stdlib")

	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, backend := range []string{"", "-ssa"} {
			name := target
			if backend != "" {
				name += backend
			}
			t.Run(name, func(t *testing.T) {
				out := filepath.Join(dir, strings.ReplaceAll(name, "-", "_")+".bin")
				args := []string{"-g", "-target", target}
				if backend != "" {
					args = append(args, backend)
				}
				args = append(args, "-o", out, src, stdlib)
				if b, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
					t.Fatalf("fern-selfhost %v: %v\n%s", args, err, b)
				}
				img, err := os.ReadFile(out)
				if err != nil {
					t.Fatal(err)
				}
				f, err := goelf.NewFile(strings.NewReader(string(img)))
				if err != nil {
					t.Fatal(err)
				}
				var hdrSeg *goelf.Prog
				for _, p := range f.Progs {
					if p.Type == goelf.PT_GNU_EH_FRAME {
						hdrSeg = p
					}
				}
				if hdrSeg == nil {
					t.Fatal("no PT_GNU_EH_FRAME — a runtime unwinder cannot find the .eh_frame however complete it is")
				}
				hdr := img[hdrSeg.Off : hdrSeg.Off+hdrSeg.Filesz]
				if got := string(hdr[:4]); got != "\x01\x1b\x03\x3b" {
					t.Fatalf(".eh_frame_hdr opens % x, want 01 1b 03 3b", got)
				}
				// File offsets equal vaddr offsets from the base in these images.
				base := f.Progs[0].Vaddr - f.Progs[0].Off
				ehVAddr := uint64(int64(hdrSeg.Vaddr+4) + int64(int32(binary.LittleEndian.Uint32(hdr[4:]))))
				ehOff := ehVAddr - base
				n := int(binary.LittleEndian.Uint32(hdr[8:]))
				if n == 0 || uint64(len(hdr)) != uint64(12+8*n) {
					t.Fatalf(".eh_frame_hdr is %d bytes for %d FDEs", len(hdr), n)
				}
				// Walk .eh_frame: [len][id]; id 0 is the CIE; a zero length ends it.
				fdes := map[uint64]uint64{}
				for off := ehOff; ; {
					ln := uint64(binary.LittleEndian.Uint32(img[off:]))
					if ln == 0 {
						break
					}
					body := img[off+4 : off+4+ln]
					if binary.LittleEndian.Uint32(body) != 0 {
						field := base + off + 8
						start := uint64(int64(field) + int64(int32(binary.LittleEndian.Uint32(body[4:]))))
						fdes[start] = start + uint64(binary.LittleEndian.Uint32(body[8:]))
					}
					off += 4 + ln
				}
				if len(fdes) != n {
					t.Errorf(".eh_frame holds %d FDEs, the header table says %d", len(fdes), n)
				}
				syms, err := f.Symbols()
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				for _, s := range syms {
					if !strings.HasPrefix(s.Name, "__fn_") {
						continue
					}
					want++
					end, ok := fdes[s.Value]
					if !ok {
						t.Errorf("%s at %#x has no FDE — unwinding stops there", s.Name, s.Value)
						continue
					}
					// An FDE ends at .cfi_endproc; the arm64 literal pool sits
					// between that and the next symbol, so the symbol's extent
					// bounds it rather than equalling it.
					if end <= s.Value || (s.Size > 0 && end > s.Value+s.Size) {
						t.Errorf("%s [%#x,%#x): its FDE ends at %#x", s.Name, s.Value, s.Value+s.Size, end)
					}
				}
				if want != 3 {
					t.Fatalf("found %d user functions in the symbol table, want 3", want)
				}
			})
		}
	}
}
