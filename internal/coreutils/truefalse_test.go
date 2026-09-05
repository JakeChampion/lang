package coreutils

import "testing"

// true(1) and false(1) ignore every argument, including ones that look
// like options, and answer `--help` / `--version` only when it is the
// sole argument and spelled in full. Both write those to stdout and
// still exit with their own status, so false(1) exits 1 from `--help`.
func trueFalseCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments"},
		{name: "an operand", args: []string{"x"}},
		{name: "several operands", args: []string{"a", "b", "c"}},
		{name: "an option-looking argument", args: []string{"-x"}},
		{name: "an unrecognized long option", args: []string{"--foo"}},
		{name: "lone dash", args: []string{"-"}},
		{name: "dashdash", args: []string{"--"}},
		// Not the sole argument, so not a help request.
		{name: "help with an operand after it", args: []string{"--help", "x"}},
		{name: "version with an operand after it", args: []string{"--version", "x"}},
		// No prefix matching here: these go through argv[1] directly
		// rather than through getopt.
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

// The status false(1) exits with does not change for `--help`.
func TestFalseHelpVersion(t *testing.T) {
	requireHelp(t, "false", []string{"--help"}, 1)
	requireVersion(t, "false", []string{"--version"}, 1)
}
