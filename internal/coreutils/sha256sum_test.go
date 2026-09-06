package coreutils

import "testing"

// sha256sum(1) — SHA-256. The corpus is `sumCases`, shared with the other
// six checksum utilities because `coreutils/lib/digest.fern` is one
// program: what is proved here that the others do not prove is the
// digest itself, the word this utility writes in a BSD tag and in
// `improperly formatted SHA256 checksum line`, and the digest length
// the check-line grammar measures against.
func sha256sumCases(t *testing.T) []invocation {
	return sumCases(t, "sha256sum")
}

func TestSha256sumParity(t *testing.T) {
	requireParity(t, "sha256sum", sha256sumCases(t))
}

func TestSha256sumHelpVersion(t *testing.T) {
	requireHelp(t, "sha256sum", []string{"--help"}, 0)
	requireHelp(t, "sha256sum", []string{"--he"}, 0)
	requireVersion(t, "sha256sum", []string{"--version"}, 0)
	requireVersion(t, "sha256sum", []string{"--vers"}, 0)
	// getopt permutes, so an operand does not end the option scan.
	requireHelp(t, "sha256sum", []string{"x", "--help"}, 0)
	requireVersion(t, "sha256sum", []string{"x", "--version"}, 0)
}
