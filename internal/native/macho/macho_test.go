package macho

import (
	"bytes"
	"crypto/sha256"
	"debug/macho"
	"encoding/binary"
	"testing"

	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
)

// buildExit assembles a tiny darwin-style program (exit with code 42) and
// wraps it in a Mach-O executable.
func buildExit(t *testing.T, data []byte) []byte {
	t.Helper()
	asm := ".text\n_main:\n\tmov x0, #42\n\tmov x16, #1\n\tsvc #0x80\n"
	text, _, err := nativearm64.AssembleProgram(asm, baseVAddr)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return StaticExecutable(text, []byte("ro\x00"), data, "fern-test")
}

func TestMachOStructure(t *testing.T) {
	bin := buildExit(t, nil)
	f, err := macho.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("debug/macho cannot parse output: %v", err)
	}
	if f.Type != macho.TypeExec {
		t.Errorf("filetype = %v, want MH_EXECUTE", f.Type)
	}
	if f.Cpu != macho.CpuArm64 {
		t.Errorf("cpu = %v, want arm64", f.Cpu)
	}
	want := map[string]bool{"__PAGEZERO": false, "__TEXT": false, "__LINKEDIT": false}
	for _, l := range f.Loads {
		if s, ok := l.(*macho.Segment); ok {
			if _, tracked := want[s.Name]; tracked {
				want[s.Name] = true
			}
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing segment %s", name)
		}
	}
	if f.Section("__text") == nil || f.Section("__const") == nil {
		t.Errorf("missing __text/__const sections")
	}
}

func TestMachOWithData(t *testing.T) {
	bin := buildExit(t, bytes.Repeat([]byte{0xAA}, 64))
	f, err := macho.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Segment("__DATA") == nil {
		t.Errorf("expected a __DATA segment when data is present")
	}
}

// findLoad walks the raw load commands and returns the body of the first
// command with the given cmd id (body excludes the 8-byte cmd/cmdsize).
func findLoad(t *testing.T, bin []byte, cmd uint32) []byte {
	t.Helper()
	ncmds := binary.LittleEndian.Uint32(bin[16:])
	off := 32
	for i := uint32(0); i < ncmds; i++ {
		c := binary.LittleEndian.Uint32(bin[off:])
		sz := binary.LittleEndian.Uint32(bin[off+4:])
		if c == cmd {
			return bin[off+8 : off+int(sz)]
		}
		off += int(sz)
	}
	t.Fatalf("load command 0x%x not found", cmd)
	return nil
}

// The entry (LC_UNIXTHREAD pc) must point at the first text instruction.
func TestMachOEntryPoint(t *testing.T) {
	bin := buildExit(t, nil)
	body := findLoad(t, bin, lcUnixThread)
	// body: flavor(4) count(4) then arm_thread_state64; pc is the 33rd u64.
	pc := binary.LittleEndian.Uint64(body[8+32*8:])
	f, _ := macho.NewFile(bytes.NewReader(bin))
	textSec := f.Section("__text")
	if pc != textSec.Addr {
		t.Errorf("entry pc = %#x, want __text addr %#x", pc, textSec.Addr)
	}
}

// The strongest check we can run off-Apple-Silicon: recompute the
// CodeDirectory's page hashes exactly as the kernel does and confirm they
// match. A mismatch is precisely what would make the kernel kill the
// process at launch.
func TestMachOCodeSignatureSelfConsistent(t *testing.T) {
	bin := buildExit(t, nil)
	cmd := findLoad(t, bin, lcCodeSignature)
	dataoff := binary.LittleEndian.Uint32(cmd[0:])
	datasize := binary.LittleEndian.Uint32(cmd[4:])
	sig := bin[dataoff : dataoff+datasize]

	be := binary.BigEndian
	if be.Uint32(sig[0:]) != csSuperBlobMagic {
		t.Fatalf("bad SuperBlob magic %#x", be.Uint32(sig[0:]))
	}
	if be.Uint32(sig[8:]) != 1 {
		t.Fatalf("expected exactly one sub-blob")
	}
	cdOff := be.Uint32(sig[16:])
	cd := sig[cdOff:]
	if be.Uint32(cd[0:]) != csCodeDirMagic {
		t.Fatalf("bad CodeDirectory magic %#x", be.Uint32(cd[0:]))
	}
	hashOffset := be.Uint32(cd[16:])
	nCodeSlots := be.Uint32(cd[28:])
	codeLimit := be.Uint32(cd[32:])
	hashSize := cd[36]

	if int(codeLimit) != int(dataoff) {
		t.Errorf("codeLimit %d != signature offset %d", codeLimit, dataoff)
	}
	if hashSize != csHashSize {
		t.Fatalf("hashSize %d", hashSize)
	}
	content := bin[:codeLimit]
	for i := uint32(0); i < nCodeSlots; i++ {
		start := int(i) * csPageSizeBytes
		end := start + csPageSizeBytes
		if end > len(content) {
			end = len(content)
		}
		want := sha256.Sum256(content[start:end])
		got := cd[hashOffset+i*uint32(hashSize) : hashOffset+(i+1)*uint32(hashSize)]
		if !bytes.Equal(want[:], got) {
			t.Errorf("page %d hash mismatch", i)
		}
	}
}
