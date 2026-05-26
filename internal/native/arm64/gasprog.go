package arm64

import (
	"fmt"
	"strconv"
	"strings"
)

// AssembleProgram is the section-aware assembler: like Assemble, but it
// also handles a .rodata data section and symbol addressing (adrp +
// add #:lo12:sym), so programs can reference read-only constants and
// strings. .text is laid out at textVAddr and .rodata immediately
// after (8-byte aligned); the returned blobs are meant to sit in one
// R+X segment (see elf.StaticExecutableData).
//
// Supported in .rodata: .byte/.2byte/.4byte/.8byte (and the .hword/
// .word/.quad/etc. aliases), .ascii/.asciz/.string, and .balign/.align.
// Writable/zero-init sections (.data/.bss) are not handled yet.
const (
	secText = iota
	secRodata
	secIgnore // a section we don't materialise (e.g. .note.GNU-stack)
)

func AssembleProgram(src string, textVAddr uint64) (text, rodata []byte, err error) {
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
				return nil, nil, fmt.Errorf("line %d: %q: %w", lineno+1, strings.TrimSpace(raw), derr)
			}
			sec = newSec
			continue
		}
		switch sec {
		case secText:
			if derr := assembleProgInsn(a, line); derr != nil {
				return nil, nil, fmt.Errorf("line %d: %q: %w", lineno+1, strings.TrimSpace(raw), derr)
			}
		case secRodata:
			return nil, nil, fmt.Errorf("line %d: %q: unexpected non-directive in .rodata", lineno+1, strings.TrimSpace(raw))
		case secIgnore:
			// dropped
		}
	}
	return a.BytesProgram(textVAddr)
}

// handleProgDirective handles section switches and .rodata data
// directives. Returns the section in effect after the directive.
func handleProgDirective(a *Assembler, line string, sec int) (int, error) {
	fields := strings.Fields(line)
	d := fields[0]
	switch d {
	case ".text":
		return secText, nil
	case ".ltorg":
		// Flush the literal pool here (off the execution path).
		a.FlushLiterals()
		return sec, nil
	case ".rodata":
		return secRodata, nil
	case ".section":
		// e.g. ".section .rodata" / ".section .text" / ".section
		// .note.GNU-stack,...". Anything else we don't materialise.
		arg := ""
		if len(fields) > 1 {
			arg = fields[1]
		}
		switch {
		case strings.Contains(arg, ".text"):
			return secText, nil
		case strings.Contains(arg, ".rodata"):
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
	case ".ascii", ".asciz", ".string":
		s, err := strconv.Unquote(strings.TrimSpace(rest))
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

// emitInts appends comma-separated integer values as width-byte
// little-endian fields.
func emitInts(a *Assembler, rest string, width int) error {
	for _, tok := range strings.Split(rest, ",") {
		v, err := strconv.ParseInt(strings.TrimSpace(tok), 0, 64)
		if err != nil {
			// allow unsigned hex like 0x80000000 that overflows int64-as-signed
			u, uerr := strconv.ParseUint(strings.TrimSpace(tok), 0, 64)
			if uerr != nil {
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
		val, err := strconv.ParseUint(strings.TrimPrefix(ops[1], "="), 0, 64)
		if err != nil {
			return fmt.Errorf("bad literal %q", ops[1])
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
		a.ADRPsym(rd, ops[1])
		return nil
	case mnem == "add" && len(ops) == 3 && isLo12(ops[2]):
		rd, err := parseReg(ops[0])
		if err != nil {
			return err
		}
		rn, err := parseReg(ops[1])
		if err != nil {
			return err
		}
		a.AddLo12(rd, rn, lo12Sym(ops[2]))
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
