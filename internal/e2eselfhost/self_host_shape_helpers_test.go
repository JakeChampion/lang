package e2eselfhost

import (
	"regexp"
	"strings"
)

// Shape helpers for the emitted-text gates. The register path leaves the
// choice of register to the allocator, so a gate names the instruction and
// leaves the register open; and it renders the two rc primitives inline, so
// a retain or a uniqueness test is counted by its label as well as by the
// call it replaces.

// matchShape reports whether body matches pattern. RE2 has no back-references,
// so a pattern's `\1` (the register its first group captured, where two
// operands must agree) is resolved by hand: every match of the pattern with
// the back-references loosened to any operand proposes the registers, and the
// pattern is re-run with those spelled out.
func matchShape(body, pattern string) bool {
	backref := regexp.MustCompile(`\\([1-9])`)
	if !backref.MatchString(pattern) {
		return regexp.MustCompile(pattern).MatchString(body)
	}
	loose := regexp.MustCompile(backref.ReplaceAllString(pattern, `[^\s,]+`))
	for _, m := range loose.FindAllStringSubmatch(body, -1) {
		exact := backref.ReplaceAllStringFunc(pattern, func(ref string) string {
			return regexp.QuoteMeta(m[int(ref[1]-'0')])
		})
		if regexp.MustCompile(exact).MatchString(body) {
			return true
		}
	}
	return false
}

// arm64Imm reports whether asm materialises the integer n as a MOVZ
// immediate, in whichever register the allocator chose.
func arm64Imm(asm, n string) bool {
	return regexp.MustCompile(`\n\s+mov x[0-9]+, #` + regexp.QuoteMeta(n) + `\n`).MatchString(asm)
}

var (
	rcIncInlineRe      = regexp.MustCompile(`_rcinc[0-9]+:`)
	rcIsUniqueInlineRe = regexp.MustCompile(`_rcuniq[0-9]+:`)
)

// rcIncSites counts the retains in asm: calls to the rc_inc helper and the
// inline form both register paths render (its label ends in `_rcinc<N>`).
func rcIncSites(asm string) int {
	return strings.Count(asm, "call __fn___fern_rc_inc") + strings.Count(asm, "bl __fn___fern_rc_inc") + len(rcIncInlineRe.FindAllString(asm, -1))
}

// rcIsUniqueSites counts the uniqueness tests in asm, calls and inline forms
// (label `_rcuniq<N>`) alike.
func rcIsUniqueSites(asm string) int {
	return strings.Count(asm, "call __fn___fern_rc_is_unique") + strings.Count(asm, "bl __fn___fern_rc_is_unique") + len(rcIsUniqueInlineRe.FindAllString(asm, -1))
}
