package coreutils

import "testing"

func init() {
	registerCorpus("sha1sum", sha1sumCases)
}

func sha1sumCases(t *testing.T) []invocation {
	return digestCases(t, "sha1sum")
}

func TestSha1sumParity(t *testing.T) {
	requireParity(t, "sha1sum", sha1sumCases(t))
}

func TestSha1sumHelpVersion(t *testing.T) {
	requireHelp(t, "sha1sum", []string{"--help"}, 0)
	requireHelp(t, "sha1sum", []string{"--he"}, 0)
	requireVersion(t, "sha1sum", []string{"--version"}, 0)
	requireVersion(t, "sha1sum", []string{"--vers"}, 0)
	requireHelp(t, "sha1sum", []string{"x", "--help"}, 0)
	requireVersion(t, "sha1sum", []string{"x", "--version"}, 0)
}
