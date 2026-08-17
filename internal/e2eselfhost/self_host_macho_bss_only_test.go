package e2eselfhost

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// machoSegment is one LC_SEGMENT_64 load command's addresses and sizes.
type machoSegment struct {
	Name                              string
	VMAddr, VMSize, FileOff, FileSize uint64
}

// machoSegments parses the LC_SEGMENT_64 load commands out of a 64-bit
// little-endian Mach-O. Only what this test needs: enough of the header to walk
// the command list, and the four numbers per segment.
func machoSegments(t *testing.T, path string) []machoSegment {
	t.Helper()
	d, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(d) < 32 {
		t.Fatalf("%s: %d bytes, too short to be a Mach-O", path, len(d))
	}
	if got := binary.LittleEndian.Uint32(d); got != 0xFEEDFACF {
		t.Fatalf("%s: magic 0x%08x, want 0xFEEDFACF (64-bit little-endian Mach-O)", path, got)
	}
	ncmds := binary.LittleEndian.Uint32(d[16:])
	var out []machoSegment
	off := uint32(32)
	for i := uint32(0); i < ncmds; i++ {
		if int(off)+8 > len(d) {
			t.Fatalf("%s: load command %d runs past EOF", path, i)
		}
		cmd := binary.LittleEndian.Uint32(d[off:])
		cmdsize := binary.LittleEndian.Uint32(d[off+4:])
		if cmdsize == 0 {
			t.Fatalf("%s: load command %d has size 0", path, i)
		}
		if cmd == 0x19 { // LC_SEGMENT_64
			name := string(d[off+8 : off+24])
			for len(name) > 0 && name[len(name)-1] == 0 {
				name = name[:len(name)-1]
			}
			out = append(out, machoSegment{
				Name:     name,
				VMAddr:   binary.LittleEndian.Uint64(d[off+24:]),
				VMSize:   binary.LittleEndian.Uint64(d[off+32:]),
				FileOff:  binary.LittleEndian.Uint64(d[off+40:]),
				FileSize: binary.LittleEndian.Uint64(d[off+48:]),
			})
		}
		off += cmdsize
	}
	return out
}

// TestSelfHostArm64DarwinBssOnlyImage pins the __DATA segment for an image whose
// only writable storage is zero-init bss.
//
// macho_executable decided `has_data` from `data.len() > 0` alone, so an image
// with bss but no initialized data got NO __DATA segment — and every .bss symbol
// then resolved to an address in no mapped segment. `__fern_scratch` is the one
// that bites: the syscall helpers stage their timeval/timespec structs there, so
// the first write faults.
//
// Nothing hit it while the arm64 emitter emitted its f64 constant pool
// unconditionally, because those 37 doubles were themselves the initialized data
// that turned `has_data` on — the pool was accidentally backing an unrelated
// section. Gating the pool on its need (#2649) made bss-only images ordinary and
// segfaulted now_unix_ms and sleep_ms on the Apple Silicon lane.
//
// This asserts the container on ANY host, which is the point: the lane that
// executes arm64-darwin output is Apple-Silicon-only, so a Linux-side gate is
// what keeps this from regressing invisibly again. Both halves are checked from
// one build — a `has_data` rule that fired the wrong way changes exactly one of
// them.
func TestSelfHostArm64DarwinBssOnlyImage(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		t.Skip("cross-emit container check; the native lane runs TestSelfHostArm64DarwinBuilds instead")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	build := func(t *testing.T, name, src string) string {
		t.Helper()
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src+"\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		binPath := filepath.Join(dir, name+".bin")
		if out, err := exec.Command(fernBin, "-target", "arm64-darwin", "-o", binPath, srcPath).CombinedOutput(); err != nil {
			t.Fatalf("compile %s: %v\n%s", name, err, out)
		}
		return binPath
	}

	find := func(segs []machoSegment, name string) *machoSegment {
		for i := range segs {
			if segs[i].Name == name {
				return &segs[i]
			}
		}
		return nil
	}

	// now_unix_ms stages a timeval in __fern_scratch (.bss) and has no
	// initialized data of its own — the exact shape that lost its segment.
	t.Run("bss_only_gets_DATA", func(t *testing.T) {
		bin := build(t, "bssonly", `function main(): i32 { var t: i64 = now_unix_ms(); if (t > 1700000000000) { return 7; } return 1; }`)
		segs := machoSegments(t, bin)
		data := find(segs, "__DATA")
		if data == nil {
			var names []string
			for _, s := range segs {
				names = append(names, s.Name)
			}
			t.Fatalf("no __DATA segment: %v — every .bss symbol resolves outside any mapped segment, so the first write to __fern_scratch faults", names)
		}
		// Zero-init bss occupies vmsize, not filesize: the kernel zero-fills the
		// difference. A __DATA whose vmsize did not exceed its filesize would
		// mean the bss got no pages even though the segment exists.
		if data.VMSize <= data.FileSize {
			t.Errorf("__DATA vmsize=0x%x filesize=0x%x — vmsize must exceed filesize to cover the zero-init bss", data.VMSize, data.FileSize)
		}
		// __LINKEDIT must start after __DATA in the address space, or __DATA's
		// pages overlap it.
		if le := find(segs, "__LINKEDIT"); le != nil && le.VMAddr < data.VMAddr+data.VMSize {
			t.Errorf("__LINKEDIT vmaddr=0x%x overlaps __DATA [0x%x, 0x%x)", le.VMAddr, data.VMAddr, data.VMAddr+data.VMSize)
		}
	})

	// The other half: an image with neither data nor bss must still not grow a
	// __DATA segment, so the rule is "needs writable storage", not "always on".
	t.Run("no_data_no_bss_has_no_DATA", func(t *testing.T) {
		bin := build(t, "nodata", `function main(): i32 { return 2 + 3 * 4 - 1; }`)
		segs := machoSegments(t, bin)
		if data := find(segs, "__DATA"); data != nil {
			t.Errorf("__DATA emitted (vmsize=0x%x) for an image with no data and no bss", data.VMSize)
		}
	})
}
