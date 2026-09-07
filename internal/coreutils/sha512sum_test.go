package coreutils

import "testing"

// sha512sum(1) — SHA-512. The corpus is `sumCases`, shared with the other
// six checksum utilities because `coreutils/lib/digest.fern` is one
// program: what is proved here that the others do not prove is the
// digest itself, the word this utility writes in a BSD tag and in
// `improperly formatted SHA512 checksum line`, and the digest length
// the check-line grammar measures against.
func sha512sumCases(t *testing.T) []invocation {
	return sumCases(t, "sha512sum")
}

func TestSha512sumParity(t *testing.T) {
	requireParity(t, "sha512sum", sha512sumCases(t))
}

func TestSha512sumHelpVersion(t *testing.T) {
	requireHelp(t, "sha512sum", []string{"--help"}, 0)
	requireHelp(t, "sha512sum", []string{"--he"}, 0)
	requireVersion(t, "sha512sum", []string{"--version"}, 0)
	requireVersion(t, "sha512sum", []string{"--vers"}, 0)
	// getopt permutes, so an operand does not end the option scan.
	requireHelp(t, "sha512sum", []string{"x", "--help"}, 0)
	requireVersion(t, "sha512sum", []string{"x", "--version"}, 0)
}
