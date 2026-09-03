package arm64_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// gnuAsLinkedText assembles src with GNU as and LINKS it at textVAddr, then
// extracts the raw .text bytes.
//
// Linking rather than gnuAsText's assemble-only path, because an adrp /
// :lo12: pair is a RELOCATION in an object file: gas leaves the immediate
// fields zero and records the fixup for the linker. Comparing against the
// object would compare two placeholders and pass no matter what the
// assembler computed.
func gnuAsLinkedText(t *testing.T, as, objcopy, src string, textVAddr uint64) []byte {
	t.Helper()
	ld, err := exec.LookPath("aarch64-linux-gnu-ld")
	if err != nil {
		t.Skip("aarch64-linux-gnu-ld not on PATH")
	}
	dir := t.TempDir()
	sPath := filepath.Join(dir, "in.s")
	oPath := filepath.Join(dir, "in.o")
	exePath := filepath.Join(dir, "in.elf")
	binPath := filepath.Join(dir, "in.bin")
	if err := os.WriteFile(sPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(as, sPath, "-o", oPath).CombinedOutput(); err != nil {
		t.Fatalf("as: %v\n%s", err, out)
	}
	if out, err := exec.Command(ld, "-e", "_start", "-Ttext", hexAddr(textVAddr), oPath, "-o", exePath).CombinedOutput(); err != nil {
		t.Fatalf("ld: %v\n%s", err, out)
	}
	if out, err := exec.Command(objcopy, "-O", "binary", "--only-section=.text", exePath, binPath).CombinedOutput(); err != nil {
		t.Fatalf("objcopy: %v\n%s", err, out)
	}
	b, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hexAddr(v uint64) string { return "0x" + strconv.FormatUint(v, 16) }

// symAddrSrc is an adrp/:lo12: pair against a real .rodata symbol — the
// exact sequence internal/codegen/arm64's adrpAdd emits for every static
// data reference on ELF.
const symAddrSrc = `.text
_start:
	adrp x0, msg
	add x0, x0, :lo12:msg
	adr x1, _start
	ret
.section .rodata
msg:
	.asciz "hi"
`

// TestSymbolAddressingMatchesGNUAs pins the adrp / adr / :lo12: text forms
// against gas over a laid-out image.
//
// The encoders (ADRPsym, ADRsym, AddLo12) have been here since the arm64
// backend was written, reached only through the direct API. Neither
// mnemonic was in the text dispatch, so `arm64.AssembleProgram` could not
// read back the assembly its own code generator writes — the encoder was
// present and the vocabulary was not, the same shape as #8071.
func TestSymbolAddressingMatchesGNUAs(t *testing.T) {
	as, objcopy := findBinutils(t)
	const base = 0x400000
	text, _, err := arm64.AssembleProgram(symAddrSrc, base)
	if err != nil {
		t.Fatalf("AssembleProgram: %v", err)
	}
	// gas leaves adrp/:lo12: as relocations in an object file, so compare
	// against a linked image: `as` then a link at the same base.
	want := gnuAsLinkedText(t, as, objcopy, symAddrSrc, base)
	if !bytes.Equal(text, want) {
		t.Errorf("symbol addressing differs\ngot  % x\nwant % x", text, want)
	}
}

// TestDarwinSymbolAddressingSpellings pins that the Mach-O spellings name
// the same relocations as the ELF ones. Apple's assembler requires
// `@PAGE` / `@PAGEOFF` and GNU as rejects them, so they cannot be checked
// against gas directly — what is checked is that they encode identically
// to the ELF spelling of the same pair.
func TestDarwinSymbolAddressingSpellings(t *testing.T) {
	const base = 0x400000
	elf, _, err := arm64.AssembleProgram(symAddrSrc, base)
	if err != nil {
		t.Fatalf("ELF spelling: %v", err)
	}
	darwin := strings.NewReplacer(
		"adrp x0, msg", "adrp x0, msg@PAGE",
		"add x0, x0, :lo12:msg", "add x0, x0, msg@PAGEOFF",
	).Replace(symAddrSrc)
	got, _, err := arm64.AssembleProgram(darwin, base)
	if err != nil {
		t.Fatalf("Mach-O spelling: %v", err)
	}
	if !bytes.Equal(got, elf) {
		t.Errorf("@PAGE/@PAGEOFF encodes differently from :lo12:\ngot  % x\nwant % x", got, elf)
	}
}

// TestBytesRefusesUnresolvedSymbols holds the hazard the adrp vocabulary
// would otherwise have opened.
//
// Bytes has no address map, so it cannot resolve a symbol reference — but
// it used to emit the instruction with its placeholder zero still in place
// and return no error. `adrp x0, nosuchsym` came back as `adrp x0, #0`,
// silently, for a symbol that does not exist anywhere. Reaching those
// encoders from text is what would have made that reachable from ordinary
// assembly, so the refusal lands with the vocabulary.
func TestBytesRefusesUnresolvedSymbols(t *testing.T) {
	for _, src := range []string{
		"\tadrp x0, nosuchsym\n\tret\n",
		"\tadr x0, nosuchsym\n\tret\n",
		"\tadrp x0, nosuchsym\n\tadd x0, x0, :lo12:nosuchsym\n\tret\n",
	} {
		got, err := arm64.Assemble(src)
		if err == nil {
			t.Errorf("Assemble(%q) returned % x and no error; want a refusal", src, got)
			continue
		}
		if !strings.Contains(err.Error(), "BytesProgram") {
			t.Errorf("Assemble(%q): %v — the error should name the entry point that can resolve them", src, err)
		}
	}
}

// TestSymbolAddressingRejectsBadShapes keeps the new vocabulary from
// accepting more than the forms it names.
func TestSymbolAddressingRejectsBadShapes(t *testing.T) {
	for _, src := range []string{
		"\tadrp x0\n",
		"\tadrp x0, msg, x1\n",
		"\tadrp #4, msg\n",
		"\tsub x0, x0, :lo12:msg\n",
		"\tadds x0, x0, :lo12:msg\n",
	} {
		if _, _, err := arm64.AssembleProgram(".text\n"+src+"msg:\n\tret\n", 0x400000); err == nil {
			t.Errorf("AssembleProgram(%q) accepted it; want a refusal", src)
		}
	}
}
