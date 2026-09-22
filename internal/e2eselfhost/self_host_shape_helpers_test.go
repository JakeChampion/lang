package e2eselfhost

import (
	"regexp"
	"strings"
	"testing"
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

// shapeCase is one program with, per function, the line patterns its body
// must match and the ones it must not.
type shapeCase struct {
	name string
	src  string
	want int
	// fn -> patterns that must match the body
	has map[string][]string
	// fn -> patterns that must not
	lacks map[string][]string
}

func runShapeCases(t *testing.T, emit func(t *testing.T, src string) string, run func(t *testing.T, name, asm string) int, cases []shapeCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := emit(t, tc.src)
			for fn, pats := range tc.has {
				body := shapeFnBody(t, asm, fn)
				for _, p := range pats {
					if !matchShape(body, p) {
						t.Errorf("%s: no line matches %q:\n%s", fn, p, body)
					}
				}
			}
			for fn, pats := range tc.lacks {
				body := shapeFnBody(t, asm, fn)
				for _, p := range pats {
					if matchShape(body, p) {
						t.Errorf("%s: still carries %q:\n%s", fn, p, body)
					}
				}
			}
			if got := run(t, tc.name, asm); got != tc.want {
				t.Errorf("exit = %d, want %d", got, tc.want)
			}
		})
	}
}
