package arm64ssa

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The allocatable register file now extends into x19..x28, so a value the
// allocator steered there is live across every `bl` the function makes —
// including the calls into this file's own runtime helpers. A helper that
// writes one of those registers without saving it therefore silently corrupts a
// caller's live value, with nothing failing until unrelated code reads it.
//
// The check over-approximates: a helper that so much as MENTIONS a callee-saved
// register must also save and restore it. Reading one it never saved is a bug
// on its own (nothing else put a value there for it), so there is no legitimate
// mention to exempt.
func TestRuntimeHelpersPreserveCalleeSaved(t *testing.T) {
	emitters := map[string]func(w func(string, ...any)){
		// The bump-heap guard is reached by a `bl` planted inline in a function
		// body (heapGuardCallLines), so it is under the same obligation as the
		// named helpers without being one of them.
		heapGuardSym: emitHeapGuard,
	}
	for name, fn := range runtimeHelperEmitters {
		emitters[name] = fn
	}
	names := make([]string, 0, len(emitters))
	for name := range emitters {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var b strings.Builder
			w := func(format string, args ...any) {
				fmt.Fprintf(&b, format, args...)
				b.WriteByte('\n')
			}
			emitters[name](w)
			asm := b.String()
			for r := firstCalleeSaved; r < maxAlloc; r++ {
				x, wr := armX[r], armW[r]
				if !mentionsReg(asm, x) && !mentionsReg(asm, wr) {
					continue
				}
				if !savesReg(asm, x) {
					t.Errorf("helper %s uses %s/%s but never saves it", name, x, wr)
				}
				if !restoresReg(asm, x) {
					t.Errorf("helper %s uses %s/%s but never restores it", name, x, wr)
				}
			}
		})
	}
}

// savesReg / restoresReg look for the register in a destination-of-store
// position: `str reg,` / `stp reg, other,` / `stp other, reg,` and the ldr/ldp
// mirror.
func savesReg(asm, reg string) bool    { return hasMemPairOp(asm, "st", reg) }
func restoresReg(asm, reg string) bool { return hasMemPairOp(asm, "ld", reg) }

func hasMemPairOp(asm, prefix, reg string) bool {
	single := regexp.MustCompile(`\b` + prefix + `r\s+` + reg + `\s*,`)
	pairFirst := regexp.MustCompile(`\b` + prefix + `p\s+` + reg + `\s*,`)
	pairSecond := regexp.MustCompile(`\b` + prefix + `p\s+\w+\s*,\s*` + reg + `\s*,`)
	return single.MatchString(asm) || pairFirst.MatchString(asm) || pairSecond.MatchString(asm)
}
