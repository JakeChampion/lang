package coreutils

import "testing"

func init() {
	registerCorpus("b2sum", b2sumCases)
}

func b2sumCases(t *testing.T) []invocation {
	return digestCases(t, "b2sum")
}

func TestB2sumParity(t *testing.T) {
	requireParity(t, "b2sum", b2sumCases(t))
}

func TestB2sumHelpVersion(t *testing.T) {
	requireHelp(t, "b2sum", []string{"--help"}, 0)
	requireHelp(t, "b2sum", []string{"--he"}, 0)
	requireVersion(t, "b2sum", []string{"--version"}, 0)
	requireVersion(t, "b2sum", []string{"--vers"}, 0)
	requireHelp(t, "b2sum", []string{"x", "--help"}, 0)
	requireVersion(t, "b2sum", []string{"x", "--version"}, 0)
}
