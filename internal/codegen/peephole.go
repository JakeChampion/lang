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
	for _, line := range lines {
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

			// 3: branch-to-next-line.
			if t := branchTarget(prev); t != "" && isLabel(line, t) {
				out = out[:len(out)-1]
				// fall through to append the label itself
			}

			// 4: store-then-load to same address into same register.
			if storeLoadSame(prev, line) {
				continue
			}
		}
		out = append(out, line)
	}
	return out
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

func branchTarget(line string) string {
	s := trim(line)
	if !strings.HasPrefix(s, "b ") {
		return ""
	}
	return trim(s[2:])
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
