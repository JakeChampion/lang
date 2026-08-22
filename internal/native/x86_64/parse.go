package x86_64

import (
	"fmt"
	"strconv"
	"strings"
)

// regTable maps every register name the code generator emits to its
// number (0..15) and operand size in bits.
var regTable = func() map[string]struct{ num, size int } {
	m := map[string]struct{ num, size int }{}
	r64 := []string{"rax", "rcx", "rdx", "rbx", "rsp", "rbp", "rsi", "rdi", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"}
	r32 := []string{"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi", "r8d", "r9d", "r10d", "r11d", "r12d", "r13d", "r14d", "r15d"}
	r16 := []string{"ax", "cx", "dx", "bx", "sp", "bp", "si", "di", "r8w", "r9w", "r10w", "r11w", "r12w", "r13w", "r14w", "r15w"}
	r8 := []string{"al", "cl", "dl", "bl", "spl", "bpl", "sil", "dil", "r8b", "r9b", "r10b", "r11b", "r12b", "r13b", "r14b", "r15b"}
	for i, n := range r64 {
		m[n] = struct{ num, size int }{i, 64}
	}
	for i, n := range r32 {
		m[n] = struct{ num, size int }{i, 32}
	}
	for i, n := range r16 {
		m[n] = struct{ num, size int }{i, 16}
	}
	for i, n := range r8 {
		m[n] = struct{ num, size int }{i, 8}
	}
	return m
}()

func parseOperand(s string) (operand, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return operand{}, fmt.Errorf("empty operand")
	}
	low := strings.ToLower(s)
	if r, ok := regTable[low]; ok {
		return operand{kind: opReg, reg: r.num, size: r.size}, nil
	}
	// High-byte registers ah/ch/dh/bh share numbers 4..7 with spl/bpl/sil/dil
	// but mean the high byte and cannot be encoded with a REX prefix.
	switch low {
	case "ah":
		return operand{kind: opReg, reg: 4, size: 8, highByte: true}, nil
	case "ch":
		return operand{kind: opReg, reg: 5, size: 8, highByte: true}, nil
	case "dh":
		return operand{kind: opReg, reg: 6, size: 8, highByte: true}, nil
	case "bh":
		return operand{kind: opReg, reg: 7, size: 8, highByte: true}, nil
	}
	if strings.HasPrefix(low, "xmm") {
		if n, err := strconv.Atoi(low[3:]); err == nil && n >= 0 && n < 16 {
			// SSE register; size 128 marks it as an xmm operand.
			return operand{kind: opReg, reg: n, size: 128}, nil
		}
	}
	if strings.Contains(s, "[") {
		// All memory operands carry brackets; a size prefix ("qword ptr")
		// only ever precedes them. (Checking for the bare substring "ptr"
		// would misfire on symbols like __ptr_width / __fern_drop_arr_ptr.)
		return parseMem(s)
	}
	if v, err := strconv.ParseInt(s, 0, 64); err == nil {
		return operand{kind: opImm, imm: v}, nil
	}
	if u, err := strconv.ParseUint(s, 0, 64); err == nil {
		return operand{kind: opImm, imm: int64(u)}, nil
	}
	if isLabelName(s) {
		return operand{kind: opLabel, sym: s}, nil
	}
	return operand{}, fmt.Errorf("cannot parse operand %q", s)
}

func parseMem(s string) (operand, error) {
	o := operand{kind: opMem, base: -1, index: -1, scale: 1}
	// A size keyword ("qword ptr") only appears before the '['. Scoping the
	// search there avoids matching a "ptr" substring inside a symbol name
	// like __fern_heap_ptr.
	pre := s
	if lb := strings.IndexByte(s, '['); lb >= 0 {
		pre = s[:lb]
	}
	if i := strings.Index(strings.ToLower(pre), "ptr"); i >= 0 {
		switch strings.ToLower(strings.TrimSpace(s[:i])) {
		case "byte":
			o.memSize = 8
		case "word":
			o.memSize = 16
		case "dword":
			o.memSize = 32
		case "qword":
			o.memSize = 64
		case "":
			// bare "ptr" — leave size unspecified
		default:
			return operand{}, fmt.Errorf("bad size prefix in %q", s)
		}
		s = strings.TrimSpace(s[i+3:])
	}
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return operand{}, fmt.Errorf("bad memory operand %q", s)
	}
	inner := strings.ReplaceAll(s[1:len(s)-1], " ", "")
	if inner == "" {
		return operand{}, fmt.Errorf("empty memory operand")
	}
	// Parse [ term (+|-) term ... ] where a term is a register,
	// register*scale, displacement, or (for rip) a symbol.
	for _, t := range splitTerms(inner) {
		tok := t.tok
		if strings.EqualFold(tok, "rip") {
			continue // the symbol displacement arrives as the next term
		}
		if strings.Contains(tok, "*") {
			parts := strings.SplitN(tok, "*", 2)
			r, ok := regTable[strings.ToLower(parts[0])]
			if !ok {
				return operand{}, fmt.Errorf("bad index register %q", parts[0])
			}
			sc, err := strconv.Atoi(parts[1])
			if err != nil || (sc != 1 && sc != 2 && sc != 4 && sc != 8) {
				return operand{}, fmt.Errorf("bad scale %q", parts[1])
			}
			o.index, o.scale = r.num, sc
			continue
		}
		if r, ok := regTable[strings.ToLower(tok)]; ok {
			// First bare register is the base; a second becomes the index.
			if o.base < 0 {
				o.base = r.num
			} else if o.index < 0 {
				o.index = r.num
			} else {
				return operand{}, fmt.Errorf("too many registers in memory operand %q", inner)
			}
			continue
		}
		// Otherwise a displacement (numeric) or, with rip, a symbol.
		if v, err := strconv.ParseInt(t.signed(), 0, 64); err == nil {
			o.disp += v
			continue
		}
		if o.base < 0 && o.index < 0 && isLabelName(tok) {
			o.sym = tok // rip-relative symbol
			continue
		}
		return operand{}, fmt.Errorf("bad term %q in memory operand", tok)
	}
	return o, nil
}

type memTerm struct {
	neg bool
	tok string
}

func (t memTerm) signed() string {
	if t.neg {
		return "-" + t.tok
	}
	return t.tok
}

// splitTerms breaks a memory operand body into signed terms, e.g.
// "rax+rcx*4-8" → [{rax} {rcx*4} {-8}]. A leading term has no sign.
func splitTerms(inner string) []memTerm {
	var terms []memTerm
	start := 0
	neg := false
	for i := 0; i < len(inner); i++ {
		if inner[i] == '+' || inner[i] == '-' {
			if i > start {
				terms = append(terms, memTerm{neg: neg, tok: inner[start:i]})
			}
			neg = inner[i] == '-'
			start = i + 1
		}
	}
	if start < len(inner) {
		terms = append(terms, memTerm{neg: neg, tok: inner[start:]})
	}
	return terms
}

func isLabelName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == '.':
		case i > 0 && (c >= '0' && c <= '9' || c == '$'):
		default:
			return false
		}
	}
	return true
}

// stripComment removes a trailing GAS comment (# or //) that is not
// inside a string literal.
func stripComment(line string) string {
	inStr := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '"' && (i == 0 || line[i-1] != '\\') {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		if c == '#' {
			return line[:i]
		}
		if c == '/' && i+1 < len(line) && line[i+1] == '/' {
			return line[:i]
		}
	}
	return line
}

// splitLabel peels a leading "name:" label off the line.
func splitLabel(line string) (label, rest string, ok bool) {
	line = strings.TrimSpace(line)
	i := strings.IndexByte(line, ':')
	if i <= 0 {
		return "", "", false
	}
	name := line[:i]
	if !isLabelName(name) {
		return "", "", false
	}
	return name, line[i+1:], true
}

func splitMnemonic(line string) (mnem, rest string) {
	line = strings.TrimSpace(line)
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return line, ""
	}
	return line[:i], strings.TrimSpace(line[i:])
}

func splitOperands(rest string) []string {
	if rest == "" {
		return nil
	}
	parts := strings.Split(rest, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func jccCode(mnem string) (byte, bool) {
	m := map[string]byte{
		"jo": 0, "jno": 1, "jb": 2, "jc": 2, "jnae": 2,
		"jae": 3, "jnb": 3, "jnc": 3, "je": 4, "jz": 4,
		"jne": 5, "jnz": 5, "jbe": 6, "jna": 6, "ja": 7, "jnbe": 7,
		"js": 8, "jns": 9, "jp": 10, "jnp": 11,
		"jl": 12, "jnge": 12, "jge": 13, "jnl": 13,
		"jle": 14, "jng": 14, "jg": 15, "jnle": 15,
	}
	cc, ok := m[mnem]
	return cc, ok
}

func setccCode(mnem string) (byte, bool) {
	m := map[string]byte{
		"seto": 0, "setno": 1, "setb": 2, "setc": 2, "setnae": 2,
		"setae": 3, "setnb": 3, "setnc": 3, "sete": 4, "setz": 4,
		"setne": 5, "setnz": 5, "setbe": 6, "setna": 6, "seta": 7, "setnbe": 7,
		"sets": 8, "setns": 9, "setp": 10, "setnp": 11,
		"setl": 12, "setnge": 12, "setge": 13, "setnl": 13,
		"setle": 14, "setng": 14, "setg": 15, "setnle": 15,
	}
	cc, ok := m[mnem]
	return cc, ok
}
