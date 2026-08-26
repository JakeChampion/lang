package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// DirectiveRule is the reserved name findings about the lint directives
// THEMSELVES are reported under — a misspelled rule in an `allow` comment
// silences nothing, and silence is exactly what the author asked for, so
// nothing else would ever surface the typo.
const DirectiveRule = "lint-directive"

// directivePrefix introduces a lint comment: `// fern-lint: allow NAME`.
const directivePrefix = "fern-lint:"

// suppressions is the per-file set of `allow` directives, resolved to the
// lines they cover.
type suppressions struct {
	// file names rules allowed for the whole file (`allow-file`).
	file map[string]bool
	// lines maps a covered line to the rules allowed on it.
	lines map[int]map[string]bool
	// bad collects directives that name no known rule, so the driver can
	// report them instead of letting a typo silently disable nothing.
	bad []Finding
}

func (s *suppressions) allows(rule string, line int) bool {
	if s == nil {
		return false
	}
	if s.file[rule] {
		return true
	}
	return s.lines[line][rule]
}

// collectSuppressions reads every `// fern-lint:` comment in prog and
// resolves which lines it covers.
//
// A directive sitting alone on its line covers the next line carrying
// code — skipping blank lines and further comments, so an `allow` may sit
// above a function's doc comment rather than wedged between the two. A
// directive trailing code covers the line it sits on.
func collectSuppressions(src string, prog *ast.Program) *suppressions {
	s := &suppressions{file: map[string]bool{}, lines: map[int]map[string]bool{}}
	if prog == nil {
		return s
	}
	code := newCodeLines(src, prog)
	for _, c := range prog.Comments {
		text := strings.TrimSpace(c.Text)
		if !strings.HasPrefix(text, directivePrefix) {
			continue
		}
		verb, rules, err := parseDirective(strings.TrimSpace(strings.TrimPrefix(text, directivePrefix)))
		if err != nil {
			s.bad = append(s.bad, Finding{Rule: DirectiveRule, Severity: Warn, Pos: c.Pos, Msg: err.Error()})
			continue
		}
		for _, r := range rules {
			if _, ok := registry[r]; !ok {
				s.bad = append(s.bad, Finding{
					Rule:     DirectiveRule,
					Severity: Warn,
					Pos:      c.Pos,
					Msg:      fmt.Sprintf("`allow` names no such lint rule: %q", r),
					Help:     knownRules(),
				})
				continue
			}
			switch verb {
			case "allow-file":
				s.file[r] = true
			case "allow":
				target := code.after(c)
				if target == 0 {
					s.bad = append(s.bad, Finding{
						Rule:     DirectiveRule,
						Severity: Warn,
						Pos:      c.Pos,
						Msg:      fmt.Sprintf("`allow %s` covers no code — nothing follows this comment", r),
						Help:     "use `fern-lint: allow-file` to silence the rule for the whole file",
					})
					continue
				}
				if s.lines[target] == nil {
					s.lines[target] = map[string]bool{}
				}
				s.lines[target][r] = true
			}
		}
	}
	sort.SliceStable(s.bad, func(i, j int) bool { return s.bad[i].Pos.Line < s.bad[j].Pos.Line })
	return s
}

// parseDirective splits `allow rule-a, rule-b` into its verb and rules.
func parseDirective(body string) (verb string, rules []string, err error) {
	verb, rest, _ := strings.Cut(body, " ")
	switch verb {
	case "allow", "allow-file":
	default:
		return "", nil, fmt.Errorf("unknown lint directive %q (want `fern-lint: allow RULE` or `fern-lint: allow-file RULE`)", verb)
	}
	for _, r := range strings.Split(rest, ",") {
		if r = strings.TrimSpace(r); r != "" {
			rules = append(rules, r)
		}
	}
	if len(rules) == 0 {
		return "", nil, fmt.Errorf("lint directive `%s` names no rule", verb)
	}
	return verb, rules, nil
}

// codeLines answers "which lines carry code" so a standalone directive can
// find the construct it is about. A line carries code when it is neither
// blank nor comment-only; both are already recorded by the lexer, so this
// needs no second tokenisation of the source.
type codeLines struct {
	total int
	// commentOnly[line] is true when the line's only content is a comment.
	commentOnly map[int]bool
	blank       map[int]bool
	// firstComment[line] is the column of the line's leftmost comment.
	firstComment map[int]int
}

func newCodeLines(src string, prog *ast.Program) codeLines {
	c := codeLines{
		total:        strings.Count(src, "\n") + 1,
		commentOnly:  map[int]bool{},
		blank:        map[int]bool{},
		firstComment: map[int]int{},
	}
	for _, n := range prog.BlankLines {
		c.blank[n] = true
	}
	lines := strings.Split(src, "\n")
	for _, cm := range prog.Comments {
		if col, seen := c.firstComment[cm.Pos.Line]; !seen || cm.Pos.Col < col {
			c.firstComment[cm.Pos.Line] = cm.Pos.Col
		}
		if cm.Pos.Line >= 1 && cm.Pos.Line-1 < len(lines) {
			line := lines[cm.Pos.Line-1]
			if before := cm.Pos.Col - 1; before >= 0 && before <= len(line) && strings.TrimSpace(line[:before]) == "" {
				c.commentOnly[cm.Pos.Line] = true
			}
		}
	}
	return c
}

// after returns the line the directive at cm covers, or 0 when none does.
func (c codeLines) after(cm ast.Comment) int {
	if !c.commentOnly[cm.Pos.Line] {
		// Trailing comment: it annotates the code it sits behind.
		return cm.Pos.Line
	}
	for ln := cm.Pos.Line + 1; ln <= c.total; ln++ {
		if c.blank[ln] || c.commentOnly[ln] {
			continue
		}
		return ln
	}
	return 0
}
