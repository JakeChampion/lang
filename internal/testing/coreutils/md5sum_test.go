package coreutils

import "testing"

func init() {
	registerCorpus("md5sum", md5sumCases)
}

func md5sumCases(t *testing.T) []invocation {
	return digestCases(t, "md5sum")
}

func TestMd5sumParity(t *testing.T) {
	requireParity(t, "md5sum", md5sumCases(t))
}

func TestMd5sumHelpVersion(t *testing.T) {
	requireHelp(t, "md5sum", []string{"--help"}, 0)
	requireHelp(t, "md5sum", []string{"--he"}, 0)
	requireVersion(t, "md5sum", []string{"--version"}, 0)
	requireVersion(t, "md5sum", []string{"--vers"}, 0)
	requireHelp(t, "md5sum", []string{"x", "--help"}, 0)
	requireVersion(t, "md5sum", []string{"x", "--version"}, 0)
}
