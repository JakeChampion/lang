package coreutils

import "testing"

// b2sum(1) — BLAKE2b. The corpus is `sumCases`, shared with the other
// six checksum utilities because `coreutils/lib/digest.fern` is one
// program: what is proved here that the others do not prove is the
// digest itself, the word this utility writes in a BSD tag and in
// `improperly formatted BLAKE2b checksum line`, and the digest length
// the check-line grammar measures against.
func b2sumCases(t *testing.T) []invocation {
	return sumCases(t, "b2sum")
}

func TestB2sumParity(t *testing.T) {
	requireParity(t, "b2sum", b2sumCases(t))
}

func TestB2sumHelpVersion(t *testing.T) {
	requireHelp(t, "b2sum", []string{"--help"}, 0)
	requireHelp(t, "b2sum", []string{"--he"}, 0)
	requireVersion(t, "b2sum", []string{"--version"}, 0)
	requireVersion(t, "b2sum", []string{"--vers"}, 0)
	// getopt permutes, so an operand does not end the option scan.
	requireHelp(t, "b2sum", []string{"x", "--help"}, 0)
	requireVersion(t, "b2sum", []string{"x", "--version"}, 0)
}
