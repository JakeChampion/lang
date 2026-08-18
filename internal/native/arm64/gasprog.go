package arm64

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/native/gasstr"
)

// AssembleProgram is the section-aware assembler: like Assemble, but it
// also handles a .rodata data section and symbol addressing (adrp +
// add #:lo12:sym), so programs can reference read-only constants and
// strings. .text is laid out at textVAddr and .rodata immediately
// after (8-byte aligned); the returned blobs are meant to sit in one
// R+X segment (see elf.StaticExecutableData).
//
// Supported in .rodata / .data: .byte/.2byte/.4byte/.8byte (and the
// .hword/.word/.quad/etc. aliases), .ascii/.asciz/.string, and
// .balign/.align. .data is materialised into the same blob as .rodata —
// nothing assembled here writes to it, and the emitter lays the two out in
// source order, so symbol addresses agree either way. .bss is accumulated
// separately so it can be laid out last and left out of the file.
const (
	secText = iota
	secRodata
	secIgnore // a section we don't materialise (e.g. .note.GNU-stack)
)

func AssembleProgram(src string, textVAddr uint64) (text, rodata []byte, err error) {
	a, err := ParseProgram(src)
	if err != nil {
		return nil, nil, err
	}
	return a.BytesProgram(textVAddr)
}

// AssembleProgramWX is AssembleProgram for the W^X two-segment ELF layout
// (elf.StaticExecutableDataWX): .rodata is page-aligned into a separate
// R+W segment instead of laid contiguously after .text. Pass
// elf.TextVAddrWX as textVAddr.
func AssembleProgramWX(src string, textVAddr uint64) (text, rodata []byte, err error) {
	a, err := ParseProgram(src)
	if err != nil {
		return nil, nil, err
	}
	return a.BytesProgramWX(textVAddr)
}

// AssembleProgramWXSyms is AssembleProgramWX that also returns every .text
// label resolved to its absolute virtual address — the function-symbol table
// the ELF writer emits into .symtab under `-g`. Pass elf.TextVAddrWX.
func AssembleProgramWXSyms(src string, textVAddr uint64) (text, rodata []byte, syms map[string]uint64, locRows []LineRow, err error) {
	a, err := ParseProgram(src)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	text, rodata, err = a.BytesProgramWX(textVAddr)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return text, rodata, a.TextLabelVAddrs(textVAddr), a.locRows, nil
}

// AssembleProgramWXEntry is AssembleProgramWX that also resolves the byte
// offset of an entry symbol within .text (for
// elf.StaticExecutableDataWXEntry's e_entry). The Go arm64 backend emits
// `_start` as the first instruction, so the entry-0 default suffices
// there — but the SELF-HOST arm64 emitter declares `.globl _start` up
// top while defining the label after other functions, so its binaries
// need the real offset or they start executing mid-function.
func AssembleProgramWXEntry(src string, textVAddr uint64, entry string) (text, rodata []byte, entryOff uint64, err error) {
	a, err := ParseProgram(src)
	if err != nil {
		return nil, nil, 0, err
	}
	text, rodata, err = a.BytesProgramWX(textVAddr)
	if err != nil {
		return nil, nil, 0, err
	}
	v, ok := a.TextLabelVAddr(entry, textVAddr)
	if !ok {
		return nil, nil, 0, fmt.Errorf("entry symbol %q is not a defined .text label", entry)
	}
	return text, rodata, v - textVAddr, nil
}

// AssembleProgramPIE is AssembleProgram for a static position-independent
// executable (elf.StaticPieExecutable): the W^X layout laid out from a
// load base of 0, returning the R_AARCH64_RELATIVE relocations for the
// `.quad <symbol>` slots. Pass elf.TextVAddrPIE as textVAddr.
func AssembleProgramPIE(src string, textVAddr uint64) (text, rodata []byte, relocs []Reloc, err error) {
	a, err := ParseProgram(src)
	if err != nil {
		return nil, nil, nil, err
	}
	return a.BytesProgramPIE(textVAddr)
}

// AssembleProgramShared assembles for a shared object (.so): the same
// base-0 PIE layout, also resolving each name in exportNames to its
// load-base-relative virtual address (textVAddr + its .text offset) in
// exportVAddr — the addresses elf.SharedLibrary records in .dynsym. Pass
// elf.TextVAddrPIE as textVAddr.
func AssembleProgramShared(src string, textVAddr uint64, exportNames []string) (text, rodata []byte, relocs []Reloc, exportVAddr map[string]uint64, err error) {
	a, err := ParseProgram(src)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	text, rodata, relocs, err = a.BytesProgramPIE(textVAddr)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	exportVAddr = map[string]uint64{}
	for _, n := range exportNames {
		v, ok := a.TextLabelVAddr(n, textVAddr)
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("export %q is not a defined .text symbol", n)
		}
		exportVAddr[n] = v
	}
	return text, rodata, relocs, exportVAddr, nil
}

// ParseProgram parses and encodes the program (instructions + data
// directives + symbol/relocation bookkeeping) but does not resolve
// vaddr-dependent fixups. The caller finishes with BytesProgram (ELF,
// contiguous .rodata) or LinkMachO (Mach-O, separate __DATA segment).
// It accepts both ELF (`:lo12:`, `.rodata`/`.bss`) and Mach-O
// (`@PAGE`/`@PAGEOFF`, `__TEXT,__const` / `__DATA,__bss`) assembly syntax.
func ParseProgram(src string) (*Assembler, error) {
	a := NewAssembler()
	sec := secText
	for lineno, raw := range strings.Split(src, "\n") {
		line := stripComment(raw)
		for {
			label, rest, ok := splitLabel(line)
			if !ok {
				break
			}
			switch sec {
			case secText:
				a.TextLabel(label)
			case secRodata:
				a.RodataLabel(label)
			}
			line = strings.TrimSpace(rest)
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ".") {
			newSec, derr := handleProgDirective(a, line, sec)
			if derr != nil {
				return nil, fmt.Errorf("line %d: %q: %w", lineno+1, strings.TrimSpace(raw), derr)
			}
			sec = newSec
			continue
		}
		switch sec {
		case secText:
			if derr := assembleProgInsn(a, line); derr != nil {
				return nil, fmt.Errorf("line %d: %q: %w", lineno+1, strings.TrimSpace(raw), derr)
			}
		case secRodata:
			return nil, fmt.Errorf("line %d: %q: unexpected non-directive in data section", lineno+1, strings.TrimSpace(raw))
		case secIgnore:
			// dropped
		}
	}
	return a, nil
}

// handleProgDirective handles section switches and .rodata data
// directives. Returns the section in effect after the directive.
func handleProgDirective(a *Assembler, line string, sec int) (int, error) {
	fields := strings.Fields(line)
	d := fields[0]
	switch d {
	case ".text":
		a.SetBssSection(false)
		return secText, nil
	case ".ltorg":
		// Flush the literal pool here (off the execution path).
		a.FlushLiterals()
		return sec, nil
	case ".rodata", ".data":
		a.SetBssSection(false)
		return secRodata, nil
	case ".bss":
		// Zero-initialised data, accumulated apart from .rodata so it can
		// be laid out last and left out of the file (#6928).
		a.SetBssSection(true)
		return secRodata, nil
	case ".file":
		// DWARF `.file` directive (-g); emits no bytes.
		return sec, nil
	case ".loc":
		// DWARF `.loc <file> <line> [<col>]` line marker (-g). It emits no
		// bytes, so len(a.insns)*4 is the byte offset of the next
		// instruction — the address this source line begins at.
		if sec == secText && len(fields) >= 3 {
			if ln, err := strconv.Atoi(fields[2]); err == nil {
				a.locRows = append(a.locRows, LineRow{Offset: len(a.insns) * 4, Line: ln})
			}
		}
		return sec, nil
	case ".section":
		// e.g. ".section .rodata" / ".section .bss" / ".section .text" /
		// ".section .note.GNU-stack,...". Mach-O variants name a segment
		// and section: ".section __TEXT,__text" / "__TEXT,__const" /
		// "__DATA,__bss" / "__DATA,__data". Anything else we don't
		// materialise.
		arg := ""
		if len(fields) > 1 {
			arg = fields[1]
		}
		switch {
		case strings.Contains(arg, ".text"), strings.Contains(arg, "__text"):
			a.SetBssSection(false)
			return secText, nil
		case strings.Contains(arg, ".bss"), strings.Contains(arg, "__bss"):
			a.SetBssSection(true)
			return secRodata, nil
		case strings.Contains(arg, ".rodata"), strings.Contains(arg, ".data"),
			strings.Contains(arg, "__const"), strings.Contains(arg, "__cstring"),
			strings.Contains(arg, "__data"):
			a.SetBssSection(false)
			return secRodata, nil
		default:
			return secIgnore, nil
		}
	}
	switch sec {
	case secText:
		return secText, handleDirective(line)
	case secRodata:
		return secRodata, appendRodataDirective(a, d, strings.TrimSpace(strings.TrimPrefix(line, d)))
	default: // secIgnore: drop directives too
		return secIgnore, nil
	}
}

func appendRodataDirective(a *Assembler, d, rest string) error {
	switch d {
	case ".byte":
		return emitInts(a, rest, 1)
	case ".2byte", ".hword", ".short", ".half":
		return emitInts(a, rest, 2)
	case ".4byte", ".word", ".long":
		return emitInts(a, rest, 4)
	case ".8byte", ".xword", ".quad", ".dword":
		return emitInts(a, rest, 8)
	case ".double", ".dc.d":
		return emitDoubles(a, rest)
	case ".float", ".single", ".dc.s":
		return emitFloats(a, rest)
	case ".space", ".skip":
		// N zero bytes (used for .bss reservations like the freelist).
		n, err := strconv.Atoi(strings.Fields(rest)[0])
		if err != nil {
			return fmt.Errorf("bad .space/.skip size %q", rest)
		}
		a.AppendRodata(make([]byte, n))
		return nil
	case ".ascii", ".asciz", ".string":
		s, err := gasstr.Unquote(strings.TrimSpace(rest))
		if err != nil {
			return fmt.Errorf("bad string literal: %v", err)
		}
		a.AppendRodata([]byte(s))
		if d != ".ascii" {
			a.AppendRodata([]byte{0})
		}
		return nil
	case ".balign", ".align", ".p2align":
		n, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return fmt.Errorf("bad alignment %q", rest)
		}
		// On AArch64, .align and .p2align take a power-of-two exponent
		// (".align 2" => align to 4 bytes); .balign takes a byte count.
		if d != ".balign" {
			n = 1 << n
		}
		a.AlignRodata(n)
		return nil
	case ".arch", ".global", ".globl", ".type", ".size", ".ltorg":
		return nil
	default:
		return fmt.Errorf("unsupported .rodata directive %q", d)
	}
}

// emitDoubles appends comma-separated f64 values as 8-byte
// little-endian IEEE-754 fields (`.double`).
func emitDoubles(a *Assembler, rest string) error {
	for _, tok := range strings.Split(rest, ",") {
		tok = strings.TrimSpace(tok)
		f, err := strconv.ParseFloat(tok, 64)
		if err != nil {
			return fmt.Errorf("bad double %q", tok)
		}
		uv := math.Float64bits(f)
		b := make([]byte, 8)
		for i := 0; i < 8; i++ {
			b[i] = byte(uv >> (8 * i))
		}
		a.AppendRodata(b)
	}
	return nil
}

// emitFloats appends comma-separated f32 values as 4-byte
// little-endian IEEE-754 fields (`.float`).
func emitFloats(a *Assembler, rest string) error {
	for _, tok := range strings.Split(rest, ",") {
		tok = strings.TrimSpace(tok)
		f, err := strconv.ParseFloat(tok, 32)
		if err != nil {
			return fmt.Errorf("bad float %q", tok)
		}
		uv := math.Float32bits(float32(f))
		b := make([]byte, 4)
		for i := 0; i < 4; i++ {
			b[i] = byte(uv >> (8 * i))
		}
		a.AppendRodata(b)
	}
	return nil
}

// emitInts appends comma-separated integer values as width-byte
// little-endian fields.
func emitInts(a *Assembler, rest string, width int) error {
	for _, tok := range strings.Split(rest, ",") {
		tok = strings.TrimSpace(tok)
		v, err := strconv.ParseInt(tok, 0, 64)
		if err != nil {
			// allow unsigned hex like 0x80000000 that overflows int64-as-signed
			u, uerr := strconv.ParseUint(tok, 0, 64)
			if uerr != nil {
				// A `.quad <symbol>` slot: emit the symbol's absolute
				// 8-byte address (function-pointer / closure tables).
				if width == 8 && isIdent(tok) {
					if a.InBssSection() {
						return fmt.Errorf(".bss cannot hold the address of %q: it is not zero", tok)
					}
					a.AppendQuadSym(tok)
					continue
				}
				return fmt.Errorf("bad integer %q", tok)
			}
			v = int64(u)
		}
		b := make([]byte, width)
		uv := uint64(v)
		for i := 0; i < width; i++ {
			b[i] = byte(uv >> (8 * i))
		}
		a.AppendRodata(b)
	}
	return nil
}

// assembleProgInsn dispatches .text instructions, intercepting the
// symbol-addressing forms (adrp, add #:lo12:sym) before delegating the
// rest to the shared instruction assembler.
func assembleProgInsn(a *Assembler, line string) error {
	mnem, rest := splitMnemonic(line)
	ops := splitOperands(rest)
	switch {
	case mnem == "ldr" && len(ops) == 2 && strings.HasPrefix(ops[1], "="):
		rt, err := parseReg(ops[0])
		if err != nil {
			return err
		}
		valStr := strings.TrimPrefix(ops[1], "=")
		val, err := strconv.ParseUint(valStr, 0, 64)
		if err != nil {
			// Negative literals (e.g. `ldr x0, =-1`): parse as signed and
			// reinterpret. For a w-register the low 32 bits are used.
			sv, serr := strconv.ParseInt(valStr, 0, 64)
			if serr != nil {
				return fmt.Errorf("bad literal %q", ops[1])
			}
			val = uint64(sv)
		}
		a.LDRLiteral(rt, val, !is32(ops[0]))
		return nil
	case mnem == "adrp":
		if len(ops) != 2 {
			return fmt.Errorf("adrp expects Xd, sym")
		}
		rd, err := parseReg(ops[0])
		if err != nil {
			return err
		}
		// ELF `adrp Xd, sym` and Mach-O `adrp Xd, sym@PAGE` are the same
		// relocation (the high 21 bits of the symbol's page address).
		a.ADRPsym(rd, strings.TrimSuffix(ops[1], "@PAGE"))
		return nil
	case mnem == "add" && len(ops) == 3 && (isLo12(ops[2]) || strings.HasSuffix(ops[2], "@PAGEOFF")):
		rd, err := parseReg(ops[0])
		if err != nil {
			return err
		}
		rn, err := parseReg(ops[1])
		if err != nil {
			return err
		}
		// `:lo12:sym` (ELF) and `sym@PAGEOFF` (Mach-O) both name the low
		// 12 bits of the symbol address.
		sym := lo12Sym(ops[2])
		if strings.HasSuffix(ops[2], "@PAGEOFF") {
			sym = strings.TrimSuffix(ops[2], "@PAGEOFF")
		}
		a.AddLo12(rd, rn, sym)
		return nil
	}
	return assembleInsn(a, line)
}

// isLo12 reports whether an operand is a ":lo12:sym" relocation (with
// or without a leading '#').
func isLo12(op string) bool {
	return strings.Contains(op, ":lo12:")
}

// lo12Sym extracts the symbol name from a ":lo12:sym" operand.
func lo12Sym(op string) string {
	i := strings.LastIndex(op, ":")
	return strings.TrimSpace(op[i+1:])
}
