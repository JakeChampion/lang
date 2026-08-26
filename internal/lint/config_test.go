package lint_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/lint"
	"github.com/jakechampion/lang/internal/parser"
)

func TestParseSeverity(t *testing.T) {
	for _, s := range []lint.Severity{lint.Allow, lint.Warn, lint.Deny} {
		got, err := lint.ParseSeverity(s.String())
		if err != nil || got != s {
			t.Errorf("round trip of %v: got %v, %v", s, got, err)
		}
	}
	if _, err := lint.ParseSeverity("error"); err == nil {
		t.Error("`error` is not one of the three spellings and must be rejected")
	}
}

// A typo'd rule name would otherwise configure nothing at all, silently.
func TestConfigRejectsUnknownNames(t *testing.T) {
	cfg := lint.NewConfig()
	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"unknown rule severity", func() error { return cfg.SetSeverity("nope", lint.Deny) }, "unknown lint rule"},
		{"unknown rule option", func() error { return cfg.SetOption("nope.max", "5") }, "unknown lint rule"},
		{"unknown option key", func() error { return cfg.SetOption("cyclomatic-complexity.limit", "5") }, "unknown option"},
		{"undotted option", func() error { return cfg.SetOption("max", "5") }, "<rule>.<key>"},
		{"non-numeric option", func() error { return cfg.SetOption("cyclomatic-complexity.max", "big") }, "not a number"},
		{"out-of-range option", func() error { return cfg.SetOption("cyclomatic-complexity.max", "0") }, "at least 1"},
		{"bad severity", func() error { return cfg.SetPair("cyclomatic-complexity", "loud") }, "unknown severity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// SetPair is the manifest's entry point: a dotted key is an option, a bare
// one a severity, so one [lint] table can carry both.
func TestConfigSetPairRouting(t *testing.T) {
	cfg := lint.NewConfig()
	if err := cfg.SetPair("cyclomatic-complexity", "deny"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetPair("cyclomatic-complexity.max", "2"); err != nil {
		t.Fatal(err)
	}
	fs := lintWith(t, cfg, noisyFn)
	if len(fs) != 1 {
		t.Fatalf("got %d findings, want 1", len(fs))
	}
	if fs[0].Severity != lint.Deny {
		t.Errorf("severity = %v, want deny", fs[0].Severity)
	}
	if !lint.Failed(fs) {
		t.Error("a deny finding must fail the run")
	}
}

// `allow` does not merely hide a rule's output — it stops the rule running.
func TestConfigAllowSilencesRule(t *testing.T) {
	cfg := lint.NewConfig()
	if err := cfg.SetPair("cyclomatic-complexity.max", "1"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetPair("cyclomatic-complexity", "allow"); err != nil {
		t.Fatal(err)
	}
	if fs := lintWith(t, cfg, noisyFn); len(fs) != 0 {
		t.Errorf("got %d findings %v, want none", len(fs), rulesOf(fs))
	}
}

// Warnings alone leave the exit status alone; that is the whole difference
// between warn and deny.
func TestFailedOnlyCountsDeny(t *testing.T) {
	if lint.Failed([]lint.Finding{{Severity: lint.Warn}, {Severity: lint.Allow}}) {
		t.Error("warnings must not fail the run")
	}
	if !lint.Failed([]lint.Finding{{Severity: lint.Warn}, {Severity: lint.Deny}}) {
		t.Error("one deny must fail the run")
	}
}

// Findings arrive in source order regardless of which rule produced them,
// so output reads top-to-bottom like a compiler's.
func TestFindingsAreInSourceOrder(t *testing.T) {
	src := "// fern-lint: allow bogus-rule\n" + noisyFn + strings.Replace(noisyFn, "noisy", "noisier", 1)
	cfg := lint.NewConfig()
	if err := cfg.SetOption("cyclomatic-complexity.max", "2"); err != nil {
		t.Fatal(err)
	}
	fs := lintWith(t, cfg, src)
	if len(fs) != 3 {
		t.Fatalf("got %d findings %v, want 3", len(fs), rulesOf(fs))
	}
	for i := 1; i < len(fs); i++ {
		if fs[i-1].Pos.Line > fs[i].Pos.Line {
			t.Errorf("finding %d at line %d follows line %d", i, fs[i].Pos.Line, fs[i-1].Pos.Line)
		}
	}
}

func TestRulesRegistryIsFreshPerCall(t *testing.T) {
	first := lint.Rules()
	if len(first) == 0 {
		t.Fatal("no rules registered")
	}
	c, ok := first[0].(lint.Configurable)
	if !ok {
		t.Skip("first rule takes no options")
	}
	if err := c.SetOption("max", "99"); err != nil {
		t.Fatal(err)
	}
	// A second call must not see the first call's mutation, or two runs
	// with different thresholds would contaminate each other.
	second := lint.Rules()
	if got := second[0].(lint.Configurable).Options()["max"]; got == "99" {
		t.Error("Rules() handed back a shared rule value")
	}
}

func lintWith(t *testing.T, cfg *lint.Config, src string) []lint.Finding {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fs, err := lint.File(cfg, "t.fern", src, prog)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// A finding carries its measurement, so a gate comparing numbers reads
// Value instead of parsing the score back out of the message.
func TestFindingCarriesItsScore(t *testing.T) {
	cfg := lint.NewConfig()
	if err := cfg.SetOption("cyclomatic-complexity.max", "2"); err != nil {
		t.Fatal(err)
	}
	fs := lintWith(t, cfg, noisyFn)
	if len(fs) != 1 {
		t.Fatalf("got %d findings, want 1", len(fs))
	}
	if fs[0].Value != 3 {
		t.Errorf("Value = %d, want the score 3", fs[0].Value)
	}
	if !strings.Contains(fs[0].Msg, "complexity of 3") {
		t.Errorf("message %q disagrees with Value %d", fs[0].Msg, fs[0].Value)
	}
}

// The repo gate ratchets on SUMMED DISTANCE over the limit, not on a count
// of functions over it. This pins why: splitting one big function into
// several smaller ones RAISES the count while LOWERING the distance, so a
// count-based gate would report the most valuable refactor available as a
// regression. If someone switches the gate back to counting, this fails.
func TestSplittingImprovesExcessButNotCount(t *testing.T) {
	const limit = 2

	// One function with six forks.
	whole := `function whole(n: i32): i32 {
    if (n == 1) { return 1; }
    if (n == 2) { return 2; }
    if (n == 3) { return 3; }
    if (n == 4) { return 4; }
    if (n == 5) { return 5; }
    if (n == 6) { return 6; }
    return 0;
}
`
	// The same work as three functions of two forks each.
	split := `function part_a(n: i32): i32 {
    if (n == 1) { return 1; }
    if (n == 2) { return 2; }
    return 0;
}
function part_b(n: i32): i32 {
    if (n == 3) { return 3; }
    if (n == 4) { return 4; }
    return 0;
}
function part_c(n: i32): i32 {
    if (n == 5) { return 5; }
    if (n == 6) { return 6; }
    return 0;
}
`
	cfg := lint.NewConfig()
	if err := cfg.SetOption("cyclomatic-complexity.max", strconv.Itoa(limit)); err != nil {
		t.Fatal(err)
	}

	countWhole, excessWhole := countAndExcess(lintWith(t, cfg, whole), limit)
	countSplit, excessSplit := countAndExcess(lintWith(t, cfg, split), limit)

	if countSplit <= countWhole {
		t.Fatalf("this test assumes splitting raises the count: whole=%d split=%d", countWhole, countSplit)
	}
	if excessSplit >= excessWhole {
		t.Errorf("summed excess must FALL when a function is split: whole=%d split=%d", excessWhole, excessSplit)
	}
}

func countAndExcess(fs []lint.Finding, limit int) (count, excess int) {
	for _, f := range fs {
		if f.Rule == lint.DirectiveRule {
			continue
		}
		count++
		excess += f.Value - limit
	}
	return count, excess
}
