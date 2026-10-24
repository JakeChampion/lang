package e2ecompiler

import (
	"regexp"
	"strings"
	"testing"
)

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

func arm64Imm(asm, n string) bool {
	return regexp.MustCompile(`\n\s+mov x[0-9]+, #` + regexp.QuoteMeta(n) + `\n`).MatchString(asm)
}

var (
	rcIncInlineRe      = regexp.MustCompile(`_rcinc[0-9]+:`)
	rcIsUniqueInlineRe = regexp.MustCompile(`_(?:rcuniq|uq)[0-9]+:`)
)

func rcIncSites(asm string) int {
	return strings.Count(asm, "call __fn___fern_rc_inc") + strings.Count(asm, "bl __fn___fern_rc_inc") + len(rcIncInlineRe.FindAllString(asm, -1))
}

func rcIsUniqueSites(asm string) int {
	return strings.Count(asm, "call __fn___fern_rc_is_unique") + strings.Count(asm, "bl __fn___fern_rc_is_unique") + len(rcIsUniqueInlineRe.FindAllString(asm, -1))
}

type shapeCase struct {
	name  string
	src   string
	want  int
	has   map[string][]string
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
