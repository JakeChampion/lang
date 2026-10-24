package coreutils

import "testing"

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

	tree("verbose by name", "-v", "daemon", "f")
	tree("verbose by number", "-v", "1", "f")
	tree("verbose empty spec", "-v", "", "f")
	tree("changes by name", "-c", "daemon", "f")
	tree("silent by name", "-f", "daemon", "f")
	tree("silent and verbose", "-f", "-v", "daemon", "f")

	tree("a name that is not there", "daemon", "nope")
	tree("verbose on a name that is not there", "-v", "daemon", "nope")
	tree("silent on a name that is not there", "-f", "daemon", "nope")

	tree("a symlink is followed by default", "-v", "daemon", "lnk")
	tree("no-dereference", "-h", "-v", "daemon", "lnk")
	tree("dereference explicitly", "--dereference", "-v", "daemon", "lnk")
	tree("dereference then no-dereference", "--dereference", "-h", "-v", "daemon", "lnk")
	tree("no-dereference then dereference", "-h", "--dereference", "-v", "daemon", "lnk")
	tree("a broken link followed", "daemon", "broken")
	tree("a broken link followed, silent", "-f", "daemon", "broken")
	tree("a broken link not followed", "-h", "-v", "daemon", "broken")

	tree("recursive", "-R", "-v", "daemon", "d")
	add(invocation{name: "recursive beyond PATH_MAX", args: []string{"-R", "-v", "daemon", "longpath"}, seedTree: chownLongTree})
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

	tree("reference", "--reference=d", "f")
	tree("reference verbose", "--reference=d", "-v", "f")
	tree("reference is not there", "--reference=nope", "f")
	tree("reference with a spec operand", "--reference=d", "daemon", "f")

	return cases
}

func TestChgrp(t *testing.T) {
	requireParity(t, "chgrp", chgrpCases(t))
}

func TestChgrpHelp(t *testing.T) {
	requireHelp(t, "chgrp", []string{"--help"}, 0)
	requireHelp(t, "chgrp", []string{"--h"}, 0)
	requireHelp(t, "chgrp", []string{"--help", "extra"}, 0)
	requireHelp(t, "chgrp", []string{"zzznope", "--help"}, 0)
	requireVersion(t, "chgrp", []string{"--version"}, 0)
	requireVersion(t, "chgrp", []string{"--versio"}, 0)
	requireVersion(t, "chgrp", []string{"--version", "extra"}, 0)
}
