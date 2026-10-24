package coreutils

import "testing"

func init() {
	registerCorpus("sha512sum", sha512sumCases)
}

func sha512sumCases(t *testing.T) []invocation {
	return digestCases(t, "sha512sum")
}

func TestSha512sumParity(t *testing.T) {
	requireParity(t, "sha512sum", sha512sumCases(t))
}

func TestSha512sumHelpVersion(t *testing.T) {
	requireHelp(t, "sha512sum", []string{"--help"}, 0)
	requireHelp(t, "sha512sum", []string{"--he"}, 0)
	requireVersion(t, "sha512sum", []string{"--version"}, 0)
	requireVersion(t, "sha512sum", []string{"--vers"}, 0)
	requireHelp(t, "sha512sum", []string{"x", "--help"}, 0)
	requireVersion(t, "sha512sum", []string{"x", "--version"}, 0)
}
