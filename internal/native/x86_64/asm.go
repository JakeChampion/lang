// Package x86_64 is a pure-Go assembler for the subset of x86-64 the
// Fern code generator (internal/codegen/x86_64) emits, in Intel syntax
// (`.intel_syntax noprefix`). It is the x86-64 counterpart of
// internal/native/arm64: AssembleProgram turns the emitted `.s` text
// into a .text blob (plus .rodata) ready to drop into a static ELF-64
// executable via internal/native/elf.StaticExecutableDataX86 — no
// external assembler or linker.
//
// Covered so far: the integer / control-flow / call surface, plus
// .rodata/.bss data, rip-relative addressing (to data and function
// symbols), .quad symbol pointer tables, indirect call/jmp, the rep
// string ops (movs/stos/cmps), and SSE scalar floats (movq/movd
// GPR<->xmm transfers, add/sub/mul/div/sqrt sd/ss, ucomis/comis,
// cvtsi2s*/cvtts*2si conversions, movap*, roundsd) — enough to assemble
// and run the whole fixture corpus (recursion, strings, maps,
// closures/higher-order functions, json, enums, floating-point math),
// and the x87 FPU transcendentals (fsin/fcos/fyl2x/f2xm1/fscale/frndint
// + the x87 stack/arith ops) that sin/cos/exp/log/pow lower to. This
// covers the full instruction surface the code generator emits; an
// unsupported instruction surfaces as a clear error rather than a
// miscompile.
package x86_64

import (
	"fmt"
	"strconv"
	"strings"
)

// Operand kinds.
const (
	opReg = iota
	opImm
	opMem
	opLabel
	opSt // x87 stack register st(i)
)

type operand struct {
	kind int
	reg  int   // register number 0..15
	size int   // operand size in bits: 8, 16, 32, 64
	imm  int64 // immediate value
	// memory operand [base + index*scale + disp]:
	base     int // base register number, or -1 if none
	index    int // index register number, or -1 if none
	scale    int // 1, 2, 4, or 8
	disp     int64
	memSize  int  // access size in bits from a "qword ptr" prefix, or 0 if unspecified
	highByte bool // ah/ch/dh/bh: an 8-bit reg (4..7) that must NOT carry a REX prefix
	sym      string
}

type relFixup struct {
	at  int    // offset in text of the 4-byte rel32 field
	sym string // target text label
}

// ripFixup records a rip-relative disp32 field (lea/mov [rip+sym]) to be
// resolved against a .rodata label once the section layout is final. `end`
// is the text offset of the END of the containing instruction — the
// runtime RIP the displacement is relative to. It is stamped by the
// assemble loop once the instruction finishes encoding: for most forms
// that's at+4, but mem,imm forms (`add qword ptr [rip+sym], 1`,
// `mov qword ptr [rip+sym], 0`) place the immediate AFTER the disp32, and
// resolving against at+4 pointed the access `immLen` bytes past the
// symbol (the ×256 rc-underflow / leakcheck counter drift and the
// strbuf_take length-reset miss on the in-process-assembled path).
type ripFixup struct {
	at  int
	end int
	sym string
}

// Assembler accumulates encoded machine code and resolves text-label
// branch/call targets in a final pass.
type Assembler struct {
	text         []byte
	rodata       []byte
	textLabels   map[string]int
	rodataLabels map[string]int
	relFixups    []relFixup
	ripFixups    []ripFixup
	quadSyms     []quadSymFixup
	locRows      []LineRow
}

// LineRow is one DWARF .debug_line row: the source line active at a code
// offset within .text, recorded when the code generator emits a `.loc`
// directive under -g (the offset is the next instruction's, since `.loc`
// emits no bytes). The linker converts Offset to an absolute vaddr.
type LineRow struct {
	Offset int
	Line   int
}

// quadSymFixup records a ".quad <symbol>" slot in .rodata (a function- or
// data-pointer table entry) to be filled with the symbol's absolute
// virtual address once layout is final.
type quadSymFixup struct {
	at  int // offset in rodata
	sym string
}

func newAssembler() *Assembler {
	return &Assembler{
		textLabels:   map[string]int{},
		rodataLabels: map[string]int{},
	}
}

func (a *Assembler) emit(b ...byte) { a.text = append(a.text, b...) }

func (a *Assembler) emit32(v uint32) {
	a.emit(byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AssembleProgram assembles the Intel-syntax program text into .text and
// .rodata blobs. textVAddr is where .text will be loaded (used to resolve
// branch targets); pass elf.TextVAddr. .rodata is laid contiguously after
// .text (8-byte aligned), matching the single-segment R+W+X image
// (elf.StaticExecutableDataX86).
func AssembleProgram(src string, textVAddr uint64) (text, rodata []byte, err error) {
	text, rodata, _, _, _, err = assembleProgram(src, textVAddr, false, false, nil)
	return text, rodata, err
}

// AssembleProgramWX is AssembleProgram for the W^X two-segment ELF layout
// (elf.StaticExecutableDataX86WX): .rodata is page-aligned into a separate
// R+W segment instead of laid contiguously after .text, so the code
// segment can be mapped R+X. Pass elf.TextVAddrWX as textVAddr.
func AssembleProgramWX(src string, textVAddr uint64) (text, rodata []byte, err error) {
	text, rodata, _, _, _, err = assembleProgram(src, textVAddr, true, false, nil)
	return text, rodata, err
}

// Reloc is one R_X86_64_RELATIVE entry: at load time `*(base + Offset) =
// base + Addend`. Both fields are relative to a load base of 0, matching
// the ET_DYN image elf.StaticPieExecutableX86 produces. Mirrors
// arm64.Reloc; callers map them onto elf.Reloc.
type Reloc struct {
	Offset uint64
	Addend uint64
}

// AssembleProgramPIE is AssembleProgram for a static position-independent
// executable (elf.StaticPieExecutableX86): the W^X layout laid out from a
// load base of 0, returning the R_X86_64_RELATIVE relocations for the
// `.quad <symbol>` slots. rip-relative code is base-independent and needs
// no relocation. Pass elf.TextVAddrPIE as textVAddr.
func AssembleProgramPIE(src string, textVAddr uint64) (text, rodata []byte, relocs []Reloc, err error) {
	text, rodata, relocs, _, _, err = assembleProgram(src, textVAddr, true, true, nil)
	return text, rodata, relocs, err
}

// AssembleProgramWXSyms is AssembleProgramWX that also returns every .text
// label resolved to its absolute virtual address (textVAddr + offset) — the
// function-symbol table the ELF writer emits into .symtab under `-g`, so a
// debugger / nm / a backtrace can map a code address back to a name. Pass
// elf.TextVAddrWX as textVAddr.
func AssembleProgramWXSyms(src string, textVAddr uint64) (text, rodata []byte, syms map[string]uint64, locRows []LineRow, err error) {
	text, rodata, _, off, rows, err := assembleProgram(src, textVAddr, true, false, nil)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	syms = make(map[string]uint64, len(off))
	for name, o := range off {
		syms[name] = textVAddr + uint64(o)
	}
	return text, rodata, syms, rows, nil
}

// AssembleProgramShared assembles for a shared object (.so): the same
// base-0 PIE layout, but it also resolves each `.text` label in
// exportNames to its load-base-relative virtual address (textVAddr +
// offset) and returns them in exportVAddr — the addresses elf.SharedLibrary
// records in .dynsym so a dynamic loader can resolve the exports. Pass
// elf.TextVAddrPIE as textVAddr.
func AssembleProgramShared(src string, textVAddr uint64, exportNames []string) (text, rodata []byte, relocs []Reloc, exportVAddr map[string]uint64, err error) {
	exportVAddr = map[string]uint64{}
	for _, n := range exportNames {
		exportVAddr[n] = 0 // marker: resolve this label
	}
	text, rodata, relocs, _, _, err = assembleProgram(src, textVAddr, true, true, exportVAddr)
	return text, rodata, relocs, exportVAddr, err
}

// assembleProgram is the shared body of AssembleProgram (wx=false,
// contiguous .rodata), AssembleProgramWX (wx=true, page-aligned .rodata in
// a separate R+W segment), and AssembleProgramPIE (pie=true: page-aligned,
// base-0 layout, returning .quad-slot relocations). When exportVAddr is
// non-nil, each key is resolved to textVAddr + its .text-label offset (for
// AssembleProgramShared).
func assembleProgram(src string, textVAddr uint64, wx, pie bool, exportVAddr map[string]uint64) (text, rodata []byte, relocs []Reloc, syms map[string]int, locRows []LineRow, err error) {
	a := newAssembler()
	sec := "text"
	for lineno, raw := range strings.Split(src, "\n") {
		line := stripComment(raw)
		// Peel any leading labels ("foo:" / ".Lx:").
		for {
			label, rest, ok := splitLabel(line)
			if !ok {
				break
			}
			if sec == "text" {
				a.textLabels[label] = len(a.text)
			} else if sec == "rodata" {
				a.rodataLabels[label] = len(a.rodata)
			}
			line = strings.TrimSpace(rest)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ".") {
			sec, err = a.directive(line, sec)
			if err != nil {
				return nil, nil, nil, nil, nil, fmt.Errorf("line %d: %q: %w", lineno+1, strings.TrimSpace(raw), err)
			}
			continue
		}
		if sec != "text" {
			return nil, nil, nil, nil, nil, fmt.Errorf("line %d: %q: instruction outside .text", lineno+1, strings.TrimSpace(raw))
		}
		nRip := len(a.ripFixups)
		if err := a.insn(line); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("line %d: %q: %w", lineno+1, strings.TrimSpace(raw), err)
		}
		// Stamp every rip fixup this instruction produced with the
		// instruction's end offset — the runtime RIP its disp32 is
		// relative to (see the ripFixup comment).
		for i := nRip; i < len(a.ripFixups); i++ {
			a.ripFixups[i].end = len(a.text)
		}
	}
	// Resolve rel32 branch/call targets now that all labels are placed.
	for _, f := range a.relFixups {
		dst, ok := a.textLabels[f.sym]
		if !ok {
			return nil, nil, nil, nil, nil, fmt.Errorf("undefined label %q", f.sym)
		}
		rel := int32(dst - (f.at + 4))
		putLE32(a.text, f.at, uint32(rel))
	}
	// Resolve rip-relative data references. In the single-segment image
	// StaticExecutableDataX86 pads .text to 8 bytes and appends .rodata,
	// so .rodata begins at align8(len(text)) within the segment. In the
	// W^X / PIE images .rodata moves to the first 4 KiB page boundary past
	// .text (a separate R+W segment), so its segment-relative base is
	// pageUp(textVAddr+len(text)) - textVAddr. Either way rodataBase is the
	// data blob's offset from textVAddr, so the textVAddr base still cancels
	// in (symVAddr - ripEnd).
	rodataBase := align8(len(a.text))
	if wx || pie {
		const page = 0x1000 // must match elf.pageAlignFor(emX86_64) (x86-64 = 4 KiB pages)
		rodataBase = int((textVAddr+uint64(len(a.text))+page-1)&^(page-1) - textVAddr)
	}
	rodataVAddr := textVAddr + uint64(rodataBase)

	// PIE self-relocation symbols the prologue references via [rip+sym].
	// Their base-relative vaddrs MUST match where elf.StaticPieExecutableX86
	// lays the bytes: .rela.dyn is 8-aligned after the data blob, one
	// Elf64_Rela (24 bytes) per `.quad` slot; __ehdr_start is the ELF header
	// at vaddr 0 (so [rip+__ehdr_start] yields the runtime load base).
	var pieSyms map[string]uint64
	if pie {
		relaStart := rodataVAddr + uint64(len(a.rodata))
		if rem := relaStart % 8; rem != 0 {
			relaStart += 8 - rem
		}
		pieSyms = map[string]uint64{
			"__ehdr_start": 0,
			"__rela_start": relaStart,
			"__rela_end":   relaStart + uint64(len(a.quadSyms))*24,
		}
	}

	for _, f := range a.ripFixups {
		var symOff int
		if v, ok := pieSyms[f.sym]; ok {
			// Synthetic base-relative symbol: its offset from .text is
			// (vaddr - textVAddr), so [rip+sym] resolves to base + vaddr.
			symOff = int(v) - int(textVAddr)
		} else if off, ok := a.rodataLabels[f.sym]; ok {
			symOff = rodataBase + off
		} else if off, ok := a.textLabels[f.sym]; ok {
			symOff = off // text symbol: a function address (e.g. a closure body)
		} else {
			return nil, nil, nil, nil, nil, fmt.Errorf("undefined rip-relative symbol %q", f.sym)
		}
		disp := int32(symOff - f.end)
		putLE32(a.text, f.at, uint32(disp))
	}
	// Fill ".quad <symbol>" pointer-table slots. In a PIE these values are
	// base-relative (textVAddr/rodataVAddr are laid out from base 0) and the
	// slot also gets an R_X86_64_RELATIVE entry so the loader adds the base.
	for _, f := range a.quadSyms {
		var abs uint64
		if off, ok := a.textLabels[f.sym]; ok {
			abs = textVAddr + uint64(off)
		} else if off, ok := a.rodataLabels[f.sym]; ok {
			abs = rodataVAddr + uint64(off)
		} else {
			return nil, nil, nil, nil, nil, fmt.Errorf("undefined .quad symbol %q", f.sym)
		}
		for i := 0; i < 8; i++ {
			a.rodata[f.at+i] = byte(abs >> (8 * i))
		}
		if pie {
			relocs = append(relocs, Reloc{Offset: rodataVAddr + uint64(f.at), Addend: abs})
		}
	}
	// Resolve requested export symbols to their load-base-relative vaddrs.
	for name := range exportVAddr {
		off, ok := a.textLabels[name]
		if !ok {
			return nil, nil, nil, nil, nil, fmt.Errorf("export %q is not a defined .text symbol", name)
		}
		exportVAddr[name] = textVAddr + uint64(off)
	}
	return a.text, a.rodata, relocs, a.textLabels, a.locRows, nil
}

func putLE32(b []byte, at int, v uint32) {
	b[at] = byte(v)
	b[at+1] = byte(v >> 8)
	b[at+2] = byte(v >> 16)
	b[at+3] = byte(v >> 24)
}

func align8(n int) int { return (n + 7) &^ 7 }

// directive handles section switches and the no-op metadata directives
// the code generator emits. Returns the section in effect afterwards.
func (a *Assembler) directive(line, sec string) (string, error) {
	fields := strings.Fields(line)
	d := fields[0]
	switch d {
	case ".text":
		return "text", nil
	case ".rodata", ".bss", ".data":
		// .bss / .data are zero-/value-initialised writable globals
		// (allocator cursors, freelist). The single program segment is
		// mapped R+W+X, so they live in the same blob as .rodata.
		return "rodata", nil
	case ".section":
		arg := ""
		if len(fields) > 1 {
			arg = fields[1]
		}
		switch {
		case strings.Contains(arg, ".text"):
			return "text", nil
		case strings.Contains(arg, ".rodata"), strings.Contains(arg, ".bss"), strings.Contains(arg, ".data"):
			return "rodata", nil
		default:
			return "ignore", nil // e.g. .note.GNU-stack
		}
	case ".intel_syntax", ".globl", ".global", ".type", ".size", ".file", ".ident":
		return sec, nil
	case ".loc":
		// `.loc <file> <line> [<col>]` (DWARF line marker, -g). It emits no
		// bytes, so the current text length is the offset of the next
		// instruction — the address this source line begins at.
		if sec == "text" && len(fields) >= 3 {
			if line, err := strconv.Atoi(fields[2]); err == nil {
				a.locRows = append(a.locRows, LineRow{Offset: len(a.text), Line: line})
			}
		}
		return sec, nil
	}
	if sec == "ignore" {
		return "ignore", nil
	}
	if sec == "rodata" {
		return "rodata", a.appendRodataDirective(d, strings.TrimSpace(strings.TrimPrefix(line, d)))
	}
	// In .text, alignment directives are advisory for this flat,
	// single-segment layout (correctness doesn't depend on padding).
	switch d {
	case ".align", ".balign", ".p2align":
		return "text", nil
	}
	return sec, fmt.Errorf("unsupported directive %q", d)
}

// insn parses and encodes one .text instruction.
func (a *Assembler) insn(line string) error {
	mnem, rest := splitMnemonic(line)
	opStrs := splitOperands(rest)
	ops := make([]operand, len(opStrs))
	for i, s := range opStrs {
		o, err := parseOperand(s)
		if err != nil {
			return err
		}
		ops[i] = o
	}
	switch mnem {
	case "ret":
		a.emit(0xC3)
		return nil
	case "syscall":
		a.emit(0x0F, 0x05)
		return nil
	case "ud2":
		a.emit(0x0F, 0x0B)
		return nil
	case "cdq":
		a.emit(0x99)
		return nil
	case "cqo":
		a.emit(0x48, 0x99)
		return nil
	case "cld":
		a.emit(0xFC)
		return nil
	case "rep", "repe", "repz":
		a.emit(0xF3)
		return a.insn(rest)
	case "repne", "repnz":
		a.emit(0xF2)
		return a.insn(rest)
	case "movsb":
		a.emit(0xA4)
		return nil
	case "movsw":
		a.emit(0x66, 0xA5)
		return nil
	case "movsq":
		a.emit(0x48, 0xA5)
		return nil
	case "stosb":
		a.emit(0xAA)
		return nil
	case "stosw":
		a.emit(0x66, 0xAB)
		return nil
	case "stosq":
		a.emit(0x48, 0xAB)
		return nil
	case "cmpsb":
		a.emit(0xA6)
		return nil
	case "cmpsq":
		a.emit(0x48, 0xA7)
		return nil
	case "scasb":
		a.emit(0xAE)
		return nil
	case "lodsb":
		a.emit(0xAC)
		return nil
	case "push":
		return a.pushPop(ops, 0x50)
	case "pop":
		return a.pushPop(ops, 0x58)
	case "mov":
		return a.mov(ops, false)
	case "movabs":
		return a.mov(ops, true)
	case "add":
		return a.alu(ops, 0x00, 0)
	case "or":
		return a.alu(ops, 0x08, 1)
	case "and":
		return a.alu(ops, 0x20, 4)
	case "sub":
		return a.alu(ops, 0x28, 5)
	case "xor":
		return a.alu(ops, 0x30, 6)
	case "cmp":
		return a.alu(ops, 0x38, 7)
	case "test":
		return a.test(ops)
	case "imul":
		return a.imul(ops)
	case "bsf":
		// The scan-forward sibling of bsr. tzcnt below is the same opcode
		// with an F3 prefix; bsf itself was simply never needed until the
		// vector kernels wanted to find the lowest set bit of a pmovmskb
		// mask, and lzcnt/tzcnt cannot substitute — they are BMI1, and on a
		// pre-BMI1 CPU the F3 is IGNORED and the instruction silently
		// behaves as bsr/bsf instead of faulting.
		return a.bitOp(ops, 0xBC)
	case "bsr":
		return a.bitOp(ops, 0xBD)
	case "lzcnt":
		return a.bitOp(ops, 0xBD, 0xF3)
	case "tzcnt":
		return a.bitOp(ops, 0xBC, 0xF3)
	case "popcnt":
		return a.bitOp(ops, 0xB8, 0xF3)
	case "idiv":
		return a.unaryF7(ops, 7)
	case "div":
		return a.unaryF7(ops, 6)
	case "neg":
		return a.unaryF7(ops, 3)
	case "inc":
		return a.incDec(ops, 0)
	case "dec":
		return a.incDec(ops, 1)
	case "sar":
		return a.shift(ops, 7)
	case "shl":
		return a.shift(ops, 4)
	case "shr":
		return a.shift(ops, 5)
	case "lea":
		return a.lea(ops)
	case "movzx":
		return a.movzx(ops, false)
	case "movsx", "movsxd":
		return a.movzx(ops, true)
	case "xchg":
		return a.xchg(ops)
	case "jmp":
		return a.jmp(ops)
	case "call":
		return a.call(ops)
	}
	switch mnem {
	case "movq":
		return a.movqd(ops, false)
	case "movd":
		return a.movqd(ops, true)
	case "movsd":
		return a.movsdss(0xF2, ops)
	case "movss":
		return a.movsdss(0xF3, ops)
	case "cvtsi2sd":
		return a.cvtsi2s(0xF2, ops)
	case "cvtsi2ss":
		return a.cvtsi2s(0xF3, ops)
	case "cvttsd2si":
		return a.cvtt2si(0xF2, 0x2C, ops)
	case "cvttss2si":
		return a.cvtt2si(0xF3, 0x2C, ops)
	case "cvtsd2si":
		return a.cvtt2si(0xF2, 0x2D, ops)
	case "cvtss2si":
		return a.cvtt2si(0xF3, 0x2D, ops)
	case "roundsd":
		return a.roundsd(ops)
	case "pmovmskb":
		return a.pmovmskb(ops)
	case "pshufd":
		return a.pshufd(ops)
	}
	if mnem == "movdqu" || mnem == "movdqa" {
		// Direction is decided by which side is the xmm: `movdqu xmm, mem`
		// is the 0x6F load, `movdqu mem, xmm` the 0x7F store.
		if len(ops) == 2 && (ops[0].kind != opReg || ops[0].size != 128) {
			prefix := byte(0xF3)
			if mnem == "movdqa" {
				prefix = 0x66
			}
			return a.movdqStore(prefix, ops)
		}
	}
	if s, ok := sseOps[mnem]; ok {
		return a.sseOp(s.prefix, s.op, ops)
	}
	if cc, ok := jccCode(mnem); ok {
		return a.jcc(ops, cc)
	}
	if cc, ok := setccCode(mnem); ok {
		return a.setcc(ops, cc)
	}
	if handled, err := a.x87(mnem, ops); handled {
		return err
	}
	return fmt.Errorf("unsupported instruction %q", mnem)
}

// rexFor returns the REX prefix byte (or 0 to omit it) for an instruction
// with the given 64-bit-operand flag, ModRM.reg field, and ModRM.rm /
// SIB.base register. needB8 forces REX when an 8-bit operand uses one of
// spl/bpl/sil/dil (regs 4..7), which require REX to address.
func rexFor(w bool, reg, rm int, needB8 bool) byte {
	var r byte
	if w {
		r |= 0x08
	}
	if reg >= 8 {
		r |= 0x04
	}
	if rm >= 8 {
		r |= 0x01
	}
	if r != 0 || needB8 {
		return 0x40 | r
	}
	return 0
}

// modrmReg encodes a register-direct ModRM byte (mod=11).
func modrmReg(reg, rm int) byte {
	return 0xC0 | byte((reg&7)<<3) | byte(rm&7)
}

// encodeMem appends the ModRM byte (and SIB / displacement as needed) for
// a memory operand addressed as [base + disp], with the given ModRM.reg
// field value.
func (a *Assembler) encodeMem(regField int, m operand) {
	if m.base < 0 && m.sym != "" {
		// rip-relative: ModRM mod=00, rm=101 means [rip + disp32].
		a.emit(byte((regField&7)<<3) | 5)
		a.ripFixups = append(a.ripFixups, ripFixup{at: len(a.text), sym: m.sym})
		a.emit32(0)
		return
	}
	if m.index >= 0 || (m.base&7) == 4 {
		a.encodeMemSIB(regField, m)
		return
	}
	mod, dispBytes := memMod(m)
	a.emit(byte(mod<<6) | byte((regField&7)<<3) | byte(m.base&7))
	a.emitDisp(dispBytes, m.disp)
}

// encodeMemSIB emits a ModRM (rm=100) + SIB byte for [base + index*scale +
// disp] and the displacement. Used whenever there is an index register, or
// the base is rsp/r12 (whose rm=100 encoding mandates a SIB byte).
func (a *Assembler) encodeMemSIB(regField int, m operand) {
	index := m.index
	if index < 0 {
		index = 4 // SIB index=100 means "no index"
	}
	var mod, dispBytes int
	var baseField int
	if m.base < 0 {
		// index-only: SIB base=101 with mod=00 means a disp32 and no base.
		mod, dispBytes, baseField = 0, 4, 5
	} else {
		mod, dispBytes = memMod(m)
		baseField = m.base & 7
	}
	a.emit(byte(mod<<6) | byte((regField&7)<<3) | 4)
	a.emit(byte(scaleBits(m.scale)<<6) | byte((index&7)<<3) | byte(baseField&7))
	a.emitDisp(dispBytes, m.disp)
}

func (a *Assembler) emitDisp(dispBytes int, disp int64) {
	switch dispBytes {
	case 1:
		a.emit(byte(disp))
	case 4:
		a.emit32(uint32(disp))
	}
}

func scaleBits(scale int) int {
	switch scale {
	case 2:
		return 1
	case 4:
		return 2
	case 8:
		return 3
	default:
		return 0 // scale 1 (or unset)
	}
}

// memRex computes the REX prefix for an instruction with a memory operand,
// accounting for base and index register extensions (REX.B / REX.X). needB8
// forces an (otherwise-empty) REX prefix when the register operand is one of
// the 8-bit registers spl/bpl/sil/dil (regs 4..7), which can only be addressed
// with a REX prefix present; without it the same ModRM reg field decodes as
// ah/ch/dh/bh instead.
func memRex(w bool, reg int, m operand, needB8 bool) byte {
	var r byte
	if w {
		r |= 0x08
	}
	if reg >= 8 {
		r |= 0x04
	}
	if m.index >= 8 {
		r |= 0x02
	}
	if m.base >= 8 {
		r |= 0x01
	}
	if r != 0 || needB8 {
		return 0x40 | r
	}
	return 0
}

// memMod picks the ModRM mod field and displacement width for [base+disp].
// rbp/r13 have no disp-less form, so they always carry at least a disp8.
func memMod(m operand) (mod, dispBytes int) {
	rbpLike := (m.base & 7) == 5
	if m.disp == 0 && !rbpLike {
		return 0, 0
	}
	if m.disp >= -128 && m.disp <= 127 {
		return 1, 1
	}
	return 2, 4
}

func (a *Assembler) pushPop(ops []operand, base byte) error {
	if len(ops) != 1 || ops[0].kind != opReg {
		return fmt.Errorf("push/pop expects one register")
	}
	r := ops[0].reg
	if r >= 8 {
		a.emit(0x41) // REX.B
	}
	a.emit(base + byte(r&7))
	return nil
}

func (a *Assembler) mov(ops []operand, abs bool) error {
	if len(ops) != 2 {
		return fmt.Errorf("mov expects two operands")
	}
	dst, src := ops[0], ops[1]
	switch {
	case dst.kind == opReg && src.kind == opImm:
		if abs || dst.size == 64 && !fitsInt32(src.imm) {
			// movabs r64, imm64
			if rex := rexFor(true, 0, dst.reg, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0xB8 + byte(dst.reg&7))
			u := uint64(src.imm)
			for i := 0; i < 8; i++ {
				a.emit(byte(u >> (8 * i)))
			}
			return nil
		}
		if dst.size == 64 {
			// mov r/m64, imm32 (sign-extended): REX.W C7 /0 id
			a.emit(rexFor(true, 0, dst.reg, false))
			a.emit(0xC7)
			a.emit(modrmReg(0, dst.reg))
			a.emit32(uint32(src.imm))
			return nil
		}
		// mov r32, imm32 : B8+rd id
		if rex := rexFor(false, 0, dst.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xB8 + byte(dst.reg&7))
		a.emit32(uint32(src.imm))
		return nil
	case dst.kind == opReg && src.kind == opReg:
		return a.rmReg(0x88, dst, src) // MR form: r/m, r
	case dst.kind == opMem && src.kind == opReg:
		return a.memReg(0x88, src, dst)
	case dst.kind == opReg && src.kind == opMem:
		return a.regMem(0x8A, dst, src)
	case dst.kind == opMem && src.kind == opImm:
		// Honour the operand size from the `byte/word/dword/qword ptr`
		// prefix. Previously this always emitted C7 + imm32 (a 4-byte
		// store), so `mov byte ptr [mem], imm` silently wrote 4 bytes —
		// a 3-byte buffer overrun (e.g. __fern_strcat's 1-byte NUL
		// terminator past a `len+1` allocation, #3544). C6 /0 ib is the
		// byte form; word needs the 0x66 size prefix + imm16.
		switch dst.memSize {
		case 8:
			// mov r/m8, imm8 : [REX] C6 /0 ib
			if rex := memRex(false, 0, dst, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0xC6)
			a.encodeMem(0, dst)
			a.emit(byte(src.imm))
			return nil
		case 16:
			// mov r/m16, imm16 : 66 [REX] C7 /0 iw
			a.emit(0x66)
			if rex := memRex(false, 0, dst, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0xC7)
			a.encodeMem(0, dst)
			a.emit(byte(src.imm), byte(src.imm>>8))
			return nil
		}
		// 32-bit (or unspecified) and 64-bit: C7 /0 id, REX.W for qword.
		w := dst.memSize == 64
		if w {
			a.emit(memRex(true, 0, dst, false))
		} else if rex := memRex(false, 0, dst, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xC7)
		a.encodeMem(0, dst)
		a.emit32(uint32(src.imm))
		return nil
	}
	return fmt.Errorf("unsupported mov form")
}

// rmReg encodes "op r/m, r" (MR) for register-direct operands. opBase is
// the 8-bit opcode; the 32/64-bit opcode is opBase|1.
func (a *Assembler) rmReg(opBase byte, rm, reg operand) error {
	w := reg.size == 64
	op := opBase
	if reg.size != 8 {
		op |= 1
	}
	if rex := rexFor(w, reg.reg, rm.reg, needsRexByte(reg) || needsRexByte(rm)); rex != 0 {
		a.emit(rex)
	}
	a.emit(op)
	a.emit(modrmReg(reg.reg, rm.reg))
	return nil
}

func (a *Assembler) memReg(opBase byte, reg, mem operand) error {
	w := reg.size == 64
	op := opBase
	if reg.size != 8 {
		op |= 1
	}
	if rex := memRex(w, reg.reg, mem, needsRexByte(reg)); rex != 0 {
		a.emit(rex)
	}
	a.emit(op)
	a.encodeMem(reg.reg, mem)
	return nil
}

func (a *Assembler) regMem(opBase byte, reg, mem operand) error {
	w := reg.size == 64
	op := opBase
	if reg.size != 8 {
		op |= 1
	}
	if rex := memRex(w, reg.reg, mem, needsRexByte(reg)); rex != 0 {
		a.emit(rex)
	}
	a.emit(op)
	a.encodeMem(reg.reg, mem)
	return nil
}

// alu encodes add/or/and/sub/xor/cmp. opBase is the family's MR 8-bit
// opcode (add=0x00, …); ext is the /digit for the imm forms.
func (a *Assembler) alu(ops []operand, opBase byte, ext int) error {
	if len(ops) != 2 {
		return fmt.Errorf("binary op expects two operands")
	}
	dst, src := ops[0], ops[1]
	switch {
	case dst.kind == opReg && src.kind == opReg:
		return a.rmReg(opBase, dst, src)
	case dst.kind == opMem && src.kind == opReg:
		return a.memReg(opBase, src, dst)
	case dst.kind == opReg && src.kind == opMem:
		return a.regMem(opBase|0x02, dst, src)
	case dst.kind == opReg && src.kind == opImm:
		// Byte register: <alu> r/m8, imm8 is its own opcode (80 /ext ib) —
		// 83/81 are the 16/32/64-bit forms and would silently widen the
		// operation (the mov-imm encoder had the same bug, #3544).
		if dst.size == 8 {
			if rex := rexFor(false, 0, dst.reg, needsRexByte(dst)); rex != 0 {
				a.emit(rex)
			}
			a.emit(0x80)
			a.emit(modrmReg(ext, dst.reg))
			a.emit(byte(src.imm))
			return nil
		}
		w := dst.size == 64
		if fitsInt8(src.imm) {
			if rex := rexFor(w, 0, dst.reg, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0x83)
			a.emit(modrmReg(ext, dst.reg))
			a.emit(byte(src.imm))
			return nil
		}
		if rex := rexFor(w, 0, dst.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x81)
		a.emit(modrmReg(ext, dst.reg))
		a.emit32(uint32(src.imm))
		return nil
	case dst.kind == opMem && src.kind == opImm:
		// Honour the operand size from a `byte ptr` prefix: <alu> r/m8,
		// imm8 is 80 /ext ib. The 83/81 forms below are 32/64-bit — using
		// them for a byte compare reads/writes 4 bytes (`cmp byte ptr
		// [rdi], 61` in __fern_env's '=' scan silently became cmp dword,
		// so env() never matched a name and always returned None on the
		// natively-linked path).
		if dst.memSize == 8 {
			if rex := memRex(false, 0, dst, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0x80)
			a.encodeMem(ext, dst)
			a.emit(byte(src.imm))
			return nil
		}
		if dst.memSize == 16 {
			// 66 [REX] 83 /ext ib (sign-extended imm8) or 66 [REX] 81 /ext iw.
			a.emit(0x66)
			if rex := memRex(false, 0, dst, false); rex != 0 {
				a.emit(rex)
			}
			if fitsInt8(src.imm) {
				a.emit(0x83)
				a.encodeMem(ext, dst)
				a.emit(byte(src.imm))
				return nil
			}
			a.emit(0x81)
			a.encodeMem(ext, dst)
			a.emit(byte(src.imm), byte(src.imm>>8))
			return nil
		}
		w := dst.memSize == 64
		if fitsInt8(src.imm) {
			if rex := memRex(w, 0, dst, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0x83)
			a.encodeMem(ext, dst)
			a.emit(byte(src.imm))
			return nil
		}
		if rex := memRex(w, 0, dst, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x81)
		a.encodeMem(ext, dst)
		a.emit32(uint32(src.imm))
		return nil
	}
	return fmt.Errorf("unsupported binary-op form")
}

func (a *Assembler) test(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("test expects a register destination")
	}
	if ops[1].kind == opReg {
		return a.rmReg(0x84, ops[0], ops[1])
	}
	if ops[1].kind == opImm {
		o := ops[0]
		w := o.size == 64
		if o.size == 8 {
			if rex := rexFor(false, 0, o.reg, o.reg >= 4); rex != 0 {
				a.emit(rex)
			}
			a.emit(0xF6)
			a.emit(modrmReg(0, o.reg))
			a.emit(byte(ops[1].imm))
			return nil
		}
		if rex := rexFor(w, 0, o.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xF7)
		a.emit(modrmReg(0, o.reg))
		a.emit32(uint32(ops[1].imm))
		return nil
	}
	return fmt.Errorf("unsupported test form")
}

func (a *Assembler) imul(ops []operand) error {
	if len(ops) == 3 { // imul r, r/m, imm
		return a.imul3(ops)
	}
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("imul expects reg, r/m")
	}
	dst := ops[0]
	w := dst.size == 64
	if ops[1].kind == opReg {
		if rex := rexFor(w, dst.reg, ops[1].reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, 0xAF)
		a.emit(modrmReg(dst.reg, ops[1].reg))
		return nil
	}
	if ops[1].kind == opMem {
		if rex := memRex(w, dst.reg, ops[1], false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, 0xAF)
		a.encodeMem(dst.reg, ops[1])
		return nil
	}
	return fmt.Errorf("unsupported imul form")
}

// bitOp encodes the "0F <op> /r, dst = reg, src = r/m" family — the same
// shape as the two-operand imul, differing in the second opcode byte and
// whether a mandatory F3 precedes everything:
//
//	bsr    0xBD, no prefix   — floor(log2), the allocator's size class
//	lzcnt  0xBD, F3          — count leading zeros
//	tzcnt  0xBC, F3          — count trailing zeros
//	popcnt 0xB8, F3          — count set bits
//
// Note bsr and lzcnt are THE SAME OPCODE: only the F3 tells them apart,
// and it must be emitted BEFORE the REX byte. Put it after and the CPU
// reads a stray prefix on a bsr — a silently different answer (and one
// that is undefined at a zero input) rather than a fault.
func (a *Assembler) bitOp(ops []operand, op byte, prefix ...byte) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("bsr/lzcnt/tzcnt/popcnt expects reg, r/m")
	}
	dst := ops[0]
	w := dst.size == 64
	if ops[1].kind == opReg {
		a.emit(prefix...)
		if rex := rexFor(w, dst.reg, ops[1].reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, op)
		a.emit(modrmReg(dst.reg, ops[1].reg))
		return nil
	}
	if ops[1].kind == opMem {
		a.emit(prefix...)
		if rex := memRex(w, dst.reg, ops[1], false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, op)
		a.encodeMem(dst.reg, ops[1])
		return nil
	}
	return fmt.Errorf("unsupported bsr/lzcnt/tzcnt/popcnt form")
}

// imul3 encodes the three-operand "imul reg, r/m, imm" (multiply by a
// constant): 0x6B /r ib for an imm8, else 0x69 /r id.
func (a *Assembler) imul3(ops []operand) error {
	dst, src, imm := ops[0], ops[1], ops[2]
	if dst.kind != opReg || imm.kind != opImm || (src.kind != opReg && src.kind != opMem) {
		return fmt.Errorf("imul reg, r/m, imm: bad operands")
	}
	w := dst.size == 64
	short := fitsInt8(imm.imm)
	var rex byte
	if src.kind == opMem {
		rex = memRex(w, dst.reg, src, false)
	} else {
		rex = rexFor(w, dst.reg, src.reg, false)
	}
	if rex != 0 {
		a.emit(rex)
	}
	if short {
		a.emit(0x6B)
	} else {
		a.emit(0x69)
	}
	if src.kind == opReg {
		a.emit(modrmReg(dst.reg, src.reg))
	} else {
		a.encodeMem(dst.reg, src)
	}
	if short {
		a.emit(byte(imm.imm))
	} else {
		a.emit32(uint32(imm.imm))
	}
	return nil
}

// unaryF7 encodes the F7-group one-operand ops (idiv /7, div /6, neg /3).
func (a *Assembler) unaryF7(ops []operand, ext int) error {
	if len(ops) != 1 {
		return fmt.Errorf("unary op expects one operand")
	}
	o := ops[0]
	if o.kind == opReg {
		w := o.size == 64
		if rex := rexFor(w, 0, o.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xF7)
		a.emit(modrmReg(ext, o.reg))
		return nil
	}
	if o.kind == opMem {
		w := o.memSize == 64
		if rex := memRex(w, 0, o, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xF7)
		a.encodeMem(ext, o)
		return nil
	}
	return fmt.Errorf("unsupported unary-op form")
}

func (a *Assembler) incDec(ops []operand, ext int) error {
	if len(ops) != 1 || ops[0].kind != opReg {
		return fmt.Errorf("inc/dec expects one register")
	}
	o := ops[0]
	w := o.size == 64
	if rex := rexFor(w, 0, o.reg, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(0xFF)
	a.emit(modrmReg(ext, o.reg))
	return nil
}

func (a *Assembler) shift(ops []operand, ext int) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("shift expects reg, imm/cl")
	}
	o := ops[0]
	w := o.size == 64
	if ops[1].kind == opImm {
		if rex := rexFor(w, 0, o.reg, false); rex != 0 {
			a.emit(rex)
		}
		if ops[1].imm == 1 {
			a.emit(0xD1)
			a.emit(modrmReg(ext, o.reg))
			return nil
		}
		a.emit(0xC1)
		a.emit(modrmReg(ext, o.reg))
		a.emit(byte(ops[1].imm))
		return nil
	}
	if ops[1].kind == opReg && ops[1].reg == 1 { // cl
		if rex := rexFor(w, 0, o.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xD3)
		a.emit(modrmReg(ext, o.reg))
		return nil
	}
	return fmt.Errorf("shift count must be an immediate or cl")
}

func (a *Assembler) lea(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[1].kind != opMem {
		return fmt.Errorf("lea expects reg, mem")
	}
	dst := ops[0]
	w := dst.size == 64
	if rex := memRex(w, dst.reg, ops[1], false); rex != 0 {
		a.emit(rex)
	}
	a.emit(0x8D)
	a.encodeMem(dst.reg, ops[1])
	return nil
}

// movzx / movsx from an 8- or 16-bit source (and movsxd from 32-bit),
// where the source is a register or a memory operand.
func (a *Assembler) movzx(ops []operand, signed bool) error {
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("movzx/movsx expects a register destination")
	}
	dst, src := ops[0], ops[1]
	w := dst.size == 64
	srcSize := src.size
	if src.kind == opMem {
		srcSize = src.memSize
	} else if src.kind != opReg {
		return fmt.Errorf("movzx/movsx source must be a register or memory")
	}
	emitRM := func() {
		if src.kind == opReg {
			a.emit(modrmReg(dst.reg, src.reg))
		} else {
			a.encodeMem(dst.reg, src)
		}
	}
	rexB8 := needsRexByte(src) || needsRexByte(dst)
	var rex byte
	if src.kind == opMem {
		rex = memRex(w, dst.reg, src, false)
	} else {
		rex = rexFor(w, dst.reg, src.reg, rexB8)
	}
	if srcSize == 32 && signed { // movsxd r64, r/m32
		a.emit(memRexOrW(rex))
		a.emit(0x63)
		emitRM()
		return nil
	}
	var op2 byte
	switch {
	case srcSize == 8 && !signed:
		op2 = 0xB6
	case srcSize == 16 && !signed:
		op2 = 0xB7
	case srcSize == 8 && signed:
		op2 = 0xBE
	case srcSize == 16 && signed:
		op2 = 0xBF
	default:
		return fmt.Errorf("unsupported movzx/movsx widths")
	}
	if rex != 0 {
		a.emit(rex)
	}
	a.emit(0x0F, op2)
	emitRM()
	return nil
}

// memRexOrW ensures a REX byte is present (movsxd is always 64-bit, so
// REX.W must be set even if no extension bits are).
func memRexOrW(rex byte) byte {
	if rex == 0 {
		return 0x48
	}
	return rex | 0x08
}

func (a *Assembler) xchg(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[1].kind != opReg {
		return fmt.Errorf("xchg expects reg, reg")
	}
	return a.rmReg(0x86, ops[0], ops[1])
}

func (a *Assembler) setcc(ops []operand, cc byte) error {
	if len(ops) != 1 || ops[0].kind != opReg || ops[0].size != 8 {
		return fmt.Errorf("setcc expects an 8-bit register")
	}
	r := ops[0].reg
	if rex := rexFor(false, 0, r, r >= 4); rex != 0 {
		a.emit(rex)
	}
	a.emit(0x0F, 0x90+cc)
	a.emit(modrmReg(0, r))
	return nil
}

func (a *Assembler) jmp(ops []operand) error {
	if len(ops) != 1 {
		return fmt.Errorf("jmp expects one operand")
	}
	if ops[0].kind == opReg { // indirect: FF /4
		return a.indirectCallJmp(ops[0], 4)
	}
	if ops[0].kind != opLabel {
		return fmt.Errorf("jmp expects a label or register")
	}
	a.emit(0xE9)
	a.relFixups = append(a.relFixups, relFixup{at: len(a.text), sym: ops[0].sym})
	a.emit32(0)
	return nil
}

func (a *Assembler) call(ops []operand) error {
	if len(ops) != 1 {
		return fmt.Errorf("call expects one operand")
	}
	if ops[0].kind == opReg { // indirect: FF /2
		return a.indirectCallJmp(ops[0], 2)
	}
	if ops[0].kind != opLabel {
		return fmt.Errorf("call expects a label or register")
	}
	a.emit(0xE8)
	a.relFixups = append(a.relFixups, relFixup{at: len(a.text), sym: ops[0].sym})
	a.emit32(0)
	return nil
}

// indirectCallJmp encodes "call/jmp reg" (FF /2 and FF /4). The operand
// size is fixed at 64-bit, so only REX.B (for r8..r15) is ever needed.
func (a *Assembler) indirectCallJmp(o operand, ext int) error {
	if o.reg >= 8 {
		a.emit(0x41) // REX.B
	}
	a.emit(0xFF)
	a.emit(modrmReg(ext, o.reg))
	return nil
}

func (a *Assembler) jcc(ops []operand, cc byte) error {
	if len(ops) != 1 || ops[0].kind != opLabel {
		return fmt.Errorf("jcc expects a label")
	}
	a.emit(0x0F, 0x80+cc)
	a.relFixups = append(a.relFixups, relFixup{at: len(a.text), sym: ops[0].sym})
	a.emit32(0)
	return nil
}

// needsRexByte reports whether an 8-bit register operand forces a REX
// prefix to address spl/bpl/sil/dil (regs 4..7). The high-byte registers
// ah/ch/dh/bh share those numbers but must NOT carry REX, so they're
// excluded.
func needsRexByte(o operand) bool {
	return o.kind == opReg && o.size == 8 && o.reg >= 4 && o.reg <= 7 && !o.highByte
}

func fitsInt8(v int64) bool  { return v >= -128 && v <= 127 }
func fitsInt32(v int64) bool { return v >= -2147483648 && v <= 2147483647 }
