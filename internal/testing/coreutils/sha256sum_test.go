package coreutils

import "testing"

func init() {
	registerCorpus("sha256sum", sha256sumCases)
}

func sha256sumCases(t *testing.T) []invocation {
	return digestCases(t, "sha256sum")
}

func TestSha256sumParity(t *testing.T) {
	requireParity(t, "sha256sum", sha256sumCases(t))
}

func TestSha256sumHelpVersion(t *testing.T) {
	requireHelp(t, "sha256sum", []string{"--help"}, 0)
	requireHelp(t, "sha256sum", []string{"--he"}, 0)
	requireVersion(t, "sha256sum", []string{"--version"}, 0)
	requireVersion(t, "sha256sum", []string{"--vers"}, 0)
	requireHelp(t, "sha256sum", []string{"x", "--help"}, 0)
	requireVersion(t, "sha256sum", []string{"x", "--version"}, 0)
}
