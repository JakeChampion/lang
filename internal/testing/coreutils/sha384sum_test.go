package coreutils

import "testing"

func init() {
	registerCorpus("sha384sum", sha384sumCases)
}

func sha384sumCases(t *testing.T) []invocation {
	return digestCases(t, "sha384sum")
}

func TestSha384sumParity(t *testing.T) {
	requireParity(t, "sha384sum", sha384sumCases(t))
}

func TestSha384sumHelpVersion(t *testing.T) {
	requireHelp(t, "sha384sum", []string{"--help"}, 0)
	requireHelp(t, "sha384sum", []string{"--he"}, 0)
	requireVersion(t, "sha384sum", []string{"--version"}, 0)
	requireVersion(t, "sha384sum", []string{"--vers"}, 0)
	requireHelp(t, "sha384sum", []string{"x", "--help"}, 0)
	requireVersion(t, "sha384sum", []string{"x", "--version"}, 0)
}
