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
	return StaticExecutable(text, data, "fern-test")
}

// buildWithSyms assembles a two-function program and wraps it in a Mach-O with
// an LC_SYMTAB (the -g path), mirroring cmd/fern's linkNativeDarwin: lay out
// against the syms-inclusive addresses, then emit the symbol table.
func buildWithSyms(t *testing.T, data []byte) ([]byte, []Sym) {
	t.Helper()
	asm := ".text\n_main:\n\tmov x0, #42\n\tbl helper\n\tmov x16, #1\n\tsvc #0x80\nhelper:\n\tret\n"
	a, err := nativearm64.ParseProgram(asm)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	textLen, dataLen := a.MachOTextLen(), a.MachODataLen()
	if len(data) > 0 {
		dataLen = len(data)
	}
	textVAddr, dataVAddr := SegmentAddrsSyms(textLen, dataLen)
	text, rodata, err := a.LinkMachO(textVAddr, dataVAddr)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if len(data) > 0 {
		rodata = data
	}
	syms := FuncSyms(a.TextLabelVAddrs(textVAddr), textVAddr+uint64(len(text)))
	return StaticExecutableSyms(text, rodata, "fern-test", syms), syms
}

// TestMachOSymtab guards the -g static symbol table (#5537 slice 1 for
// arm64-darwin): debug/macho parses the LC_SYMTAB and every function name
// resolves to its __text address, so lldb / nm / a backtrace can symbolicate.
func TestMachOSymtab(t *testing.T) {
	bin, _ := buildWithSyms(t, nil)
	f, err := macho.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("debug/macho cannot parse output: %v", err)
	}
	if f.Symtab == nil {
		t.Fatal("no LC_SYMTAB in output")
	}
	got := map[string]uint64{}
	for _, s := range f.Symtab.Syms {
		got[s.Name] = s.Value
	}
	for _, name := range []string{"_main", "helper"} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing symbol %q (have %v)", name, got)
		}
	}
	// _main is the first text label → the entry / __text section address.
	if textSec := f.Section("__text"); textSec != nil && got["_main"] != textSec.Addr {
		t.Errorf("_main = %#x, want __text addr %#x", got["_main"], textSec.Addr)
	}
	// helper sits after _main.
	if got["helper"] <= got["_main"] {
		t.Errorf("helper %#x should follow _main %#x", got["helper"], got["_main"])
	}
}

// TestMachOSymtabSigned confirms the code signature stays self-consistent with
// the symtab in place: codeLimit moves past the symtab/strtab to the signature
// offset, and every page hash (now covering the symbol table too) matches.
func TestMachOSymtabSigned(t *testing.T) {
	bin, _ := buildWithSyms(t, nil)
	cmd := findLoad(t, bin, lcCodeSignature)
	dataoff := binary.LittleEndian.Uint32(cmd[0:])
	datasize := binary.LittleEndian.Uint32(cmd[4:])
	sig := bin[dataoff : dataoff+datasize]
	be := binary.BigEndian
	cdOff := be.Uint32(sig[16:])
	cd := sig[cdOff:]
	hashOffset := be.Uint32(cd[16:])
	nCodeSlots := be.Uint32(cd[28:])
	codeLimit := be.Uint32(cd[32:])
	if int(codeLimit) != int(dataoff) {
		t.Errorf("codeLimit %d != signature offset %d", codeLimit, dataoff)
	}
	// The symtab load command points inside [textEnd, codeLimit).
	st := findLoad(t, bin, lcSymtab)
	symoff := binary.LittleEndian.Uint32(st[0:])
	if symoff >= codeLimit {
		t.Errorf("symoff %d not before codeLimit %d (must be hashed)", symoff, codeLimit)
	}
	content := bin[:codeLimit]
	for i := uint32(0); i < nCodeSlots; i++ {
		start := int(i) * csPageSizeBytes
		end := start + csPageSizeBytes
		if end > len(content) {
			end = len(content)
		}
		want := sha256.Sum256(content[start:end])
		got := cd[hashOffset+i*csHashSize : hashOffset+(i+1)*csHashSize]
		if !bytes.Equal(want[:], got) {
			t.Errorf("page %d hash mismatch", i)
		}
	}
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
	if f.Section("__text") == nil {
		t.Errorf("missing __text section")
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
