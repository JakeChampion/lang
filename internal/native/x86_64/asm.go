// Package x86_64 is a pure-Go assembler for the subset of x86-64 the
// Fern code generator (internal/codegen/x86_64) emits, in Intel syntax
// (`.intel_syntax noprefix`). It is the x86-64 counterpart of
// internal/native/arm64: AssembleProgram turns the emitted `.s` text
// into a .text blob (plus .rodata) ready to drop into a static ELF-64
// executable via internal/native/elf.StaticExecutableDataX86 — no
// external assembler or linker.
//
// Covered: the integer / control-flow / call surface (ALU with full
// carry/borrow, one-operand mul/imul group, cmovcc, rotates and
// double-precision shifts, the bt/bswap/xadd/cmpxchg/lock atomics,
// push/pop of immediates and memory, indirect call/jmp through registers
// and memory), all four operand sizes including the 0x66-prefixed 16-bit
// forms, .text alignment with GNU-as-compatible NOP fill, plus
// .rodata/.bss data, rip-relative addressing, .quad symbol pointer
// tables, the string ops, and SSE/SSE2/SSE4 through the Haswell baseline
// (scalar and packed float math, packed integer arithmetic/compares/
// shuffles, vector shifts, movups/movdq*, pextr/pinsr, crc32, pcmp*stri,
// ptest, round*). Like GNU as, a jmp/jcc to an in-range label is relaxed
// to its 2-byte rel8 form (EB / 70+cc); only out-of-range branches — and
// calls, which have no short form — stay rel32. An unsupported
// instruction — or an ambiguous operand size — surfaces as a clear error
// rather than a miscompile.
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

// Assembler accumulates encoded machine code, relaxes in-range branches
// to their rel8 forms, and resolves text-label branch/call targets in a
// final pass.
type Assembler struct {
	text   []byte
	rodata []byte
	// bss accumulates .bss (zero-initialised) contributions SEPARATELY from
	// rodata, and is concatenated after it once layout is final. Folding the
	// two in emission order is what made the ELF writer's trailing-zero trim
	// useless: `.section .bss` blocks are emitted mid-stream, so the 64 MiB
	// __fern_strbuf_data reservation had initialised .rodata after it and the
	// whole run was written to the file (#6928). Kept at the tail, it is
	// trailing zeros and the loader supplies it via p_memsz.
	bss          []byte
	textLabels   map[string]int
	rodataLabels map[string]int
	bssLabels    map[string]int
	relFixups    []relFixup
	ripFixups    []ripFixup
	quadSyms     []quadSymFixup
	locRows      []LineRow
	relaxEvents  []relaxEvent
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
// virtual address once layout is final. .bss holds no such slot — a symbol
// address is not zero, so it is initialised data by definition — and
// emitInts rejects one there rather than silently placing it.
type quadSymFixup struct {
	at  int // offset in rodata
	sym string
}

func newAssembler() *Assembler {
	return &Assembler{
		textLabels:   map[string]int{},
		rodataLabels: map[string]int{},
		bssLabels:    map[string]int{},
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
			switch sec {
			case "text":
				a.defineTextLabel(label)
			case "rodata":
				a.rodataLabels[label] = len(a.rodata)
			case "bss":
				a.bssLabels[label] = len(a.bss)
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
	// Shrink in-range branches to their rel8 forms and settle the final
	// layout before any offsets are resolved.
	if err := a.relax(); err != nil {
		return nil, nil, nil, nil, nil, err
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

	// .bss sits immediately after .rodata in the blob, 16-aligned so the
	// widest scalar in it stays naturally aligned. Placing it last is what
	// lets the ELF writer drop it from the file: everything from bssBase on
	// is zero, so trailingTrimZeros reaches all of it.
	if len(a.bss) > 0 {
		for len(a.rodata)%16 != 0 {
			a.rodata = append(a.rodata, 0)
		}
	}
	bssBase := rodataBase + len(a.rodata)
	dataLen := len(a.rodata) + len(a.bss)

	// PIE self-relocation symbols the prologue references via [rip+sym].
	// Their base-relative vaddrs MUST match where elf.StaticPieExecutableX86
	// lays the bytes: .rela.dyn is 8-aligned after the data blob, one
	// Elf64_Rela (24 bytes) per `.quad` slot; __ehdr_start is the ELF header
	// at vaddr 0 (so [rip+__ehdr_start] yields the runtime load base).
	var pieSyms map[string]uint64
	if pie {
		relaStart := rodataVAddr + uint64(dataLen)
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
		} else if off, ok := a.bssLabels[f.sym]; ok {
			symOff = bssBase + off
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
		} else if off, ok := a.bssLabels[f.sym]; ok {
			abs = textVAddr + uint64(bssBase+off)
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
	return a.text, append(a.rodata, a.bss...), relocs, a.textLabels, a.locRows, nil
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
	case ".rodata", ".data":
		// .data is value-initialised writable globals (allocator cursors,
		// freelist). The single program segment is mapped R+W+X, so it
		// lives in the same blob as .rodata.
		return "rodata", nil
	case ".bss":
		return "bss", nil
	case ".section":
		arg := ""
		if len(fields) > 1 {
			arg = fields[1]
		}
		switch {
		case strings.Contains(arg, ".text"):
			return "text", nil
		case strings.Contains(arg, ".bss"):
			return "bss", nil
		case strings.Contains(arg, ".rodata"), strings.Contains(arg, ".data"):
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
				if e := a.trailingPad(); e != nil {
					e.locs = append(e.locs, len(a.locRows)-1)
				}
			}
		}
		return sec, nil
	}
	if sec == "ignore" {
		return "ignore", nil
	}
	if sec == "rodata" || sec == "bss" {
		return sec, a.appendRodataDirective(sec, d, strings.TrimSpace(strings.TrimPrefix(line, d)))
	}
	switch d {
	case ".align", ".balign", ".p2align":
		return "text", a.alignText(d, strings.TrimSpace(strings.TrimPrefix(line, d)))
	}
	return sec, fmt.Errorf("unsupported directive %q", d)
}

// alignText pads .text to the requested boundary with the multi-byte NOP
// fill GNU as uses. On x86-64 ELF, .align and .balign both take a byte
// count and .p2align a power-of-two exponent. The optional second argument
// (a fill value) is ignored — padding is always executable NOPs — and the
// optional third (max-skip) is honoured: alignment is skipped entirely when
// it would insert more than that many bytes.
func (a *Assembler) alignText(d, rest string) error {
	parts := strings.Split(rest, ",")
	n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || n < 0 {
		return fmt.Errorf("bad alignment %q", rest)
	}
	align := n
	if d == ".p2align" {
		if n > 30 {
			return fmt.Errorf("bad .p2align exponent %d", n)
		}
		align = 1 << n
	}
	if align <= 1 {
		return nil
	}
	maxSkip := -1
	if len(parts) >= 3 {
		if s := strings.TrimSpace(parts[2]); s != "" {
			maxSkip, err = strconv.Atoi(s)
			if err != nil {
				return fmt.Errorf("bad max-skip %q", rest)
			}
		}
	}
	pad := padWidth(len(a.text), align, maxSkip)
	// Recorded even when pad is 0: branch relaxation shifts offsets, so
	// the width must be recomputed against the final layout.
	a.relaxEvents = append(a.relaxEvents, relaxEvent{start: len(a.text), size: pad, align: align, maxSkip: maxSkip})
	a.text = appendNopPad(a.text, pad)
	return nil
}

// padWidth is the NOP fill needed to bring offset off up to a multiple of
// align, honouring a gas max-skip (-1 when absent): alignment is skipped
// entirely when it would insert more than maxSkip bytes.
func padWidth(off, align, maxSkip int) int {
	pad := (align - off%align) % align
	if maxSkip >= 0 && pad > maxSkip {
		return 0
	}
	return pad
}

// nopPatterns[n] is the n-byte NOP GNU as fills alignment padding with
// (binutils' alt_patt: 0F 1F multi-byte NOPs, prefix-padded). Eleven bytes
// is the longest single fill gas emits; longer paddings repeat it.
var nopPatterns = [12][]byte{
	1:  {0x90},
	2:  {0x66, 0x90},
	3:  {0x0F, 0x1F, 0x00},
	4:  {0x0F, 0x1F, 0x40, 0x00},
	5:  {0x0F, 0x1F, 0x44, 0x00, 0x00},
	6:  {0x66, 0x0F, 0x1F, 0x44, 0x00, 0x00},
	7:  {0x0F, 0x1F, 0x80, 0x00, 0x00, 0x00, 0x00},
	8:  {0x0F, 0x1F, 0x84, 0x00, 0x00, 0x00, 0x00, 0x00},
	9:  {0x66, 0x0F, 0x1F, 0x84, 0x00, 0x00, 0x00, 0x00, 0x00},
	10: {0x66, 0x2E, 0x0F, 0x1F, 0x84, 0x00, 0x00, 0x00, 0x00, 0x00},
	11: {0x66, 0x66, 0x2E, 0x0F, 0x1F, 0x84, 0x00, 0x00, 0x00, 0x00, 0x00},
}

func appendNopPad(b []byte, n int) []byte {
	for n > 11 {
		b = append(b, nopPatterns[11]...)
		n -= 11
	}
	if n > 0 {
		b = append(b, nopPatterns[n]...)
	}
	return b
}

// insn parses and encodes one .text instruction.
func (a *Assembler) insn(line string) error {
	mnem, rest := splitMnemonic(line)
	// Prefix mnemonics wrap a whole instruction, so they must recurse
	// before `rest` is parsed as an operand list.
	switch mnem {
	case "rep", "repe", "repz":
		a.emit(0xF3)
		return a.insn(rest)
	case "repne", "repnz":
		a.emit(0xF2)
		return a.insn(rest)
	case "lock":
		next, _ := splitMnemonic(rest)
		if !lockable[next] {
			return fmt.Errorf("lock needs a lockable instruction to prefix (got %q)", next)
		}
		a.emit(0xF0)
		return a.insn(rest)
	}
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
	case "nop":
		a.emit(0x90)
		return nil
	case "int3":
		a.emit(0xCC)
		return nil
	case "leave":
		a.emit(0xC9)
		return nil
	case "pause":
		a.emit(0xF3, 0x90)
		return nil
	case "mfence":
		a.emit(0x0F, 0xAE, 0xF0)
		return nil
	case "lfence":
		a.emit(0x0F, 0xAE, 0xE8)
		return nil
	case "sfence":
		a.emit(0x0F, 0xAE, 0xF8)
		return nil
	case "cbw":
		a.emit(0x66, 0x98)
		return nil
	case "cwde":
		a.emit(0x98)
		return nil
	case "cdqe":
		a.emit(0x48, 0x98)
		return nil
	case "cwd":
		a.emit(0x66, 0x99)
		return nil
	// pushfq / popfq — save and restore RFLAGS. The x86-64 SSA backend's heap
	// guard needs them: every way to compare its cursor against the limit writes
	// flags, and it is called from bump sites that may keep flags live.
	case "pushfq":
		a.emit(0x9C)
		return nil
	case "popfq":
		a.emit(0x9D)
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
	case "std":
		a.emit(0xFD)
		return nil
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
	case "stosd":
		a.emit(0xAB)
		return nil
	case "stosq":
		a.emit(0x48, 0xAB)
		return nil
	case "cmpsb":
		a.emit(0xA6)
		return nil
	case "cmpsw":
		a.emit(0x66, 0xA7)
		return nil
	case "cmpsq":
		a.emit(0x48, 0xA7)
		return nil
	case "scasb":
		a.emit(0xAE)
		return nil
	case "scasw":
		a.emit(0x66, 0xAF)
		return nil
	case "scasd":
		a.emit(0xAF)
		return nil
	case "scasq":
		a.emit(0x48, 0xAF)
		return nil
	case "lodsb":
		a.emit(0xAC)
		return nil
	case "lodsw":
		a.emit(0x66, 0xAD)
		return nil
	case "lodsd":
		a.emit(0xAD)
		return nil
	case "lodsq":
		a.emit(0x48, 0xAD)
		return nil
	case "movsd", "cmpsd":
		// With NO operands these are the dword string ops movs/cmps (A5/A7);
		// with operands the same mnemonics are the SSE scalar-double forms
		// handled below.
		if len(ops) == 0 {
			if mnem == "movsd" {
				a.emit(0xA5)
			} else {
				a.emit(0xA7)
			}
			return nil
		}
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
	case "adc":
		return a.alu(ops, 0x10, 2)
	case "sbb":
		return a.alu(ops, 0x18, 3)
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
	case "mul":
		return a.unaryF7(ops, 4)
	case "neg":
		return a.unaryF7(ops, 3)
	case "not":
		return a.unaryF7(ops, 2)
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
	case "shld":
		return a.shld(ops)
	case "shrd":
		return a.shrd(ops)
	case "rol":
		return a.shift(ops, 0)
	case "ror":
		return a.shift(ops, 1)
	case "rcl":
		return a.shift(ops, 2)
	case "rcr":
		return a.shift(ops, 3)
	case "bt":
		return a.btOp(ops, 0xA3, 4)
	case "bts":
		return a.btOp(ops, 0xAB, 5)
	case "btr":
		return a.btOp(ops, 0xB3, 6)
	case "btc":
		return a.btOp(ops, 0xBB, 7)
	case "bswap":
		return a.bswap(ops)
	case "xadd":
		return a.rmwOp(ops, 0xC0, "xadd")
	case "cmpxchg":
		return a.rmwOp(ops, 0xB0, "cmpxchg")
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
	case "movups":
		return a.movsdss(0x00, ops)
	case "movupd":
		return a.movsdss(0x66, ops)
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
		return a.sse3AImm8(0x0B, ops, "roundsd")
	case "roundss":
		return a.sse3AImm8(0x0A, ops, "roundss")
	case "pcmpistri":
		return a.sse3AImm8(0x63, ops, "pcmpistri")
	case "pcmpestri":
		return a.sse3AImm8(0x61, ops, "pcmpestri")
	case "pmovmskb":
		return a.xmmToGpr(0x66, 0xD7, ops, "pmovmskb")
	case "movmskps":
		return a.xmmToGpr(0x00, 0x50, ops, "movmskps")
	case "movmskpd":
		return a.xmmToGpr(0x66, 0x50, ops, "movmskpd")
	case "pshufd":
		return a.sseImm8(0x66, 0x70, ops, "pshufd")
	case "shufps":
		return a.sseImm8(0x00, 0xC6, ops, "shufps")
	case "shufpd":
		return a.sseImm8(0x66, 0xC6, ops, "shufpd")
	case "pextrb", "pextrw", "pextrd", "pextrq":
		return a.pextr(mnem, ops)
	case "pinsrb", "pinsrw", "pinsrd", "pinsrq":
		return a.pinsr(mnem, ops)
	case "crc32":
		return a.crc32(ops)
	case "psllw", "psrlw", "psraw", "pslld", "psrld", "psrad",
		"psllq", "psrlq", "pslldq", "psrldq":
		// The by-immediate forms (0F 71/72/73 groups) shift by a constant;
		// the by-register forms fall through to the sseOps table. pslldq /
		// psrldq exist only with an immediate, so a register count on those
		// lands on the unsupported-instruction error below.
		if len(ops) == 2 && ops[1].kind == opImm {
			return a.vecShiftImm(mnem, ops)
		}
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
	if op, ok := sse38Ops[mnem]; ok {
		return a.sse38Op(op, ops)
	}
	if cc, ok := cmovccCode(mnem); ok {
		return a.cmov(ops, cc)
	}
	if cc, ok := jccCode(mnem); ok {
		return a.jcc(ops, cc)
	}
	if cc, ok := setccCode(mnem); ok {
		return a.setcc(ops, cc)
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
	if len(ops) != 1 {
		return fmt.Errorf("push/pop expects one operand")
	}
	o := ops[0]
	isPush := base == 0x50
	switch {
	case o.kind == opReg && (o.size == 64 || o.size == 16):
		// 32-bit push/pop does not exist in 64-bit mode; 8-bit never did.
		if o.size == 16 {
			a.emit(0x66)
		}
		if o.reg >= 8 {
			a.emit(0x41) // REX.B
		}
		a.emit(base + byte(o.reg&7))
		return nil
	case o.kind == opImm && isPush:
		if !fitsInt32(o.imm) {
			return fmt.Errorf("push immediate %d does not fit in 32 bits", o.imm)
		}
		if fitsInt8(o.imm) {
			a.emit(0x6A, byte(o.imm))
			return nil
		}
		a.emit(0x68)
		a.emit32(uint32(o.imm))
		return nil
	case o.kind == opMem && (o.memSize == 0 || o.memSize == 64):
		// push m64 = FF /6, pop m64 = 8F /0; the operand size defaults to
		// 64-bit, so no REX.W.
		op, ext := byte(0xFF), 6
		if !isPush {
			op, ext = 0x8F, 0
		}
		if rex := memRex(false, 0, o, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(op)
		a.encodeMem(ext, o)
		return nil
	}
	return fmt.Errorf("unsupported push/pop form")
}

func (a *Assembler) mov(ops []operand, abs bool) error {
	if len(ops) != 2 {
		return fmt.Errorf("mov expects two operands")
	}
	dst, src := ops[0], ops[1]
	if err := noXmm("mov", dst, src); err != nil {
		return err
	}
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
		if dst.size == 8 {
			// mov r8, imm8 : [REX] B0+rb ib
			if rex := rexFor(false, 0, dst.reg, needsRexByte(dst)); rex != 0 {
				a.emit(rex)
			}
			a.emit(0xB0 + byte(dst.reg&7))
			a.emit(byte(src.imm))
			return nil
		}
		if dst.size == 16 {
			// mov r16, imm16 : 66 [REX] B8+rw iw
			a.emit(0x66)
			if rex := rexFor(false, 0, dst.reg, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0xB8 + byte(dst.reg&7))
			a.emit(byte(src.imm), byte(src.imm>>8))
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
		// prefix. Emitting C7 + imm32 unconditionally makes
		// `mov byte ptr [mem], imm` write 4 bytes — a 3-byte buffer
		// overrun (#3544). C6 /0 ib is the byte form; word needs the
		// 0x66 size prefix + imm16.
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
// the 8-bit opcode; the 16/32/64-bit opcode is opBase|1, with the 16-bit
// form selected by the 0x66 operand-size prefix.
func (a *Assembler) rmReg(opBase byte, rm, reg operand) error {
	w := reg.size == 64
	op := opBase
	if reg.size != 8 {
		op |= 1
	}
	if reg.size == 16 {
		a.emit(0x66)
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
	if reg.size == 16 {
		a.emit(0x66)
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
	if reg.size == 16 {
		a.emit(0x66)
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
	if err := noXmm("binary op", dst, src); err != nil {
		return err
	}
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
		if dst.size == 16 {
			// 66 [REX] 83 /ext ib (sign-extended imm8) or 66 [REX] 81 /ext iw.
			a.emit(0x66)
			if rex := rexFor(false, 0, dst.reg, false); rex != 0 {
				a.emit(rex)
			}
			if fitsInt8(src.imm) {
				a.emit(0x83)
				a.emit(modrmReg(ext, dst.reg))
				a.emit(byte(src.imm))
				return nil
			}
			a.emit(0x81)
			a.emit(modrmReg(ext, dst.reg))
			a.emit(byte(src.imm), byte(src.imm>>8))
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

// test has only the MR form (84/85 /r) — it is symmetric, so ModRM.reg is
// always the register whichever operand order was written — plus the
// F6/F7 /0 immediate forms.
func (a *Assembler) test(ops []operand) error {
	if len(ops) != 2 {
		return fmt.Errorf("test expects two operands")
	}
	if err := noXmm("test", ops[0], ops[1]); err != nil {
		return err
	}
	switch {
	case ops[0].kind == opReg && ops[1].kind == opReg:
		return a.rmReg(0x84, ops[0], ops[1])
	case ops[0].kind == opReg && ops[1].kind == opMem:
		return a.memReg(0x84, ops[0], ops[1])
	case ops[0].kind == opMem && ops[1].kind == opReg:
		return a.memReg(0x84, ops[1], ops[0])
	case ops[0].kind == opReg && ops[1].kind == opImm:
		o := ops[0]
		switch o.size {
		case 8:
			if rex := rexFor(false, 0, o.reg, needsRexByte(o)); rex != 0 {
				a.emit(rex)
			}
			a.emit(0xF6)
			a.emit(modrmReg(0, o.reg))
			a.emit(byte(ops[1].imm))
			return nil
		case 16:
			a.emit(0x66)
			if rex := rexFor(false, 0, o.reg, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0xF7)
			a.emit(modrmReg(0, o.reg))
			a.emit(byte(ops[1].imm), byte(ops[1].imm>>8))
			return nil
		}
		if rex := rexFor(o.size == 64, 0, o.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xF7)
		a.emit(modrmReg(0, o.reg))
		a.emit32(uint32(ops[1].imm))
		return nil
	case ops[0].kind == opMem && ops[1].kind == opImm:
		m := ops[0]
		switch m.memSize {
		case 0:
			return fmt.Errorf("test on memory needs a byte/word/dword/qword ptr size")
		case 8:
			if rex := memRex(false, 0, m, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0xF6)
			a.encodeMem(0, m)
			a.emit(byte(ops[1].imm))
			return nil
		case 16:
			a.emit(0x66)
			if rex := memRex(false, 0, m, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0xF7)
			a.encodeMem(0, m)
			a.emit(byte(ops[1].imm), byte(ops[1].imm>>8))
			return nil
		}
		if rex := memRex(m.memSize == 64, 0, m, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0xF7)
		a.encodeMem(0, m)
		a.emit32(uint32(ops[1].imm))
		return nil
	}
	return fmt.Errorf("unsupported test form")
}

func (a *Assembler) imul(ops []operand) error {
	if len(ops) == 1 { // widening rdx:rax form, F6/F7 /5
		return a.unaryF7(ops, 5)
	}
	if len(ops) == 3 { // imul r, r/m, imm
		return a.imul3(ops)
	}
	if len(ops) != 2 || ops[0].kind != opReg {
		return fmt.Errorf("imul expects reg, r/m")
	}
	dst := ops[0]
	if dst.size == 8 {
		return fmt.Errorf("two-operand imul has no 8-bit form")
	}
	if dst.size == 16 {
		a.emit(0x66)
	}
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
	if dst.size == 8 || dst.size == 128 {
		return fmt.Errorf("bsf/bsr/lzcnt/tzcnt/popcnt need a 16/32/64-bit GPR destination")
	}
	if dst.size == 16 {
		// The 0x66 operand-size prefix goes before the mandatory F3.
		a.emit(0x66)
	}
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
	if dst.size == 8 {
		return fmt.Errorf("three-operand imul has no 8-bit form")
	}
	if dst.size == 16 {
		a.emit(0x66)
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
	switch {
	case short:
		a.emit(byte(imm.imm))
	case dst.size == 16:
		a.emit(byte(imm.imm), byte(imm.imm>>8))
	default:
		a.emit32(uint32(imm.imm))
	}
	return nil
}

// unaryF7 encodes the F6/F7-group one-operand ops (idiv /7, div /6,
// imul /5, mul /4, neg /3, not /2). An 8-bit operand selects the F6
// opcode; 16-bit adds the 0x66 prefix.
func (a *Assembler) unaryF7(ops []operand, ext int) error {
	if len(ops) != 1 {
		return fmt.Errorf("unary op expects one operand")
	}
	o := ops[0]
	if o.kind != opReg && o.kind != opMem {
		return fmt.Errorf("unsupported unary-op form")
	}
	if err := noXmm("unary op", o); err != nil {
		return err
	}
	size := o.size
	if o.kind == opMem {
		size = o.memSize
		if size == 0 {
			return fmt.Errorf("unary op on memory needs a byte/word/dword/qword ptr size")
		}
	}
	op := byte(0xF7)
	if size == 8 {
		op = 0xF6
	}
	if size == 16 {
		a.emit(0x66)
	}
	if o.kind == opReg {
		if rex := rexFor(size == 64, 0, o.reg, needsRexByte(o)); rex != 0 {
			a.emit(rex)
		}
		a.emit(op)
		a.emit(modrmReg(ext, o.reg))
		return nil
	}
	if rex := memRex(size == 64, 0, o, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(op)
	a.encodeMem(ext, o)
	return nil
}

func (a *Assembler) incDec(ops []operand, ext int) error {
	if len(ops) != 1 {
		return fmt.Errorf("inc/dec expects one operand")
	}
	o := ops[0]
	if err := noXmm("inc/dec", o); err != nil {
		return err
	}
	size := o.size
	if o.kind == opMem {
		size = o.memSize
		if size == 0 {
			return fmt.Errorf("inc/dec on memory needs a byte/word/dword/qword ptr size")
		}
	} else if o.kind != opReg {
		return fmt.Errorf("inc/dec expects a register or memory operand")
	}
	op := byte(0xFF)
	if size == 8 {
		op = 0xFE
	}
	if size == 16 {
		a.emit(0x66)
	}
	if o.kind == opReg {
		if rex := rexFor(size == 64, 0, o.reg, needsRexByte(o)); rex != 0 {
			a.emit(rex)
		}
		a.emit(op)
		a.emit(modrmReg(ext, o.reg))
		return nil
	}
	if rex := memRex(size == 64, 0, o, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(op)
	a.encodeMem(ext, o)
	return nil
}

// shift encodes the C0/C1/D0..D3 shift-and-rotate group (rol /0, ror /1,
// rcl /2, rcr /3, shl /4, shr /5, sar /7) for register and sized memory
// destinations.
func (a *Assembler) shift(ops []operand, ext int) error {
	if len(ops) != 2 || (ops[0].kind != opReg && ops[0].kind != opMem) {
		return fmt.Errorf("shift expects reg/mem, imm/cl")
	}
	dst := ops[0]
	if err := noXmm("shift", dst); err != nil {
		return err
	}
	size := dst.size
	if dst.kind == opMem {
		size = dst.memSize
		if size == 0 {
			return fmt.Errorf("shift on memory needs a byte/word/dword/qword ptr size")
		}
	}
	var op byte
	imm := byte(0)
	hasImm := false
	switch {
	case ops[1].kind == opImm && ops[1].imm == 1:
		op = 0xD1
	case ops[1].kind == opImm:
		op = 0xC1
		imm, hasImm = byte(ops[1].imm), true
	case ops[1].kind == opReg && ops[1].reg == 1 && ops[1].size == 8: // cl
		op = 0xD3
	default:
		return fmt.Errorf("shift count must be an immediate or cl")
	}
	if size == 8 {
		op-- // the byte-operand opcode sits one below each wide one (C0/D0/D2)
	}
	if size == 16 {
		a.emit(0x66)
	}
	if dst.kind == opReg {
		if rex := rexFor(size == 64, 0, dst.reg, needsRexByte(dst)); rex != 0 {
			a.emit(rex)
		}
		a.emit(op)
		a.emit(modrmReg(ext, dst.reg))
	} else {
		if rex := memRex(size == 64, 0, dst, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(op)
		a.encodeMem(ext, dst)
	}
	if hasImm {
		a.emit(imm)
	}
	return nil
}

// shld encodes `shld r/m64, r64, cl` (0F A5 /r) and its imm8 form
// (0F A4 /r ib) — the double-precision left shift, which fills the vacated
// low bits of the destination from the HIGH bits of the source.
//
// The operand direction is reversed from the usual two-register encoding:
// ModRM.reg holds the SOURCE and ModRM.rm the DESTINATION, so `shld rsi, rdi,
// cl` is 48 0f a5 fe with reg=rdi and rm=rsi.
func (a *Assembler) shld(ops []operand) error {
	return a.shldShrd(ops, 0xA4, "shld")
}

// shrd (0F AD /r, imm8 form 0F AC /r ib) is shld's right-shift mirror: the
// destination's vacated HIGH bits fill from the LOW bits of the source.
func (a *Assembler) shrd(ops []operand) error {
	return a.shldShrd(ops, 0xAC, "shrd")
}

func (a *Assembler) shldShrd(ops []operand, opImmForm byte, name string) error {
	if len(ops) != 3 || (ops[0].kind != opReg && ops[0].kind != opMem) ||
		ops[0].size == 128 || ops[1].kind != opReg || ops[1].size == 128 {
		return fmt.Errorf("%s expects reg, reg, imm/cl", name)
	}
	dst, src := ops[0], ops[1]
	size := dst.size
	if dst.kind == opMem {
		size = src.size // a memory destination takes its width from the register
	}
	if size == 8 {
		return fmt.Errorf("%s has no 8-bit form", name)
	}
	op := opImmForm
	isImm := false
	switch {
	case ops[2].kind == opImm:
		isImm = true
	case ops[2].kind == opReg && ops[2].reg == 1 && ops[2].size == 8: // cl
		op++
	default:
		return fmt.Errorf("%s count must be an immediate or cl", name)
	}
	if size == 16 {
		a.emit(0x66)
	}
	w := size == 64
	if dst.kind == opReg {
		if rex := rexFor(w, src.reg, dst.reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, op)
		a.emit(modrmReg(src.reg, dst.reg))
	} else {
		if rex := memRex(w, src.reg, dst, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, op)
		a.encodeMem(src.reg, dst)
	}
	if isImm {
		a.emit(byte(ops[2].imm))
	}
	return nil
}

// btOp encodes the bit-test family: bt/bts/btr/btc r/m, reg (0F A3/AB/B3/BB,
// ModRM.reg = the bit-index SOURCE) and r/m, imm8 (0F BA /4../7 ib). regOp is
// the register-form opcode, immExt the /digit in the BA group. There is no
// 8-bit form.
func (a *Assembler) btOp(ops []operand, regOp byte, immExt int) error {
	if len(ops) != 2 || (ops[0].kind != opReg && ops[0].kind != opMem) || ops[0].size == 128 {
		return fmt.Errorf("bt/bts/btr/btc expects reg/mem, reg/imm")
	}
	dst := ops[0]
	size := dst.size
	if ops[1].kind == opReg && ops[1].size != 128 {
		src := ops[1]
		if dst.kind == opMem {
			size = src.size // memory width follows the bit-index register
		}
		if size == 8 {
			return fmt.Errorf("bt/bts/btr/btc has no 8-bit form")
		}
		if size == 16 {
			a.emit(0x66)
		}
		w := size == 64
		if dst.kind == opReg {
			if rex := rexFor(w, src.reg, dst.reg, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0x0F, regOp)
			a.emit(modrmReg(src.reg, dst.reg))
			return nil
		}
		if rex := memRex(w, src.reg, dst, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, regOp)
		a.encodeMem(src.reg, dst)
		return nil
	}
	if ops[1].kind == opImm {
		if dst.kind == opMem {
			size = dst.memSize
			if size == 0 {
				return fmt.Errorf("bt/bts/btr/btc on memory needs a word/dword/qword ptr size")
			}
		}
		if size == 8 {
			return fmt.Errorf("bt/bts/btr/btc has no 8-bit form")
		}
		if size == 16 {
			a.emit(0x66)
		}
		w := size == 64
		if dst.kind == opReg {
			if rex := rexFor(w, 0, dst.reg, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0x0F, 0xBA)
			a.emit(modrmReg(immExt, dst.reg))
		} else {
			if rex := memRex(w, 0, dst, false); rex != 0 {
				a.emit(rex)
			}
			a.emit(0x0F, 0xBA)
			a.encodeMem(immExt, dst)
		}
		a.emit(byte(ops[1].imm))
		return nil
	}
	return fmt.Errorf("bt/bts/btr/btc bit index must be a register or immediate")
}

// bswap r32/r64: 0F C8+rd. The 16-bit form is architecturally undefined
// (GNU as rejects it), and there is no byte form.
func (a *Assembler) bswap(ops []operand) error {
	if len(ops) != 1 || ops[0].kind != opReg || (ops[0].size != 32 && ops[0].size != 64) {
		return fmt.Errorf("bswap expects a 32- or 64-bit register")
	}
	o := ops[0]
	if rex := rexFor(o.size == 64, 0, o.reg, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(0x0F, 0xC8+byte(o.reg&7))
	return nil
}

// rmwOp encodes the two-byte-opcode read-modify-write family xadd
// (0F C0/C1) and cmpxchg (0F B0/B1): ModRM.reg is the SOURCE register, the
// destination is a register or memory. opBase is the 8-bit opcode; wider
// sizes use opBase|1 with 66/REX.W from the source register's width.
func (a *Assembler) rmwOp(ops []operand, opBase byte, name string) error {
	if len(ops) != 2 || ops[1].kind != opReg || ops[1].size == 128 || ops[0].size == 128 ||
		(ops[0].kind != opReg && ops[0].kind != opMem) {
		return fmt.Errorf("%s expects reg/mem destination, reg source", name)
	}
	dst, src := ops[0], ops[1]
	op := opBase
	if src.size != 8 {
		op |= 1
	}
	if src.size == 16 {
		a.emit(0x66)
	}
	w := src.size == 64
	if dst.kind == opReg {
		if rex := rexFor(w, src.reg, dst.reg, needsRexByte(src) || needsRexByte(dst)); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, op)
		a.emit(modrmReg(src.reg, dst.reg))
		return nil
	}
	if rex := memRex(w, src.reg, dst, needsRexByte(src)); rex != 0 {
		a.emit(rex)
	}
	a.emit(0x0F, op)
	a.encodeMem(src.reg, dst)
	return nil
}

// cmov encodes cmovcc reg, reg/mem (0F 40+cc /r). There is no 8-bit form.
func (a *Assembler) cmov(ops []operand, cc byte) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[0].size == 128 {
		return fmt.Errorf("cmovcc expects a general-purpose register destination")
	}
	dst := ops[0]
	if dst.size == 8 {
		return fmt.Errorf("cmovcc has no 8-bit form")
	}
	if dst.size == 16 {
		a.emit(0x66)
	}
	w := dst.size == 64
	if ops[1].kind == opReg && ops[1].size != 128 {
		if rex := rexFor(w, dst.reg, ops[1].reg, false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, 0x40+cc)
		a.emit(modrmReg(dst.reg, ops[1].reg))
		return nil
	}
	if ops[1].kind == opMem {
		if rex := memRex(w, dst.reg, ops[1], false); rex != 0 {
			a.emit(rex)
		}
		a.emit(0x0F, 0x40+cc)
		a.encodeMem(dst.reg, ops[1])
		return nil
	}
	return fmt.Errorf("cmovcc source must be a register or memory")
}

func (a *Assembler) lea(ops []operand) error {
	if len(ops) != 2 || ops[0].kind != opReg || ops[1].kind != opMem {
		return fmt.Errorf("lea expects reg, mem")
	}
	dst := ops[0]
	if dst.size == 8 || dst.size == 128 {
		return fmt.Errorf("lea destination must be a 16/32/64-bit GPR")
	}
	if dst.size == 16 {
		a.emit(0x66)
	}
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
	if dst.size == 8 || dst.size == 128 || srcSize == 128 || srcSize >= dst.size {
		return fmt.Errorf("movzx/movsx must widen a smaller GPR/memory source")
	}
	if dst.size == 16 {
		a.emit(0x66)
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

// xchg encodes reg,reg and the memory forms (86/87 /r). The memory form is
// implicitly LOCKed by the CPU regardless of any lock prefix.
func (a *Assembler) xchg(ops []operand) error {
	if len(ops) != 2 {
		return fmt.Errorf("xchg expects two operands")
	}
	switch {
	case ops[0].kind == opReg && ops[1].kind == opReg:
		return a.rmReg(0x86, ops[0], ops[1])
	case ops[0].kind == opMem && ops[1].kind == opReg:
		return a.memReg(0x86, ops[1], ops[0])
	case ops[0].kind == opReg && ops[1].kind == opMem:
		return a.memReg(0x86, ops[0], ops[1])
	}
	return fmt.Errorf("xchg expects reg/mem, reg")
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
	if ops[0].kind == opMem { // indirect through memory: FF /4
		return a.indirectCallJmpMem(ops[0], 4)
	}
	if ops[0].kind != opLabel {
		return fmt.Errorf("jmp expects a label, register, or memory operand")
	}
	a.relaxEvents = append(a.relaxEvents, relaxEvent{start: len(a.text), size: 5, fixup: len(a.relFixups)})
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
	if ops[0].kind == opMem { // indirect through memory: FF /2
		return a.indirectCallJmpMem(ops[0], 2)
	}
	if ops[0].kind != opLabel {
		return fmt.Errorf("call expects a label, register, or memory operand")
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

// indirectCallJmpMem encodes "call/jmp qword ptr [mem]" (FF /2 and /4),
// including rip-relative targets. The operand size is fixed at 64-bit.
func (a *Assembler) indirectCallJmpMem(o operand, ext int) error {
	if o.memSize != 0 && o.memSize != 64 {
		return fmt.Errorf("indirect call/jmp through memory must be 64-bit (qword ptr)")
	}
	if rex := memRex(false, 0, o, false); rex != 0 {
		a.emit(rex)
	}
	a.emit(0xFF)
	a.encodeMem(ext, o)
	return nil
}

func (a *Assembler) jcc(ops []operand, cc byte) error {
	if len(ops) != 1 || ops[0].kind != opLabel {
		return fmt.Errorf("jcc expects a label")
	}
	a.relaxEvents = append(a.relaxEvents, relaxEvent{start: len(a.text), size: 6, fixup: len(a.relFixups)})
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

// lockable is the set of instructions the F0 lock prefix applies to;
// anything else is #UD at runtime, so it is rejected at assembly.
var lockable = map[string]bool{
	"add": true, "adc": true, "and": true, "btc": true, "btr": true,
	"bts": true, "cmpxchg": true, "dec": true, "inc": true, "neg": true,
	"not": true, "or": true, "sbb": true, "sub": true, "xadd": true,
	"xchg": true, "xor": true,
}

// noXmm rejects xmm registers reaching a GPR-only encoder, where the
// register number would otherwise encode a general-purpose register
// silently (e.g. `add xmm0, xmm1` becoming `add eax, ecx`).
func noXmm(what string, ops ...operand) error {
	for _, o := range ops {
		if o.kind == opReg && o.size == 128 {
			return fmt.Errorf("%s cannot take an xmm register", what)
		}
	}
	return nil
}

func fitsInt8(v int64) bool  { return v >= -128 && v <= 127 }
func fitsInt32(v int64) bool { return v >= -2147483648 && v <= 2147483647 }
