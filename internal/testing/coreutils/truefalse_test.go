package coreutils

import "testing"

func init() {
	registerCorpus("false", trueFalseCases)
	registerCorpus("true", trueFalseCases)
}

func trueFalseCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments"},
		{name: "an operand", args: []string{"x"}},
		{name: "several operands", args: []string{"a", "b", "c"}},
		{name: "an option-looking argument", args: []string{"-x"}},
		{name: "an unrecognized long option", args: []string{"--foo"}},
		{name: "lone dash", args: []string{"-"}},
		{name: "dashdash", args: []string{"--"}},
		{name: "help with an operand after it", args: []string{"--help", "x"}},
		{name: "version with an operand after it", args: []string{"--version", "x"}},
		{name: "abbreviated help", args: []string{"--hel"}},
		{name: "abbreviated version", args: []string{"--vers"}},
	}
}

func TestTrueParity(t *testing.T) {
	requireParity(t, "true", trueFalseCases(t))
}

func TestFalseParity(t *testing.T) {
	requireParity(t, "false", trueFalseCases(t))
}

func TestTrueHelpVersion(t *testing.T) {
	requireHelp(t, "true", []string{"--help"}, 0)
	requireVersion(t, "true", []string{"--version"}, 0)
}

func TestFalseHelpVersion(t *testing.T) {
	requireHelp(t, "false", []string{"--help"}, 1)
	requireVersion(t, "false", []string{"--version"}, 1)
}
