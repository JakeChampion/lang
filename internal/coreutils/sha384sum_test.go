package coreutils

import "testing"

// sha384sum(1) — SHA-384. The corpus is `sumCases`, shared with the other
// six checksum utilities because `coreutils/lib/digest.fern` is one
// program: what is proved here that the others do not prove is the
// digest itself, the word this utility writes in a BSD tag and in
// `improperly formatted SHA384 checksum line`, and the digest length
// the check-line grammar measures against.
func sha384sumCases(t *testing.T) []invocation {
	return sumCases(t, "sha384sum")
}

func TestSha384sumParity(t *testing.T) {
	requireParity(t, "sha384sum", sha384sumCases(t))
}

func TestSha384sumHelpVersion(t *testing.T) {
	requireHelp(t, "sha384sum", []string{"--help"}, 0)
	requireHelp(t, "sha384sum", []string{"--he"}, 0)
	requireVersion(t, "sha384sum", []string{"--version"}, 0)
	requireVersion(t, "sha384sum", []string{"--vers"}, 0)
	// getopt permutes, so an operand does not end the option scan.
	requireHelp(t, "sha384sum", []string{"x", "--help"}, 0)
	requireVersion(t, "sha384sum", []string{"x", "--version"}, 0)
}
