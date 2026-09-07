package coreutils

import "testing"

// md5sum(1) — MD5. The corpus is `sumCases`, shared with the other
// six checksum utilities because `coreutils/lib/digest.fern` is one
// program: what is proved here that the others do not prove is the
// digest itself, the word this utility writes in a BSD tag and in
// `improperly formatted MD5 checksum line`, and the digest length
// the check-line grammar measures against.
func md5sumCases(t *testing.T) []invocation {
	return sumCases(t, "md5sum")
}

func TestMd5sumParity(t *testing.T) {
	requireParity(t, "md5sum", md5sumCases(t))
}

func TestMd5sumHelpVersion(t *testing.T) {
	requireHelp(t, "md5sum", []string{"--help"}, 0)
	requireHelp(t, "md5sum", []string{"--he"}, 0)
	requireVersion(t, "md5sum", []string{"--version"}, 0)
	requireVersion(t, "md5sum", []string{"--vers"}, 0)
	// getopt permutes, so an operand does not end the option scan.
	requireHelp(t, "md5sum", []string{"x", "--help"}, 0)
	requireVersion(t, "md5sum", []string{"x", "--version"}, 0)
}
