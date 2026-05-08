package codegen

import "strings"

// peephole performs small local rewrites on the emitted assembly text.
// The patterns each preserve semantics within a basic block and only
// look at adjacent lines in the *output* stream, so a label or branch
// between two would-be matches stops the rewrite.
//
// Patterns recognised:
//
//  1. push {r0} ; pop {r0}              → drop both        (no-op)
//  2. push {r0} ; pop {rN} (N != 0)     → mov rN, r0       (one fewer mem touch)
//  3. b LBL    ; LBL:                   → drop the branch  (fallthrough)
//  4. str rN, [A] ; ldr rN, [A]         → drop the ldr     (value already in rN)
//  5. mov rN, rN                        → drop             (no-op)
//  6. b<cc> LBL ; LBL:                  → drop the branch  (fallthrough — even
//                                                          for conditional branches,
//                                                          the next instruction is
//                                                          the fallthrough target)
//  7. mov<TC> r0, #1                    \
//     mov<FC> r0, #0                     │ collapse the cmpPop /
//     cmp r0, #0                         │ fcmpPop boolean materialise
//     b<eq|ne> LBL                       │ + branch into a single
//                                         conditional branch:
//                                          beq → b<FC>, bne → b<TC>
//                                       Saves ~3 instructions per
//                                       `if (a <cond> b)`.
//
// Run to a fixed point so cascades — e.g. dropping a branch reveals a
// new push/pop adjacency — are caught.
func peephole(asm string) string {
	for {
		next := strings.Join(peepPass(strings.Split(asm, "\n")), "\n")
		if next == asm {
			return next
		}
		asm = next
	}
}

func peepPass(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		// Single-line patterns first.
		if isSelfMov(line) {
			continue
		}

		if len(out) > 0 {
			prev := out[len(out)-1]

			// 1 + 2: push/pop fusion.
			if isPushR0(prev) {
				if r := popReg(line); r != "" {
					if r == "r0" {
						out = out[:len(out)-1]
						continue
					}
					out[len(out)-1] = "\tmov " + r + ", r0"
					continue
				}
			}

			// 3 + 6: (un)conditional branch-to-next-line.
			if t, _ := branchTarget(prev); t != "" && isLabel(line, t) {
				out = out[:len(out)-1]
				// fall through to append the label itself
			}

			// 4: store-then-load to same address into same register.
			if storeLoadSame(prev, line) {
				continue
			}
		}

		// 7: fold the cmpPop / fcmpPop materialise + branch into a
		// single conditional branch. Look back at the last three
		// emitted lines plus the current line.
		if collapsed, ok := tryCmpBranchFusion(out, line); ok {
			out = collapsed
			continue
		}

		// 8: fold a 3-operand instruction whose third operand is r0
		// against an `ldr r0, =N` (or `mov r0, #N`) two lines back.
		// Covers cmp, add, sub, and, orr, eor (imm window 0..255)
		// and lsl, asr (imm window 0..31). The const enters r0
		// from the IR's OpConstI32; pop {rM} is the binPop's lhs.
		// Common for `if (x == 0)`, `while (i < 10)`, `x + 5`,
		// `n & 0xff`, `i << 4`, etc.
		if collapsed, ok := tryRrImmFold(out, line); ok {
			out = collapsed
			continue
		}

		// 9: address-mode sink — fold `add/sub rD, rB, #N`
		// followed by `ldr rD, [rD]` (or ldrb) into a single
		// `ldr rD, [rB, #±N]`. The trailing load overwrites
		// rD, so the temp register isn't read after the load
		// and the original add/sub becomes pure overhead.
		// Same shape Cranelift's ISLE tutorial calls out as
		// "sink the offset into the addressing mode".
		if collapsed, ok := tryAddrModeSink(out, line); ok {
			out = collapsed
			continue
		}

		// 10: mov-chain elimination. The push/pop-fusion peep
		// often leaves a `mov r0, rB ; mov rA, r0` pair where
		// the first mov is dead because nothing reads r0
		// before it's reassigned. When the current line is a
		// pure r0-overwrite and no instruction between the
		// chain and now reads r0, drop the leading mov and
		// rewrite the pair into `mov rA, rB`.
		if collapsed, ok := tryMovChainElim(out, line); ok {
			out = collapsed
			out = append(out, line)
			continue
		}

		// 11: branch inversion — collapse a conditional-over-
		// unconditional branch pair when the conditional's
		// target is the *next* label:
		//
		//     b<cc> THEN
		//     b ELSE
		//   THEN:
		//
		// is equivalent to (and one instruction shorter than)
		//
		//     b<!cc> ELSE
		//   THEN:
		//
		// since whichever branch is taken lands at the same
		// place. Common in switch dispatch — every case emits
		// this exact shape from the IR's nested-block lowering.
		if collapsed, ok := tryBranchInversion(out, line); ok {
			out = collapsed
			out = append(out, line)
			continue
		}

		_ = i
		out = append(out, line)
	}
	return out
}

// tryBranchInversion recognises the three-line pattern
//
//	b<cc> THEN          (conditional branch over the next line)
//	b ELSE              (unconditional fallback)
//   THEN:               (`cur` — the conditional's target)
//
// and rewrites it to
//
//	b<!cc> ELSE         (jump to else when !cc holds; else fall through)
//   THEN:
//
// dropping the unconditional branch. Saves one instruction per
// match — every IR-level switch case dispatches through exactly
// this shape, so the per-case overhead drops from 3 lines to 2.
func tryBranchInversion(out []string, cur string) ([]string, bool) {
	if len(out) < 2 {
		return nil, false
	}
	condCC, condTarget, ok := matchBranch(out[len(out)-2])
	if !ok || condCC == "" {
		return nil, false
	}
	uncondCC, uncondTarget, ok := matchBranch(out[len(out)-1])
	if !ok || uncondCC != "" {
		return nil, false
	}
	if !isLabel(cur, condTarget) {
		return nil, false
	}
	inv, ok := invertCondCode(condCC)
	if !ok {
		return nil, false
	}
	rewritten := append(out[:len(out)-2], "\tb"+inv+" "+uncondTarget)
	return rewritten, true
}

// invertCondCode returns the opposite ARM condition code.
// All eight standard pairs are recognised; `al` (always) has
// no opposite and isn't accepted as a conditional branch.
func invertCondCode(cc string) (string, bool) {
	switch cc {
	case "eq":
		return "ne", true
	case "ne":
		return "eq", true
	case "cs":
		return "cc", true
	case "cc":
		return "cs", true
	case "mi":
		return "pl", true
	case "pl":
		return "mi", true
	case "vs":
		return "vc", true
	case "vc":
		return "vs", true
	case "hi":
		return "ls", true
	case "ls":
		return "hi", true
	case "ge":
		return "lt", true
	case "lt":
		return "ge", true
	case "gt":
		return "le", true
	case "le":
		return "gt", true
	}
	return "", false
}

// tryMovChainElim recognises an `r0` redundancy of the form
//
//	mov r0, rB
//	mov rA, r0          (A != 0)
//	[lines that don't read r0]
//	<pure r0 overwrite>     ← `cur`
//
// and rewrites the leading pair as a single `mov rA, rB`.
// Returns the lines preceding `cur` after the rewrite plus
// `true`. The caller appends `cur` itself.
//
// This is the cleanup the cmp-imm peephole's surrounding
// fusion pattern leaves behind: after `mov r0, r4 ; mov r1,
// r0 ; cmp r1, #0 ; bne L`, the first mov is dead because
// the next thing to touch r0 is whatever the then-arm starts
// with (`ldr r0, =1`, etc.), which pure-overwrites r0.
//
// Conservative: capped at a 12-line backward scan, only fires
// when no intervening instruction reads r0, and `cur` must
// be a recognised pure overwrite of r0 (not an op like
// `add r0, r0, #N` that reads r0 first).
func tryMovChainElim(out []string, cur string) ([]string, bool) {
	if !isPureR0Write(cur) {
		return nil, false
	}
	for i := len(out) - 1; i >= 0 && i >= len(out)-12; i-- {
		line := out[i]
		if dst, ok := matchMovOtherFromR0(line); ok {
			if i == 0 {
				return nil, false
			}
			src, ok := matchMovR0FromOther(out[i-1])
			if !ok {
				return nil, false
			}
			rewritten := append([]string{}, out[:i-1]...)
			rewritten = append(rewritten, "\tmov "+dst+", "+src)
			rewritten = append(rewritten, out[i+1:]...)
			return rewritten, true
		}
		if readsR0(line) || writesR0(line) {
			return nil, false
		}
	}
	return nil, false
}

// matchMovOtherFromR0 returns (rA, true) when `line` is
// `mov rA, r0` with A != 0.
func matchMovOtherFromR0(line string) (string, bool) {
	s := trim(line)
	if !strings.HasPrefix(s, "mov ") {
		return "", false
	}
	a, b := splitFirst(s[len("mov "):])
	if b != "r0" || a == "" || a == "r0" {
		return "", false
	}
	return a, true
}

// matchMovR0FromOther returns (rB, true) when `line` is
// `mov r0, rB` for some general-purpose register rB != r0.
func matchMovR0FromOther(line string) (string, bool) {
	s := trim(line)
	if !strings.HasPrefix(s, "mov r0, ") {
		return "", false
	}
	src := strings.TrimSpace(s[len("mov r0, "):])
	if src == "" || src == "r0" {
		return "", false
	}
	if !strings.HasPrefix(src, "r") {
		return "", false
	}
	return src, true
}

// isPureR0Write reports whether `line` writes r0 without
// reading it. The whitelist below covers the codegen patterns
// that arise after the existing peeps run; anything else
// stays as-is and the chain-elim fold won't fire.
func isPureR0Write(line string) bool {
	s := trim(line)
	switch {
	case strings.HasPrefix(s, "ldr r0, ="):
		// `ldr r0, =CONST` — pure write from the literal pool.
		return true
	case strings.HasPrefix(s, "mov r0, #"):
		// `mov r0, #imm` — pure immediate write.
		return true
	case strings.HasPrefix(s, "mov r0, "):
		// `mov r0, rN` — pure register copy iff rN != r0
		// (self-mov is already dropped by an earlier rule).
		src := strings.TrimSpace(s[len("mov r0, "):])
		return src != "r0" && strings.HasPrefix(src, "r")
	case s == "pop {r0}":
		return true
	case strings.HasPrefix(s, "ldr r0, ["):
		// `ldr r0, [base, ...]` — pure iff base != r0.
		// Strip the bracket and check the first token.
		inner := s[len("ldr r0, ["):]
		end := strings.IndexByte(inner, ']')
		if end < 0 {
			return false
		}
		operand := inner[:end]
		base, _ := splitFirst(operand)
		if base == "" {
			base = strings.TrimSpace(operand)
		}
		return base != "r0"
	}
	return false
}

// readsR0 reports whether `line` references r0 in any
// operand position (we don't distinguish source from
// destination here — anything that touches r0 breaks the
// dead-r0 chain we're trying to fold).
func readsR0(line string) bool {
	s := trim(line)
	// Quick reject: lines that don't mention r0 at all.
	if !strings.Contains(s, "r0") {
		return false
	}
	// Walk the line splitting on spaces / commas / brackets.
	tokens := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ' ', ',', '[', ']', '!', '\t':
			return true
		}
		return false
	})
	for i, t := range tokens {
		if t != "r0" {
			continue
		}
		// Skip the first operand for opcodes that write their
		// first operand without reading it.
		if i == 1 && isWriteFirstOp(tokens[0]) {
			continue
		}
		return true
	}
	return false
}

// writesR0 reports whether `line` writes r0 (any addressing
// shape). Used to bound the dead-chain scan: once r0 is
// reassigned, the chain we're tracking is gone.
func writesR0(line string) bool {
	s := trim(line)
	if !strings.Contains(s, "r0") {
		return false
	}
	tokens := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ' ', ',', '[', ']', '!', '\t':
			return true
		}
		return false
	})
	if len(tokens) < 2 {
		return false
	}
	return isWriteFirstOp(tokens[0]) && tokens[1] == "r0"
}

// isWriteFirstOp reports whether `mn` writes its first
// operand. The whitelist covers the integer / address-compute
// mnemonics our codegen emits; ops not on the list (cmp,
// str, push, etc.) read all operands.
func isWriteFirstOp(mn string) bool {
	// Strip optional condition-code suffix from `b<cc>` /
	// `mov<cc>` etc. — we only care about the base mnemonic.
	for _, suffix := range []string{"eq", "ne", "cs", "cc", "mi", "pl", "vs", "vc", "hi", "ls", "ge", "lt", "gt", "le", "al"} {
		if strings.HasSuffix(mn, suffix) && len(mn) > len(suffix) {
			mn = mn[:len(mn)-len(suffix)]
		}
	}
	switch mn {
	case "mov", "mvn", "ldr", "ldrb", "ldrh",
		"add", "sub", "rsb", "and", "orr", "eor", "bic",
		"mul", "lsl", "lsr", "asr", "ror",
		"sdiv", "udiv", "mls", "neg",
		"vmov", "vadd.f32", "vsub.f32", "vmul.f32", "vdiv.f32", "vneg.f32":
		return true
	}
	return false
}

// tryAddrModeSink recognises the two-line shape
//
//	add  rD, rB, #N        (or `sub rD, rB, #N`)
//	ldr  rD, [rD]          (or `ldrb`)
//
// and rewrites it to
//
//	ldr  rD, [rB, #N]      (negated for sub)
//
// dropping the address compute. The trailing load overwrites
// rD, so the add/sub's only consumer is gone — the original
// instruction becomes dead. Stores are intentionally not
// folded: `str rX, [rD]` doesn't write rD, so subsequent code
// might still read it, and verifying that requires liveness.
//
// Offset window is 0..255 (the same conservative window the
// preceding tryRrImmFold uses). ARMv7-A actually accepts
// -4095..4095 for word/byte loads, so we leave headroom for
// future widening if a hot path appears.
func tryAddrModeSink(out []string, cur string) ([]string, bool) {
	if len(out) < 1 {
		return nil, false
	}
	prev := out[len(out)-1]
	op, dst, base, imm, ok := matchAddSubImm(prev)
	if !ok {
		return nil, false
	}
	loadOp, ld, addrReg, ok := matchLoadIndirect(cur)
	if !ok || ld != dst || addrReg != dst {
		return nil, false
	}
	signed := imm
	if op == "sub" {
		signed = -imm
	}
	if signed < -4095 || signed > 4095 {
		return nil, false
	}
	rewrittenLine := "\t" + loadOp + " " + ld + ", [" + base + ", #" + signedItoa(signed) + "]"
	rewritten := append(out[:len(out)-1], rewrittenLine)
	return rewritten, true
}

// matchAddSubImm peels back `add rD, rB, #N` or `sub rD, rB, #N`,
// returning the opcode, destination, base, and immediate.
func matchAddSubImm(line string) (op, dst, base string, imm int, ok bool) {
	s := trim(line)
	switch {
	case strings.HasPrefix(s, "add "):
		op = "add"
		s = s[len("add "):]
	case strings.HasPrefix(s, "sub "):
		op = "sub"
		s = s[len("sub "):]
	default:
		return "", "", "", 0, false
	}
	first, rest := splitFirst(s)
	second, third := splitFirst(rest)
	if first == "" || second == "" || !strings.HasPrefix(third, "#") {
		return "", "", "", 0, false
	}
	n, ok := parseDecimal(third[1:])
	if !ok {
		return "", "", "", 0, false
	}
	return op, first, second, n, true
}

// matchLoadIndirect peels back `ldr rD, [rA]` (or ldrb) with
// no offset — the candidate for sinking an address compute
// into. Returns the opcode, destination, and the bracketed
// register.
func matchLoadIndirect(line string) (op, dst, base string, ok bool) {
	s := trim(line)
	switch {
	case strings.HasPrefix(s, "ldr "):
		op = "ldr"
		s = s[len("ldr "):]
	case strings.HasPrefix(s, "ldrb "):
		op = "ldrb"
		s = s[len("ldrb "):]
	default:
		return "", "", "", false
	}
	dst, rest := splitFirst(s)
	if dst == "" || !strings.HasPrefix(rest, "[") || !strings.HasSuffix(rest, "]") {
		return "", "", "", false
	}
	inner := rest[1 : len(rest)-1]
	if strings.ContainsAny(inner, ",") {
		// Already has an offset — leave alone.
		return "", "", "", false
	}
	return op, dst, inner, true
}

// parseDecimal accepts a non-negative decimal integer string
// and returns its value. Leading sign / hex / negative not
// accepted — the callers of tryAddrModeSink only feed it the
// `#N` form arith-imm peeps emit.
func parseDecimal(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 1<<30 {
			return 0, false
		}
	}
	return n, true
}

// signedItoa renders a (possibly negative) integer in decimal
// — the bare itoa elsewhere only handles non-negatives.
func signedItoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	return itoa(n)
}

// tryRrImmFold folds a 3-operand register/register/r0
// instruction against a preceding `ldr r0, =N` (or
// `mov r0, #N`) by rewriting r0 → #N. Recognises the
// three-line shape:
//
//	ldr r0, =N         (or `mov r0, #N`)
//	pop {rM}           (M != 0)
//	<op> [rD,] rM, r0
//
// and rewrites it to
//
//	pop {rM}
//	<op> [rD,] rM, #N
//
// dropping the const-load. The const enters r0 from the IR's
// OpConstI32 (followed by the binPop's pop {r0}; the existing
// push/pop fusion has already collapsed that pair down to
// just the load). Common cases:
//
//	cmp r1, r0          → cmp r1, #N         (if (x == N))
//	add r0, r1, r0      → add r0, r1, #N     (x + N)
//	sub r0, r1, r0      → sub r0, r1, #N     (x - N)
//	and r0, r1, r0      → and r0, r1, #N     (x & N)
//	orr r0, r1, r0      → orr r0, r1, #N     (x | N)
//	eor r0, r1, r0      → eor r0, r1, #N     (x ^ N)
//	lsl r0, r1, r0      → lsl r0, r1, #N     (x << N)
//	asr r0, r1, r0      → asr r0, r1, #N     (x >> N)
//
// Encoding windows: 0..255 for the data-processing ops (a
// conservative subset of ARM's rotated-imm encoding so we
// don't have to validate the rotation), 0..31 for shifts
// (the full immediate range the encoding supports).
func tryRrImmFold(out []string, cur string) ([]string, bool) {
	if len(out) < 2 {
		return nil, false
	}
	loadLine := out[len(out)-2]
	popLine := out[len(out)-1]
	imm, ok := matchLoadConstR0(loadLine)
	if !ok {
		return nil, false
	}
	popReg := matchPop(popLine)
	if popReg == "" || popReg == "r0" {
		// pop into r0 would overwrite the const we just loaded.
		return nil, false
	}
	op, dst, src, ok := matchRrInstr(cur, popReg)
	if !ok {
		return nil, false
	}
	max, ok := immWindow(op)
	if !ok || imm < 0 || imm > max {
		return nil, false
	}
	var rewrittenLine string
	if dst == "" {
		// Two-operand form (cmp / cmn / tst / teq).
		rewrittenLine = "\t" + op + " " + src + ", #" + itoa(imm)
	} else {
		rewrittenLine = "\t" + op + " " + dst + ", " + src + ", #" + itoa(imm)
	}
	rewritten := append(out[:len(out)-2], popLine, rewrittenLine)
	return rewritten, true
}

// matchRrInstr peels back a 3-operand `<op> rD, rN, r0` (or
// 2-operand `<op> rN, r0` for cmp-shaped instructions). When
// the source register matches `popReg`, returns the opcode,
// optional destination, source, and ok=true.
func matchRrInstr(line, popReg string) (op, dst, src string, ok bool) {
	s := trim(line)
	sp := strings.IndexByte(s, ' ')
	if sp < 0 {
		return "", "", "", false
	}
	mn := s[:sp]
	rest := s[sp+1:]
	switch mn {
	case "cmp":
		// cmp rN, r0
		a, b := splitFirst(rest)
		if a != popReg || b != "r0" {
			return "", "", "", false
		}
		return mn, "", a, true
	case "add", "sub", "and", "orr", "eor", "lsl", "asr":
		// op rD, rN, r0
		first, rest2 := splitFirst(rest)
		if first == "" {
			return "", "", "", false
		}
		second, third := splitFirst(rest2)
		if second != popReg || third != "r0" {
			return "", "", "", false
		}
		return mn, first, second, true
	}
	return "", "", "", false
}

// immWindow is the conservative immediate range we accept for
// each foldable opcode. Data-processing ops (add/sub/and/etc.)
// use 0..255 — strictly within ARM's 8-bit rotated-imm
// encoding so no rotation analysis is needed. Shifts use
// 0..31, the full encoding window for `lsl rD, rN, #imm`
// and `asr rD, rN, #imm`. cmp shares the data-processing
// window.
func immWindow(op string) (int, bool) {
	switch op {
	case "cmp", "add", "sub", "and", "orr", "eor":
		return 255, true
	case "lsl", "asr":
		return 31, true
	}
	return 0, false
}

// matchLoadConstR0 returns (N, true) when `line` is one of
//   - `ldr r0, =N`
//   - `mov r0, #N`
// for some non-negative integer N. The two forms are
// equivalent at runtime — the assembler picks `mov` when N
// fits the encoding. We accept both for symmetry across
// hand-written and gas-rewritten code.
func matchLoadConstR0(line string) (int, bool) {
	s := trim(line)
	if v, ok := parseAfter(s, "ldr r0, ="); ok {
		return v, true
	}
	if v, ok := parseAfter(s, "mov r0, #"); ok {
		return v, true
	}
	return 0, false
}

func matchPop(line string) string {
	return popReg(line)
}

// parseAfter returns (n, true) when s begins with prefix and
// what follows is a non-negative decimal integer. Used to
// pull the immediate out of `ldr r0, =N` / `mov r0, #N`.
func parseAfter(s, prefix string) (int, bool) {
	if !strings.HasPrefix(s, prefix) {
		return 0, false
	}
	rest := s[len(prefix):]
	if rest == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 1<<30 {
			return 0, false
		}
	}
	return n, true
}

// itoa is the int → decimal string for the small immediates
// the cmp-against-const peephole emits. The standard library
// strconv.Itoa would work, but a tiny inline avoids pulling
// strconv into this otherwise-string-handling-only file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// tryCmpBranchFusion recognises the four-line shape
//
//	mov<TC> r0, #1
//	mov<FC> r0, #0
//	cmp r0, #0
//	b<eq|ne> LBL
//
// where <TC> and <FC> are opposite condition codes (the
// cmpPop / fcmpPop helpers emit them in pairs). The mov pair
// materialises the boolean result of an integer or VFP
// comparison; the cmp + b<cc> then immediately tests it.
// Together they're a 4-instruction "branch on comparison
// result" — collapsible to a single `b<TC|FC> LBL` because the
// preceding `cmp Rn, Rm` (or `vcmp.f32` + `vmrs`) already set
// the flags we need.
//
// `out` is the lines emitted so far; `cur` is the current line
// (the candidate `b<eq|ne>`). On a match returns the new tail
// (with the three mov-pair / cmp lines dropped and the branch
// rewritten) and true.
func tryCmpBranchFusion(out []string, cur string) ([]string, bool) {
	if len(out) < 3 {
		return nil, false
	}
	movTrue, ok1 := matchMov(out[len(out)-3], "#1")
	movFalse, ok2 := matchMov(out[len(out)-2], "#0")
	if !ok1 || !ok2 {
		return nil, false
	}
	if trim(out[len(out)-1]) != "cmp r0, #0" {
		return nil, false
	}
	branchCC, label, ok3 := matchBranch(cur)
	if !ok3 {
		return nil, false
	}
	var newCC string
	switch branchCC {
	case "eq":
		newCC = movFalse
	case "ne":
		newCC = movTrue
	default:
		return nil, false
	}
	rewritten := append(out[:len(out)-3], "\tb"+newCC+" "+label)
	return rewritten, true
}

// matchMov returns (cc, true) when `line` is `mov<cc> r0, <imm>`.
// `wantImm` filters by the immediate; pass "" to accept any.
func matchMov(line, wantImm string) (string, bool) {
	s := trim(line)
	if !strings.HasPrefix(s, "mov") {
		return "", false
	}
	const dst = " r0, "
	idx := strings.Index(s, dst)
	if idx < 0 {
		return "", false
	}
	cc := s[len("mov"):idx]
	imm := s[idx+len(dst):]
	if wantImm != "" && imm != wantImm {
		return "", false
	}
	if !isCondCode(cc) {
		return "", false
	}
	return cc, true
}

// matchBranch returns (cc, label, true) when `line` is a
// `b<cc> LABEL` form. Plain `b LABEL` (unconditional) returns
// cc = "".
func matchBranch(line string) (cc, label string, ok bool) {
	s := trim(line)
	if !strings.HasPrefix(s, "b") {
		return "", "", false
	}
	rest := s[1:]
	// b<cc> LABEL — find the space.
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return "", "", false
	}
	cc = rest[:sp]
	label = strings.TrimSpace(rest[sp+1:])
	if cc != "" && !isCondCode(cc) {
		return "", "", false
	}
	if label == "" {
		return "", "", false
	}
	return cc, label, true
}

// isCondCode reports whether s is one of the ARM condition-code
// suffixes that follow a base mnemonic like `mov` or `b`.
func isCondCode(s string) bool {
	switch s {
	case "eq", "ne", "cs", "cc", "mi", "pl", "vs", "vc",
		"hi", "ls", "ge", "lt", "gt", "le", "al":
		return true
	}
	return false
}

// ---------- predicates ----------

func trim(s string) string { return strings.TrimSpace(s) }

func isPushR0(line string) bool { return trim(line) == "push {r0}" }

// popReg returns the single register `pop {rN}` is loading into, or ""
// if the line isn't a single-register pop.
func popReg(line string) string {
	s := trim(line)
	if !strings.HasPrefix(s, "pop {") || !strings.HasSuffix(s, "}") {
		return ""
	}
	inner := s[len("pop {") : len(s)-1]
	if strings.ContainsAny(inner, ", ") {
		return ""
	}
	return inner
}

func branchTarget(line string) (string, string) {
	s := trim(line)
	if !strings.HasPrefix(s, "b") {
		return "", ""
	}
	// Strip the base 'b' and any condition-code suffix, leaving a
	// space-separated label after.
	rest := s[1:]
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return "", ""
	}
	cc := rest[:sp]
	if cc != "" && !isCondCode(cc) {
		return "", ""
	}
	return strings.TrimSpace(rest[sp+1:]), cc
}

func isLabel(line, name string) bool { return trim(line) == name+":" }

func storeLoadSame(prev, cur string) bool {
	p := trim(prev)
	c := trim(cur)
	if !strings.HasPrefix(p, "str ") || !strings.HasPrefix(c, "ldr ") {
		return false
	}
	pReg, pAddr := splitFirst(p[len("str "):])
	cReg, cAddr := splitFirst(c[len("ldr "):])
	return pReg == cReg && pAddr == cAddr && pAddr != ""
}

func isSelfMov(line string) bool {
	s := trim(line)
	if !strings.HasPrefix(s, "mov ") {
		return false
	}
	a, b := splitFirst(s[len("mov "):])
	return a != "" && a == b
}

// splitFirst splits "rN, <rest>" into ("rN", "<rest>").
func splitFirst(s string) (string, string) {
	i := strings.Index(s, ", ")
	if i < 0 {
		return "", ""
	}
	return s[:i], s[i+2:]
}
