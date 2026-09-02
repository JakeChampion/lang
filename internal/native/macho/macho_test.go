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
	return StaticExecutable(text, nil, data, "fern-test", nil)
}

// buildWithSyms assembles a two-function program and wraps it in a Mach-O with
// an LC_SYMTAB (the -g path), mirroring cmd/fern's linkNativeDarwin: lay out
// against the syms-inclusive addresses, then emit the symbol table.
func buildWithSyms(t *testing.T, data []byte) ([]byte, []Sym) {
	t.Helper()
	// Lskip is a Mach-O temporary label — the emitters' local-label prefix on
	// this target — and must not become a symbol: under -g every one of them
	// would otherwise read as a function to lldb, nm, and the FDE checks.
	asm := ".text\n_main:\n\tmov x0, #42\n\tbl helper\n\tb Lskip\nLskip:\n\tmov x16, #1\n\tsvc #0x80\nhelper:\n\tret\n"
	a, err := nativearm64.ParseProgram(asm)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	textLen, dataLen := a.MachOTextLen(), a.MachODataLen()
	if len(data) > 0 {
		dataLen = len(data)
	}
	m := SegmentMap(textLen, 0, dataLen, true)
	text, rodata, err := a.LinkMachO(m.Text, m.Data)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if len(data) > 0 {
		rodata = data
	}
	syms := FuncSyms(a.TextLabelVAddrs(m.Text), m.Text+uint64(len(text)))
	return StaticExecutableSyms(text, nil, rodata, "fern-test", syms, nil), syms
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
	if _, leaked := got["Lskip"]; leaked || len(got) != 2 {
		t.Errorf("symbol table is %v, want exactly _main and helper — an L-prefixed label is Mach-O's temporary symbol, not a function", got)
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

// hasLoad is findLoad's non-fatal twin, for asserting a command is ABSENT.
func hasLoad(bin []byte, cmd uint32) bool {
	ncmds := binary.LittleEndian.Uint32(bin[16:])
	off := 32
	for i := uint32(0); i < ncmds; i++ {
		if binary.LittleEndian.Uint32(bin[off:]) == cmd {
			return true
		}
		off += int(binary.LittleEndian.Uint32(bin[off+4:]))
	}
	return false
}

// The entry (LC_MAIN entryoff) must point at the first text instruction.
func TestMachOEntryPoint(t *testing.T) {
	bin := buildExit(t, nil)
	// LC_MAIN carries a FILE offset from the start of the mach header, not a
	// VM address — dyld adds the load address itself, which is what lets the
	// image slide. So the check is against __text's file offset, not its addr.
	body := findLoad(t, bin, lcMain)
	entryoff := binary.LittleEndian.Uint64(body)
	f, _ := macho.NewFile(bytes.NewReader(bin))
	textSec := f.Section("__text")
	if entryoff != uint64(textSec.Offset) {
		t.Errorf("LC_MAIN entryoff = %#x, want __text file offset %#x", entryoff, textSec.Offset)
	}
}

// TestMachODyldCommandSet pins the load commands Apple Silicon requires of a
// main executable. Each was added because its absence was a HARD launch
// failure, and the failures are silent in different ways, so the set is worth
// asserting as a set:
//   - MH_PIE: without it the kernel SIGKILLs at exec (exit 137) before dyld
//     runs at all. ld64 cannot even produce a non-PIE arm64 executable
//     ("-no_pie ignored for arm64*"), which is the tell.
//   - LC_LOAD_DYLINKER + LC_MAIN: a static, dyld-free LC_UNIXTHREAD image is
//     rejected the same way — verified by building one with Apple's own ld64,
//     which is killed identically. Every arm64 main executable is dyld-loaded.
//   - LC_SYMTAB alongside LC_DYSYMTAB: dyld errors "LC_DYSYMTAB but no
//     LC_SYMTAB load command" and aborts (134).
//   - LC_BUILD_VERSION: names the platform.
func TestMachODyldCommandSet(t *testing.T) {
	bin := buildExit(t, nil)
	flags := binary.LittleEndian.Uint32(bin[24:])
	if flags&mhPIE == 0 {
		t.Errorf("header flags %#x missing MH_PIE — the kernel will refuse to exec this", flags)
	}
	for _, lc := range []struct {
		cmd  uint32
		name string
	}{
		{lcLoadDylinker, "LC_LOAD_DYLINKER"},
		{lcLoadDylib, "LC_LOAD_DYLIB"},
		{lcMain, "LC_MAIN"},
		{lcSymtab, "LC_SYMTAB"},
		{lcDysymtab, "LC_DYSYMTAB"},
		{lcBuildVersion, "LC_BUILD_VERSION"},
	} {
		findLoad(t, bin, lc.cmd) // fatals when absent
	}
	if hasLoad(bin, lcUnixThread) {
		t.Error("LC_UNIXTHREAD is still emitted; it is what made the image unlaunchable")
	}
}

// The strongest check we can run off-Apple-Silicon: recompute the
// CodeDirectory's page hashes exactly as the kernel does and confirm they
// match. A mismatch is precisely what would make the kernel kill the
// process at launch.
func TestMachOCodeSignatureSelfConsistent(t *testing.T) {
	checkSignature(t, buildExit(t, nil))
}

// checkSignature re-hashes every page under the code directory's codeLimit
// and compares with the slots the signature carries.
func checkSignature(t *testing.T, bin []byte) {
	t.Helper()
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

// cfiAsm is a two-function program carrying the frame-pointer rules the
// arm64 emitter produces, with the literal pool `.ltorg` puts between the
// `.cfi_endproc` and the next function.
const cfiAsm = ".text\n_main:\n\t.cfi_startproc\n" +
	"\tstp x29, x30, [sp, #-16]!\n\t.cfi_def_cfa_offset 16\n" +
	"\t.cfi_offset x29, -16\n\t.cfi_offset x30, -8\n" +
	"\tmov x29, sp\n\t.cfi_def_cfa_register x29\n" +
	"\tbl helper\n\tmov x16, #1\n\tsvc #0x80\n" +
	"\tldp x29, x30, [sp], #16\n\t.cfi_def_cfa sp, 0\n\tret\n\t.cfi_endproc\n" +
	"\t.ltorg\n" +
	"helper:\n\t.cfi_startproc\n\tmov x0, #42\n\tret\n\t.cfi_endproc\n"

// buildWithCFI runs the three-step layout cmd/fern's linkNativeDarwin runs:
// the map with a placeholder unwind length fixes the code and __eh_frame
// addresses, the image is rendered there, and the real length places __DATA.
func buildWithCFI(t *testing.T, data []byte, syms bool) (bin []byte, eh []byte, m ImageMap) {
	t.Helper()
	a, err := nativearm64.ParseProgram(cfiAsm)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !a.HasCFI() {
		t.Fatal("the fixture recorded no CFI")
	}
	textLen, dataLen := a.MachOTextLen(), a.MachODataLen()
	if len(data) > 0 {
		dataLen = len(data)
	}
	m = SegmentMap(textLen, 1, dataLen, syms)
	if eh, err = a.MachOEhFrame(m.Text, m.EhFrame); err != nil {
		t.Fatalf("eh_frame: %v", err)
	}
	m = SegmentMap(textLen, len(eh), dataLen, syms)
	text, rodata, err := a.LinkMachO(m.Text, m.Data)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if len(data) > 0 {
		rodata = data
	}
	if syms {
		fs := FuncSyms(a.TextLabelVAddrs(m.Text), m.Text+uint64(len(text)))
		return StaticExecutableSyms(text, eh, rodata, "fern-test", fs, nil), eh, m
	}
	return StaticExecutable(text, eh, rodata, "fern-test", nil), eh, m
}

// TestMachOEhFrame is the container half of #7901 on Darwin: the __eh_frame
// the assembler rendered lands in __TEXT at the address it was rendered for,
// so its pcrel FDE pointers name the functions, and nothing else in the image
// moved off the map the assembler resolved against.
func TestMachOEhFrame(t *testing.T) {
	for _, syms := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "syms"}[syms], func(t *testing.T) {
			bin, eh, m := buildWithCFI(t, bytes.Repeat([]byte{0xAA}, 64), syms)
			f, err := macho.NewFile(bytes.NewReader(bin))
			if err != nil {
				t.Fatalf("debug/macho cannot parse output: %v", err)
			}
			sec := f.Section("__eh_frame")
			if sec == nil {
				t.Fatal("no __eh_frame section — the unwind data was rendered and then dropped")
			}
			if sec.Seg != "__TEXT" {
				t.Errorf("__eh_frame is in %s, want __TEXT: _dyld_find_unwind_sections looks for it there", sec.Seg)
			}
			if sec.Addr != m.EhFrame || sec.Addr%8 != 0 {
				t.Errorf("__eh_frame at %#x, want the %#x it was rendered for (8-aligned)", sec.Addr, m.EhFrame)
			}
			got, err := sec.Data()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, eh) {
				t.Error("__eh_frame bytes differ from the rendered image")
			}
			text := f.Section("__text")
			if text == nil || text.Addr != m.Text {
				t.Fatalf("__text at %#x, want %#x", text.Addr, m.Text)
			}
			if sec.Addr < text.Addr+text.Size {
				t.Errorf("__eh_frame at %#x overlaps __text ending at %#x", sec.Addr, text.Addr+text.Size)
			}
			if d := f.Segment("__DATA"); d == nil || d.Addr != m.Data {
				t.Errorf("__DATA is not at the %#x the assembler resolved against", m.Data)
			}
			// Each FDE's 8-byte pcrel initial_location resolves to a function
			// start: _main at the first code byte, helper after main's seven
			// instructions and the (empty) pool.
			starts := map[uint64]bool{}
			for off := 0; off+4 <= len(got); {
				n := int(binary.LittleEndian.Uint32(got[off:]))
				if n == 0 {
					break
				}
				body := got[off+4 : off+4+n]
				if binary.LittleEndian.Uint32(body) != 0 {
					field := sec.Addr + uint64(off) + 8
					starts[uint64(int64(field)+int64(binary.LittleEndian.Uint64(body[4:])))] = true
				}
				off += 4 + n
			}
			if !starts[m.Text] || !starts[m.Text+7*4] || len(starts) != 2 {
				t.Errorf("FDEs describe functions at %v, want _main at %#x and helper at %#x", starts, m.Text, m.Text+28)
			}
			// The signature covers __eh_frame too, or the kernel refuses the
			// image at exec time with nothing to say about why.
			checkSignature(t, bin)
		})
	}
}

// TestMachOEhFrameShiftsTheMap pins the reason SegmentMap takes the unwind
// length at all: the extra section header moves the first code byte, so a
// layout computed without it resolves every adrp against the wrong page.
func TestMachOEhFrameShiftsTheMap(t *testing.T) {
	without := SegmentMap(64, 0, 0, false)
	with := SegmentMap(64, 32, 0, false)
	if with.Text != without.Text+sectLen {
		t.Errorf("code moved from %#x to %#x, want exactly one section header (%d bytes) later", without.Text, with.Text, sectLen)
	}
	if with.EhFrame != alignUp8(with.Text+64) {
		t.Errorf("__eh_frame at %#x, want 8-aligned right after the 64 bytes of code at %#x", with.EhFrame, with.Text)
	}
	if without.EhFrame != 0 {
		t.Errorf("an image with no unwind data reports an __eh_frame address %#x", without.EhFrame)
	}
	// The code and __eh_frame addresses do not depend on the unwind length,
	// which is what lets the image be rendered before its size is known.
	if again := SegmentMap(64, 4000, 0, false); again.Text != with.Text || again.EhFrame != with.EhFrame {
		t.Errorf("code/__eh_frame moved with the unwind length: %#x/%#x vs %#x/%#x", again.Text, again.EhFrame, with.Text, with.EhFrame)
	}
}

func alignUp8(v uint64) uint64 { return (v + 7) &^ 7 }
