package lint_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/lint"
	"github.com/jakechampion/lang/internal/parser"
)

// A function that scores 3, for a config whose limit is 2.
const noisyFn = `function noisy(n: i32): i32 {
    if (n > 0) { return 1; }
    if (n < 0) { return 2; }
    return 0;
}
`

func lintSrc(t *testing.T, src string) []lint.Finding {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, src)
	}
	cfg := lint.NewConfig()
	if err := cfg.SetOption("cyclomatic-complexity.max", "2"); err != nil {
		t.Fatal(err)
	}
	fs, err := lint.File(cfg, "t.fern", src, prog)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func rulesOf(fs []lint.Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Rule
	}
	return out
}

func TestSuppressionForms(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int // findings expected
	}{
		{"no directive", noisyFn, 1},
		{"directive on the line above", "// fern-lint: allow cyclomatic-complexity\n" + noisyFn, 0},
		{
			// The `allow` may sit above a doc comment rather than wedged
			// between the doc and the function it documents.
			name: "directive above a doc comment",
			src:  "// fern-lint: allow cyclomatic-complexity\n// noisy dispatches a flat table.\n\n" + noisyFn,
			want: 0,
		},
		{
			name: "directive naming several rules",
			src:  "// fern-lint: allow cyclomatic-complexity, cyclomatic-complexity\n" + noisyFn,
			want: 0,
		},
		{
			name: "allow-file covers a function further down",
			src:  "// fern-lint: allow-file cyclomatic-complexity\nfunction quiet(): i32 { return 0; }\n" + noisyFn,
			want: 0,
		},
		{
			// A directive covers ONE site, not the rest of the file.
			name: "allow covers only the next construct",
			src:  "// fern-lint: allow cyclomatic-complexity\n" + noisyFn + "\n" + strings.Replace(noisyFn, "noisy", "noisier", 1),
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := lintSrc(t, tc.src)
			if len(fs) != tc.want {
				t.Errorf("got %d findings %v, want %d", len(fs), rulesOf(fs), tc.want)
			}
		})
	}
}

// A directive naming a rule that does not exist silences nothing — and
// silence is what the author asked for, so nothing else would ever surface
// the typo. It has to be reported.
func TestSuppressionReportsBadDirectives(t *testing.T) {
	cases := []struct {
		name    string
		comment string
		want    string
	}{
		{"unknown rule", "// fern-lint: allow cyclomatic-complexety", "names no such lint rule"},
		{"unknown verb", "// fern-lint: deny cyclomatic-complexity", "unknown lint directive"},
		{"no rule named", "// fern-lint: allow", "names no rule"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := lintSrc(t, tc.comment+"\n"+noisyFn)
			var got *lint.Finding
			for i := range fs {
				if fs[i].Rule == lint.DirectiveRule {
					got = &fs[i]
				}
			}
			if got == nil {
				t.Fatalf("no %s finding in %v", lint.DirectiveRule, rulesOf(fs))
			}
			if !strings.Contains(got.Msg, tc.want) {
				t.Errorf("message = %q, want it to contain %q", got.Msg, tc.want)
			}
			// The bad directive silenced nothing, so the rule still fires.
			if len(fs) != 2 {
				t.Errorf("got %d findings %v, want the complexity finding as well", len(fs), rulesOf(fs))
			}
		})
	}
}

// A trailing `// fern-lint: allow` annotates the code it sits behind, not
// the line after it.
func TestSuppressionTrailingComment(t *testing.T) {
	src := "function noisy(n: i32): i32 { // fern-lint: allow cyclomatic-complexity\n" +
		"    if (n > 0) { return 1; }\n    if (n < 0) { return 2; }\n    return 0;\n}\n"
	if fs := lintSrc(t, src); len(fs) != 0 {
		t.Errorf("got %d findings %v, want the trailing directive to cover its own line", len(fs), rulesOf(fs))
	}
}

// An `allow` with nothing after it covers nothing, which is a mistake worth
// reporting rather than a no-op worth hiding.
func TestSuppressionDanglingDirective(t *testing.T) {
	fs := lintSrc(t, noisyFn+"\n// fern-lint: allow cyclomatic-complexity\n")
	var msgs []string
	for _, f := range fs {
		if f.Rule == lint.DirectiveRule {
			msgs = append(msgs, f.Msg)
		}
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0], "covers no code") {
		t.Errorf("messages = %v, want one about covering no code", msgs)
	}
}
