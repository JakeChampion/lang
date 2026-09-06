package coreutils

import "testing"

// sha1sum(1) — SHA-1. The corpus is `sumCases`, shared with the other
// six checksum utilities because `coreutils/lib/digest.fern` is one
// program: what is proved here that the others do not prove is the
// digest itself, the word this utility writes in a BSD tag and in
// `improperly formatted SHA1 checksum line`, and the digest length
// the check-line grammar measures against.
func sha1sumCases(t *testing.T) []invocation {
	return sumCases(t, "sha1sum")
}

func TestSha1sumParity(t *testing.T) {
	requireParity(t, "sha1sum", sha1sumCases(t))
}

func TestSha1sumHelpVersion(t *testing.T) {
	requireHelp(t, "sha1sum", []string{"--help"}, 0)
	requireHelp(t, "sha1sum", []string{"--he"}, 0)
	requireVersion(t, "sha1sum", []string{"--version"}, 0)
	requireVersion(t, "sha1sum", []string{"--vers"}, 0)
	// getopt permutes, so an operand does not end the option scan.
	requireHelp(t, "sha1sum", []string{"x", "--help"}, 0)
	requireVersion(t, "sha1sum", []string{"x", "--version"}, 0)
}
