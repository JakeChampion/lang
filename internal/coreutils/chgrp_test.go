package coreutils

import "testing"

// chgrp(1) is chown with the user half taken away, and it shares chown's
// fixture, its option cases and its ownership comparison. What is its own is
// the operand grammar, and it is worth a corpus rather than a note: the
// operand is ONE group token with no split at all, so `chgrp a:b` is `invalid
// group: 'a:b'` where `chown a:b` is a user and a group — while `--from=` on
// the SAME utility is still the full user:group spec, so `chgrp --from=staff`
// is an invalid USER.
//
// That asymmetry is GNU's and it reads like a bug. It is the reason the two
// specs are parsed by different entry points rather than by one with a flag.

func init() {
	registerCorpus("chgrp", chgrpCases)
}

func chgrpCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

	chownOptionCases(add, "daemon")
	chownRootCases(add, "daemon")

	tree := func(name string, args ...string) {
		add(invocation{name: name, args: args, seedTree: chownTree, ownership: true})
	}

	// ---- the operand is one group token --------------------------------
	spec := func(name, s string) { tree(name, s, "f") }
	spec("unknown group", "nosuchgroup")
	spec("a user and a group is not a group", "root:daemon")
	spec("a leading colon is not a group", ":daemon")
	spec("a group and a group is not a group", "daemon:daemon")
	spec("a dot is not a separator here", "root.daemon")
	spec("colon alone", ":")
	spec("two colons", "::")
	spec("empty spec", "")
	spec("by name", "daemon")
	spec("by number", "1")
	spec("forced numeric", "+1")
	spec("hex is not a number", "0x10")
	spec("past the sentinel", "4294967296")
	spec("leading zeros are decimal", "007")
	spec("a trailing space is a fault", "1 ")
	spec("two numbers", "20:20")

	// ---- the wording ----------------------------------------------------
	tree("verbose by name", "-v", "daemon", "f")
	tree("verbose by number", "-v", "1", "f")
	tree("verbose empty spec", "-v", "", "f")
	tree("changes by name", "-c", "daemon", "f")
	tree("silent by name", "-f", "daemon", "f")
	tree("silent and verbose", "-f", "-v", "daemon", "f")

	// ---- entries that cannot be reached ---------------------------------
	tree("a name that is not there", "daemon", "nope")
	tree("verbose on a name that is not there", "-v", "daemon", "nope")
	tree("silent on a name that is not there", "-f", "daemon", "nope")

	// ---- symlinks --------------------------------------------------------
	tree("a symlink is followed by default", "-v", "daemon", "lnk")
	tree("no-dereference", "-h", "-v", "daemon", "lnk")
	tree("dereference explicitly", "--dereference", "-v", "daemon", "lnk")
	tree("dereference then no-dereference", "--dereference", "-h", "-v", "daemon", "lnk")
	tree("no-dereference then dereference", "-h", "--dereference", "-v", "daemon", "lnk")
	// The `-v` line is left off the followed form: GNU fills its "from"
	// clause from a stat that failed. See chown_test.go's header.
	tree("a broken link followed", "daemon", "broken")
	tree("a broken link followed, silent", "-f", "daemon", "broken")
	tree("a broken link not followed", "-h", "-v", "daemon", "broken")

	// ---- the walk --------------------------------------------------------
	tree("recursive", "-R", "-v", "daemon", "d")
	tree("recursive quiet", "-R", "daemon", "d")
	tree("recursive changes", "-R", "-c", "daemon", "d")
	tree("recursive on a symlinked directory", "-R", "-v", "daemon", "symdir")
	tree("recursive H", "-R", "-H", "-v", "daemon", "symdir")
	tree("recursive L", "-R", "-L", "-v", "daemon", "symdir")
	tree("recursive P", "-R", "-P", "-v", "daemon", "symdir")
	tree("L then P", "-R", "-L", "-P", "-v", "daemon", "symdir")
	tree("P then L", "-R", "-P", "-L", "-v", "daemon", "symdir")
	tree("recursive with no-dereference", "-R", "-h", "-v", "daemon", "d")
	tree("no-dereference loses to L", "-R", "-h", "-L", "-v", "daemon", "d")
	tree("L in either order", "-R", "-L", "-h", "-v", "daemon", "d")
	tree("recursive over two operands", "-R", "-v", "daemon", "d", "f")

	// ---- --reference -----------------------------------------------------
	tree("reference", "--reference=d", "f")
	tree("reference verbose", "--reference=d", "-v", "f")
	tree("reference is not there", "--reference=nope", "f")
	tree("reference with a spec operand", "--reference=d", "daemon", "f")

	// `--from` and the `(gid_t) -1` refusal are NOT in this corpus, and the
	// reason is the oracle rather than the utility. `chgrp` reached GNU's
	// shared `parse_user_spec` somewhere after 9.1: that release's chgrp
	// answers `unrecognized option '--from='` and accepts `4294967295` as a
	// gid, where 9.10 takes the option and refuses the sentinel. Measured on
	// both — 9.1 in the devbox container, 9.10 on macOS.
	//
	// The corpus is held to "9.4 or newer", so a case may only assert what
	// every version in that range does, and which side of the change 9.4
	// falls on is not something this branch can measure. `chgrp.fern`
	// implements 9.10's answer for both, which is why `--from` appears in
	// `chownOptionCases` above only through the spellings that fault the
	// same way on either version.

	return cases
}

func TestChgrp(t *testing.T) {
	requireParity(t, "chgrp", chgrpCases(t))
}

// The two options whose TEXT is ours by design (docs/COREUTILS.md).
func TestChgrpHelp(t *testing.T) {
	requireHelp(t, "chgrp", []string{"--help"}, 0)
	requireHelp(t, "chgrp", []string{"--h"}, 0)
	requireHelp(t, "chgrp", []string{"--help", "extra"}, 0)
	requireHelp(t, "chgrp", []string{"zzznope", "--help"}, 0)
	requireVersion(t, "chgrp", []string{"--version"}, 0)
	requireVersion(t, "chgrp", []string{"--versio"}, 0)
	requireVersion(t, "chgrp", []string{"--version", "extra"}, 0)
}
