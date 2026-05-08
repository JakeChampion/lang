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

		// 8: fold `cmp rN, r0` immediately preceded by
		// `ldr r0, =CONST` (or `mov r0, #CONST`) into
		// `cmp rN, #CONST`, dropping the load. Common for
		// `if (x == 0)`, `while (i < 10)` etc. — the const
		// is the OpConstI32 operand pushed from the IR's
		// stack-machine binPop. Only fires when the const
		// fits the simple 0..255 immediate window so we
		// don't have to reason about ARM's rotated-imm
		// encoding.
		if collapsed, ok := tryCmpAgainstConst(out, line); ok {
			out = collapsed
			continue
		}

		_ = i
		out = append(out, line)
	}
	return out
}

// tryCmpAgainstConst recognises the three-line shape
//
//	ldr r0, =N         (or `mov r0, #N`)
//	pop {rM}           (M != 0)
//	cmp rM, r0
//
// and rewrites it to
//
//	pop {rM}
//	cmp rM, #N
//
// dropping the const-load entirely. The const enters r0 from
// the IR's OpConstI32 (followed by the binPop's pop {r0}; the
// existing push/pop fusion collapses that pair down to just
// the load). Only safe when N fits in the simple 0..255
// immediate window — above that we'd need to verify the
// encoding rules (8-bit rotated by even amounts) and the win
// is marginal anyway.
func tryCmpAgainstConst(out []string, cur string) ([]string, bool) {
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
	cmpReg, ok := matchCmpAgainstR0(cur)
	if !ok || cmpReg != popReg {
		return nil, false
	}
	if imm < 0 || imm > 255 {
		return nil, false
	}
	rewritten := append(out[:len(out)-2], popLine)
	rewritten = append(rewritten, "\tcmp "+cmpReg+", #"+itoa(imm))
	return rewritten, true
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

// matchCmpAgainstR0 returns the *other* register when `line`
// is `cmp rN, r0` (N != 0). The form `cmp r0, r0` self-
// compares and is left alone.
func matchCmpAgainstR0(line string) (string, bool) {
	s := trim(line)
	if !strings.HasPrefix(s, "cmp ") {
		return "", false
	}
	a, b := splitFirst(s[len("cmp "):])
	if b != "r0" || a == "" || a == "r0" {
		return "", false
	}
	return a, true
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
