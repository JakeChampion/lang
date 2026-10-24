package coreutils

import "testing"

func init() {
	registerCorpus("sha224sum", sha224sumCases)
}

func sha224sumCases(t *testing.T) []invocation {
	return digestCases(t, "sha224sum")
}

func TestSha224sumParity(t *testing.T) {
	requireParity(t, "sha224sum", sha224sumCases(t))
}

func TestSha224sumHelpVersion(t *testing.T) {
	requireHelp(t, "sha224sum", []string{"--help"}, 0)
	requireHelp(t, "sha224sum", []string{"--he"}, 0)
	requireVersion(t, "sha224sum", []string{"--version"}, 0)
	requireVersion(t, "sha224sum", []string{"--vers"}, 0)
	requireHelp(t, "sha224sum", []string{"x", "--help"}, 0)
	requireVersion(t, "sha224sum", []string{"x", "--version"}, 0)
}
