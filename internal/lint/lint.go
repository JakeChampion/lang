// Package lint holds Fern's source linter — the `fern -lint` checks that
// flag code which compiles but reads badly.
//
// A lint is not a type error. The checker's job is to reject programs that
// cannot run; a lint's job is to name a program that runs and still costs
// the next reader more than it should. So lints work off the PARSE tree,
// before type-checking: a file with a type error still lints, and linting a
// large tree costs a parse rather than a full check.
//
// Rules are addressed by a stable kebab-case name (`cyclomatic-complexity`),
// carry a default severity, and are looked up through the registry below.
// Severity is per-rule and overridable three ways, innermost wins:
//
//	default   the rule's own DefaultSeverity
//	manifest  a [lint] table in the governing fern.toml
//	flag      -lint-set NAME=SEVERITY on the command line
//
// plus per-site suppression with a `// fern-lint: allow NAME` comment
// (see suppress.go).
package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Severity says what a rule's findings do to the run.
type Severity int

const (
	// Allow silences the rule entirely — it is not even run.
	Allow Severity = iota
	// Warn prints the finding but leaves the exit status alone.
	Warn
	// Deny prints the finding and fails the run.
	Deny
)

func (s Severity) String() string {
	switch s {
	case Allow:
		return "allow"
	case Warn:
		return "warn"
	case Deny:
		return "deny"
	}
	return fmt.Sprintf("Severity(%d)", int(s))
}

// ParseSeverity maps the source spelling of a severity to its value.
// Accepted spellings are exactly the three String() produces, so a
// manifest and a -lint-set flag agree on vocabulary.
func ParseSeverity(s string) (Severity, error) {
	switch s {
	case "allow":
		return Allow, nil
	case "warn":
		return Warn, nil
	case "deny":
		return Deny, nil
	}
	return Allow, fmt.Errorf("unknown severity %q (want allow, warn, or deny)", s)
}

// Finding is one reported lint site.
type Finding struct {
	// Rule is the emitting rule's name, printed in the header so the
	// reader can silence or look up exactly this check.
	Rule string
	// Severity is resolved at report time, so a finding already knows
	// whether it fails the run — nothing downstream re-consults config.
	Severity Severity
	Pos      ast.Position
	// File is the path the finding was reported against; the renderer
	// prints it and the sorter groups by it.
	File string
	Msg  string
	// Help is an optional second line suggesting what to do. Empty when
	// the message is its own advice.
	Help string
	// Value is the measurement behind the finding — a complexity score, a
	// nesting depth — or zero for a rule that measures nothing. A finding's
	// message is prose for a human; a gate comparing numbers should read
	// them here rather than parsing the message back out.
	Value int
}

// Pass is one rule's view of one file. A rule reads Prog and reports
// through Report; everything else about the run is the driver's business.
type Pass struct {
	File string
	Src  string
	Prog *ast.Program

	rule     Rule
	severity Severity
	sup      *suppressions
	out      *[]Finding
}

// Report records a finding at pos. Sites suppressed by a
// `// fern-lint: allow <rule>` comment are dropped here, so no rule has
// to know suppression exists.
//
// value is the measurement behind the finding (see Finding.Value); pass 0
// from a rule that measures nothing.
func (p *Pass) Report(pos ast.Position, msg, help string, value int) {
	if p.sup.allows(p.rule.Name(), pos.Line) {
		return
	}
	*p.out = append(*p.out, Finding{
		Rule:     p.rule.Name(),
		Severity: p.severity,
		Pos:      pos,
		File:     p.File,
		Msg:      msg,
		Help:     help,
		Value:    value,
	})
}

// Rule is one lint check. Implementations are stateless with respect to
// the file under inspection — Check reads the Pass and reports; any
// tunable knob lives on the rule value and is set through SetOption
// before the run.
type Rule interface {
	// Name is the stable kebab-case identifier used in config, in
	// suppression comments, and in the rendered header.
	Name() string
	// Doc is a one-line description for `fern -lint-rules`.
	Doc() string
	// DefaultSeverity is what the rule reports at when nothing
	// overrides it.
	DefaultSeverity() Severity
	Check(*Pass)
}

// Configurable is implemented by rules with tunable knobs. Options are
// addressed as `<rule>.<key>` in a manifest and `-lint-opt <rule>.<key>=V`
// on the command line.
type Configurable interface {
	Rule
	// SetOption applies one setting, returning an error for an unknown
	// key or an unusable value. A rule that reports an error here fails
	// the run rather than silently linting at a default the user did
	// not ask for.
	SetOption(key, value string) error
	// Options lists the rule's option keys with their current values,
	// for `fern -lint-rules`.
	Options() map[string]string
}

// Rules returns a fresh instance of every registered rule, in name order.
// Instances are fresh because SetOption mutates them: two concurrent runs
// with different thresholds must not share a rule value.
func Rules() []Rule {
	out := make([]Rule, 0, len(registry))
	for _, mk := range registry {
		out = append(out, mk())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// registry holds a constructor per rule, keyed by name. Populated by
// register() from each rule file's init.
var registry = map[string]func() Rule{}

func register(mk func() Rule) {
	name := mk().Name()
	if _, dup := registry[name]; dup {
		panic("lint: duplicate rule name " + name)
	}
	if name == DirectiveRule {
		panic("lint: " + DirectiveRule + " is reserved for diagnostics about the lint directives themselves")
	}
	registry[name] = mk
}

// Config is the resolved lint configuration for a run: which rules are on
// and at what severity, plus each rule's options.
type Config struct {
	// Severities overrides a rule's DefaultSeverity. Absent = default.
	Severities map[string]Severity
	// Options holds `<rule>.<key>` → value settings.
	Options map[string]string
}

// NewConfig returns an empty config — every rule at its default severity.
func NewConfig() *Config {
	return &Config{Severities: map[string]Severity{}, Options: map[string]string{}}
}

// SetSeverity records an override for one rule, rejecting a name no rule
// answers to: a typo'd rule name in a manifest would otherwise silently
// configure nothing.
func (c *Config) SetSeverity(rule string, sev Severity) error {
	if _, ok := registry[rule]; !ok {
		return fmt.Errorf("unknown lint rule %q (%s)", rule, knownRules())
	}
	c.Severities[rule] = sev
	return nil
}

// SetOption records a `<rule>.<key>` option. The rule must exist and must
// accept the key; both are checked here so a bad setting is reported once,
// against the file that spelled it, rather than per linted file.
func (c *Config) SetOption(dotted, value string) error {
	rule, key, ok := strings.Cut(dotted, ".")
	if !ok {
		return fmt.Errorf("lint option %q must be spelled <rule>.<key>", dotted)
	}
	mk, ok := registry[rule]
	if !ok {
		return fmt.Errorf("unknown lint rule %q (%s)", rule, knownRules())
	}
	cfg, ok := mk().(Configurable)
	if !ok {
		return fmt.Errorf("lint rule %q takes no options", rule)
	}
	if err := cfg.SetOption(key, value); err != nil {
		return fmt.Errorf("lint option %s: %w", dotted, err)
	}
	c.Options[dotted] = value
	return nil
}

// SetPair applies one `key = value` line from a [lint] or [lint.options]
// table. A key containing a dot is an option; anything else is a severity.
func (c *Config) SetPair(key, value string) error {
	if strings.Contains(key, ".") {
		return c.SetOption(key, value)
	}
	sev, err := ParseSeverity(value)
	if err != nil {
		return fmt.Errorf("lint rule %s: %w", key, err)
	}
	return c.SetSeverity(key, sev)
}

func knownRules() string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return "known rules: " + strings.Join(names, ", ")
}

// severityFor resolves the severity one rule runs at under c.
func (c *Config) severityFor(r Rule) Severity {
	if c != nil {
		if sev, ok := c.Severities[r.Name()]; ok {
			return sev
		}
	}
	return r.DefaultSeverity()
}

// applyOptions pushes c's options onto a rule instance.
func (c *Config) applyOptions(r Rule) error {
	if c == nil || len(c.Options) == 0 {
		return nil
	}
	cfg, ok := r.(Configurable)
	if !ok {
		return nil
	}
	// Sorted so a rule that validates one option against another sees
	// the same order every run.
	keys := make([]string, 0, len(c.Options))
	for k := range c.Options {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, dotted := range keys {
		rule, key, _ := strings.Cut(dotted, ".")
		if rule != r.Name() {
			continue
		}
		if err := cfg.SetOption(key, c.Options[dotted]); err != nil {
			return fmt.Errorf("lint option %s: %w", dotted, err)
		}
	}
	return nil
}

// File runs every enabled rule over one parsed file and returns the
// findings in source order. prog must come from parser.Parse of src;
// file is the path used in the rendered output.
func File(cfg *Config, file, src string, prog *ast.Program) ([]Finding, error) {
	sup := collectSuppressions(src, prog)
	out := append([]Finding(nil), sup.bad...)
	for i := range out {
		out[i].File = file
	}
	for _, r := range Rules() {
		sev := cfg.severityFor(r)
		if sev == Allow {
			continue
		}
		if err := cfg.applyOptions(r); err != nil {
			return nil, err
		}
		r.Check(&Pass{File: file, Src: src, Prog: prog, rule: r, severity: sev, sup: sup, out: &out})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pos.Line != out[j].Pos.Line {
			return out[i].Pos.Line < out[j].Pos.Line
		}
		if out[i].Pos.Col != out[j].Pos.Col {
			return out[i].Pos.Col < out[j].Pos.Col
		}
		return out[i].Rule < out[j].Rule
	})
	return out, nil
}

// Failed reports whether any finding was emitted at Deny — the exit-status
// question, kept here so every caller answers it the same way.
func Failed(fs []Finding) bool {
	for _, f := range fs {
		if f.Severity == Deny {
			return true
		}
	}
	return false
}
