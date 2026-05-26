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
func AssembleProgram(src string, textVAddr uint64) (text, rodata []byte, err error) {
	a := NewAssembler()
	inText := true
	for lineno, raw := range strings.Split(src, "\n") {
		line := stripComment(raw)
		for {
			label, rest, ok := splitLabel(line)
			if !ok {
				break
			}
			if inText {
				a.TextLabel(label)
			} else {
				a.RodataLabel(label)
			}
			line = strings.TrimSpace(rest)
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ".") {
			switched, nowText, derr := handleProgDirective(a, line, inText)
			if derr != nil {
				return nil, nil, fmt.Errorf("line %d: %q: %w", lineno+1, strings.TrimSpace(raw), derr)
			}
			if switched {
				inText = nowText
			}
			continue
		}
		if !inText {
			return nil, nil, fmt.Errorf("line %d: instruction %q outside .text", lineno+1, line)
		}
		if derr := assembleProgInsn(a, line); derr != nil {
			return nil, nil, fmt.Errorf("line %d: %q: %w", lineno+1, strings.TrimSpace(raw), derr)
		}
	}
	return a.BytesProgram(textVAddr)
}

// handleProgDirective handles section switches and .rodata data
// directives. Returns (switchedSection, nowInText, err).
func handleProgDirective(a *Assembler, line string, inText bool) (bool, bool, error) {
	fields := strings.Fields(line)
	d := fields[0]
	switch d {
	case ".text":
		return true, true, nil
	case ".rodata":
		return true, false, nil
	case ".section":
		// e.g. ".section .rodata" / ".section .text" (optional flags).
		arg := ""
		if len(fields) > 1 {
			arg = fields[1]
		}
		if strings.Contains(arg, ".text") {
			return true, true, nil
		}
		if strings.Contains(arg, ".rodata") {
			return true, false, nil
		}
		return false, inText, fmt.Errorf("unsupported section %q", arg)
	}
	// In .text, the rest are harmless no-ops (handled like Assemble).
	if inText {
		return false, inText, handleDirective(line)
	}
	// In .rodata, parse data directives.
	return false, inText, appendRodataDirective(a, d, strings.TrimSpace(strings.TrimPrefix(line, d)))
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
		if d == ".p2align" {
			n = 1 << n
		}
		a.AlignRodata(n)
		return nil
	case ".text", ".arch", ".global", ".globl", ".type", ".size":
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
