package coreutils

import "testing"

// sha224sum(1) — SHA-224. The corpus is `sumCases`, shared with the other
// six checksum utilities because `coreutils/lib/digest.fern` is one
// program: what is proved here that the others do not prove is the
// digest itself, the word this utility writes in a BSD tag and in
// `improperly formatted SHA224 checksum line`, and the digest length
// the check-line grammar measures against.
func sha224sumCases(t *testing.T) []invocation {
	return sumCases(t, "sha224sum")
}

func TestSha224sumParity(t *testing.T) {
	requireParity(t, "sha224sum", sha224sumCases(t))
}

func TestSha224sumHelpVersion(t *testing.T) {
	requireHelp(t, "sha224sum", []string{"--help"}, 0)
	requireHelp(t, "sha224sum", []string{"--he"}, 0)
	requireVersion(t, "sha224sum", []string{"--version"}, 0)
	requireVersion(t, "sha224sum", []string{"--vers"}, 0)
	// getopt permutes, so an operand does not end the option scan.
	requireHelp(t, "sha224sum", []string{"x", "--help"}, 0)
	requireVersion(t, "sha224sum", []string{"x", "--version"}, 0)
}
