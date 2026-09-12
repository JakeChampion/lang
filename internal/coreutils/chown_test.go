package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

// chown(1) is the first utility here whose whole subject is a property the
// tree comparison did not look at. Every other field of `treeEntry` — kind,
// mode, symlink target, bytes, hard-link group — is one a run that did NOTHING
// AT ALL leaves untouched, so the corpus below would have passed a `chown`
// that parsed every operand, printed every line and made no call.
//
// So every case here sets `ownership`, which puts each entry's uid and gid
// into that comparison. It is opt-in because no other utility sets an id, and
// because an id is one more thing two machines can differ on for reasons that
// are not the utility's.
//
// ---- what the corpus can assume about the machine --------------------------
//
// Almost nothing. The suite runs as root in one lane and as a plain user in
// another, so "this succeeds" is not a property a case may assert: an owner
// change is EPERM for a normal user and silent for root, and the two produce
// different bytes. What IS stable is that GNU and Fern see the SAME machine,
// so a case whose answer depends on the runner still compares equal — which is
// why the corpus leans on specs that are faults, no-ops, or changes to ids the
// process already has.
//
// The ids it names are the ones every unix has in /etc/passwd and /etc/group
// rather than the invoking user, who on some hosts is not in either file.
//
// ---- the one divergence, and it is GNU reading uninitialised memory --------
//
// `chown -v` on a DANGLING symlink under `--dereference` prints a "from"
// clause built from a stat buffer the failed stat never filled: the same link
// reports `from wheel`, `from 2` and `from _uucp:wheel` on one machine
// depending on what was last on the stack. There is no value to match, so no
// corpus case pairs `-v` with that combination. The stderr line and the exit
// status ARE deterministic and are compared; docs/COREUTILS.md records it.

func init() {
	registerCorpus("chown", chownCases)
}

// chownTree is the fixture nearly every case runs against: a file, a
// directory with a file and a subdirectory under it, a symlink to the file, a
// symlink to the directory, and a broken one. Between them they reach every
// arm of the walk and both symlink questions.
func chownTree(t *testing.T, dir string) {
	t.Helper()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	mk := func(name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	link := func(target, name string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatalf("seed symlink %s: %v", name, err)
		}
	}
	write("f", "x")
	mk("d/sub")
	write("d/a", "x")
	write("d/sub/b", "x")
	link("f", "lnk")
	link("d", "symdir")
	link("no-such-target", "broken")
}

// chownSpecCases are the grammar and its diagnostics: they never reach a file,
// so they are shared verbatim between the two utilities' corpora except where
// the utility changes the reading.
func chownSpecCases(add func(invocation), util string) {
	spec := func(name string, s string) {
		add(invocation{name: name, args: []string{s, "f"}, seedTree: chownTree, ownership: true})
	}
	spec("unknown user", "nosuchuser")
	spec("unknown group", ":nosuchgroup")
	spec("both unknown", "nosuchuser:nosuchgroup")
	spec("three colons", ":::")
	spec("two colons", "::")
	spec("trailing colon on an unknown user", "nosuchuser:")
	spec("trailing colon on a number", "501:")
	spec("trailing colon on a dot", ".:")
	spec("empty spec", "")
	spec("colon alone", ":")
	spec("hex is not a number", "0x10")
	spec("hex group is not a number", ":0x10")
	spec("the uid_t sentinel", "4294967295")
	spec("the gid_t sentinel", ":4294967295")
	spec("past the sentinel", "4294967296")
	spec("far past the sentinel", "18446744073709551616")
	spec("leading zeros are decimal", "007")
	spec("a trailing space is a fault", "501 ")
	spec("three components", "501:20:30")
	spec("three named components", "a:b:c")
	spec("three dots", "...")
	spec("colon dot", ":.")
	spec("plus alone", "+")
	spec("colon plus", ":+")
	spec("plus and a name", "+nosuch")
	spec("colon plus and a name", ":+nosuch")
	spec("root by name", "root")
	spec("root by number", "0")
	spec("root forced numeric", "+0")
	spec("daemon group by name", ":daemon")
	spec("dot separator", "root.daemon")
	spec("dot separator with a bad group", "root.zzznope")
	spec("dot separator with a bad user", "zzznope.daemon")
	spec("dot separator all numeric", "0.0")
	spec("dot separator both bad", "zzznope.zzznope")
}

// chownOptionCases are getopt's own answers plus the arities, which are the
// same shape for both utilities and differ only in the operand's name.
func chownOptionCases(add func(invocation), good string) {
	add(invocation{name: "no operands"})
	add(invocation{name: "spec but no file", args: []string{good}})
	add(invocation{name: "dashdash alone", args: []string{"--"}})
	add(invocation{name: "spec then dashdash", args: []string{good, "--"}})
	add(invocation{name: "dashdash then spec", args: []string{"--", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "spec dashdash file", args: []string{good, "--", "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "empty operand", args: []string{good, ""}, seedTree: chownTree, ownership: true})
	add(invocation{name: "recursive with no operands", args: []string{"-R"}})
	add(invocation{name: "verbose with no operands", args: []string{"-v"}})
	add(invocation{name: "no-dereference with no operands", args: []string{"-h"}})
	add(invocation{name: "silent with no operands", args: []string{"-f"}})
	add(invocation{name: "reference with no operands", args: []string{"--reference=f"}, seedTree: chownTree, ownership: true})

	add(invocation{name: "unrecognized long option", args: []string{"--bogus", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "invalid short option", args: []string{"-Z", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "another invalid short option", args: []string{"-X", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "ambiguous no", args: []string{"--no", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "ambiguous n", args: []string{"--n", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "ambiguous r", args: []string{"--r", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "ambiguous re", args: []string{"--re", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "ambiguous v", args: []string{"--v", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "changes takes no argument", args: []string{"--changes=x", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "verbose takes no argument", args: []string{"--verbose=1", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "unambiguous de", args: []string{"--de", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "unambiguous rec", args: []string{"--rec", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "unambiguous s", args: []string{"--s", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "unambiguous c", args: []string{"--c", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "unambiguous p", args: []string{"--p", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "a spec beginning with a dash", args: []string{"-1", "f"}, seedTree: chownTree, ownership: true})

	// The traversal flags outside -R are accepted and do nothing.
	add(invocation{name: "H without recursive", args: []string{"-H", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "L without recursive", args: []string{"-L", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "P without recursive", args: []string{"-P", good, "f"}, seedTree: chownTree, ownership: true})

	// The one refused combination, and the three spellings that lift it.
	add(invocation{name: "recursive dereference alone", args: []string{"--dereference", "-R"}})
	add(invocation{name: "recursive dereference with a spec", args: []string{"--dereference", "-R", good}})
	add(invocation{name: "recursive dereference with a file", args: []string{"-R", "--dereference", good, "f"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "recursive dereference with H", args: []string{"-R", "--dereference", "-H", good, "d"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "recursive dereference with L", args: []string{"-R", "--dereference", "-L", good, "d"}, seedTree: chownTree, ownership: true})
}

// chownRootCases are the --preserve-root failsafe. They name `/` and are safe
// because the refusal happens before anything is touched; the spec is a bad
// one wherever the refusal is not what is being tested.
func chownRootCases(add func(invocation), good string) {
	add(invocation{name: "preserve-root refuses slash", args: []string{"-R", "--preserve-root", good, "/"}})
	add(invocation{name: "preserve-root refuses double slash", args: []string{"-R", "--preserve-root", good, "//"}})
	add(invocation{name: "preserve-root refuses slash dot", args: []string{"-R", "--preserve-root", good, "/."}})
	add(invocation{name: "preserve-root refuses slash dot slash", args: []string{"-R", "--preserve-root", good, "/./"}})
	// Without -R there is no failsafe: it just tries, and fails or does not
	// depending on who is running.
	add(invocation{name: "preserve-root without recursive", args: []string{"--preserve-root", good, "/"}})
	// The spec is parsed BEFORE the failsafe is asked.
	add(invocation{name: "a bad spec outranks the failsafe", args: []string{"-R", "--preserve-root", "zzznope", "/"}})
	add(invocation{name: "no-preserve-root is the default", args: []string{"--no-preserve-root", "-R", "zzznope", "/"}})
	add(invocation{name: "last of the two root flags wins", args: []string{"--preserve-root", "--no-preserve-root", "-R", "zzznope", "/"}})
	add(invocation{name: "and the other way round", args: []string{"--no-preserve-root", "--preserve-root", "-R", good, "/"}})
	// The refusal is per operand and does not stop the run.
	add(invocation{name: "the run continues past the refusal", args: []string{"-R", "--preserve-root", good, "/", "nope"}, seedTree: chownTree, ownership: true})
	add(invocation{name: "silent does not suppress the refusal", args: []string{"-R", "-f", "--preserve-root", good, "/"}})
}

func chownCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

	chownSpecCases(add, "chown")
	chownOptionCases(add, "root")
	chownRootCases(add, "root")

	tree := func(name string, args ...string) {
		add(invocation{name: name, args: args, seedTree: chownTree, ownership: true})
	}

	// ---- the verbose wording -------------------------------------------
	// Every one of these is a spec the process cannot apply unless it is
	// root, and both sides meet the same answer either way. What they pin
	// is which NOUN the line uses and how the "to" half renders, which is
	// not simply the spec typed back: a numeric user beside a symbolic
	// group disappears from it.
	tree("verbose user by name", "-v", "root", "f")
	tree("verbose user and group by name", "-v", "root:daemon", "f")
	tree("verbose group by name", "-v", ":daemon", "f")
	tree("verbose group by number", "-v", ":1", "f")
	tree("verbose user by number", "-v", "0", "f")
	tree("verbose both by number", "-v", "0:1", "f")
	tree("verbose number user named group", "-v", "0:daemon", "f")
	tree("verbose named user number group", "-v", "root:1", "f")
	tree("verbose login group", "-v", "root:", "f")
	tree("verbose empty spec", "-v", "", "f")
	tree("verbose colon spec", "-v", ":", "f")
	tree("changes user by name", "-c", "root", "f")
	tree("changes group by name", "-c", ":daemon", "f")
	tree("silent user by name", "-f", "root", "f")
	tree("silent and verbose together", "-f", "-v", "root", "f")
	tree("verbose then changes", "-v", "-c", "root", "f")
	tree("changes then verbose", "-c", "-v", "root", "f")

	// ---- entries that cannot be reached ---------------------------------
	tree("a name that is not there", "root", "nope")
	tree("verbose on a name that is not there", "-v", "root", "nope")
	tree("changes on a name that is not there", "-c", "root", "nope")
	tree("silent on a name that is not there", "-f", "root", "nope")
	tree("silent and verbose on a name that is not there", "-f", "-v", "root", "nope")
	tree("two names that are not there", "-v", "root", "nope1", "nope2")
	tree("a good name and a bad one", "root", "f", "nope")
	tree("a bad name and a good one", "root", "nope", "f")
	tree("a name below a name that is not there", "root", "nope/deeper")

	// ---- symlinks --------------------------------------------------------
	tree("a symlink is followed by default", "-v", ":daemon", "lnk")
	tree("no-dereference short", "-h", "-v", ":daemon", "lnk")
	tree("no-dereference long", "--no-dereference", "-v", ":daemon", "lnk")
	tree("dereference explicitly", "--dereference", "-v", ":daemon", "lnk")
	tree("dereference then no-dereference", "--dereference", "-h", "-v", ":daemon", "lnk")
	tree("no-dereference then dereference", "-h", "--dereference", "-v", ":daemon", "lnk")
	// A broken link followed is `cannot dereference`; not followed it is an
	// ordinary entry. The `-v` line is left off the followed form: GNU fills
	// its "from" clause from a stat that failed.
	tree("a broken link followed", ":daemon", "broken")
	tree("a broken link followed, silent", "-f", ":daemon", "broken")
	tree("a broken link followed, changes", "-c", ":daemon", "broken")
	tree("a broken link not followed", "-h", "-v", ":daemon", "broken")

	// ---- the walk --------------------------------------------------------
	tree("recursive", "-R", "-v", ":daemon", "d")
	tree("recursive quiet", "-R", ":daemon", "d")
	tree("recursive changes", "-R", "-c", ":daemon", "d")
	tree("recursive over two operands", "-R", "-v", ":daemon", "d", "f")
	tree("recursive on a symlinked directory", "-R", "-v", ":daemon", "symdir")
	tree("recursive H on a symlinked directory", "-R", "-H", "-v", ":daemon", "symdir")
	tree("recursive L on a symlinked directory", "-R", "-L", "-v", ":daemon", "symdir")
	tree("recursive P on a symlinked directory", "-R", "-P", "-v", ":daemon", "symdir")
	tree("L then P", "-R", "-L", "-P", "-v", ":daemon", "symdir")
	tree("P then L", "-R", "-P", "-L", "-v", ":daemon", "symdir")
	tree("H then L", "-R", "-H", "-L", "-v", ":daemon", "symdir")
	tree("L then H", "-R", "-L", "-H", "-v", ":daemon", "symdir")
	tree("recursive with no-dereference", "-R", "-h", "-v", ":daemon", "d")
	tree("no-dereference loses to L", "-R", "-h", "-L", "-v", ":daemon", "d")
	tree("L loses to nothing", "-R", "-L", "-h", "-v", ":daemon", "d")
	tree("recursive on a plain file", "-R", "-v", ":daemon", "f")
	tree("recursive on a name that is not there", "-R", "-v", ":daemon", "nope")

	// ---- --reference -----------------------------------------------------
	tree("reference", "--reference=d", "f")
	tree("reference verbose", "--reference=d", "-v", "f")
	tree("reference changes", "--reference=d", "-c", "f")
	tree("reference itself", "--reference=f", "-v", "f")
	tree("reference is not there", "--reference=nope", "f")
	tree("reference is empty", "--reference=", "f")
	tree("reference with a spec operand", "--reference=d", "root", "f")
	tree("reference as two arguments", "--reference", "d", "f")
	tree("reference twice", "--reference=d", "--reference=f", "-v", "f")
	tree("reference outranks a bad spec", "--reference=nope", "zzznope", "f")

	// ---- --from ----------------------------------------------------------
	// `--f` is `--from`, which takes an argument — so it swallows the spec
	// and the FILE becomes the spec. chown-only: chgrp did not have the
	// option until after 9.1 (see chgrp_test.go).
	tree("f prefix is from", "--f", "root", "f")
	tree("from that cannot match", "--from=root:daemon", "-v", ":daemon", "f")
	tree("from that cannot match, changes", "--from=root:daemon", "-c", ":daemon", "f")
	tree("from that cannot match, quiet", "--from=root:daemon", ":daemon", "f")
	tree("from group only that cannot match", "--from=:daemon", "-v", ":daemon", "f")
	tree("from empty matches everything", "--from=", "-v", ":daemon", "f")
	tree("from with an unknown user", "--from=zzznope", "-v", "root", "f")
	tree("from with an unknown group", "--from=:zzznope", "-v", "root", "f")
	tree("from with a dot", "--from=root.daemon", "-v", "root", "f")
	tree("from and reference together", "--from=root", "--reference=d", "-v", "f")
	tree("from recursive", "-R", "--from=root:daemon", "-v", ":daemon", "d")

	return cases
}

func TestChown(t *testing.T) {
	requireParity(t, "chown", chownCases(t))
}

// The two options whose TEXT is ours by design (docs/COREUTILS.md).
func TestChownHelp(t *testing.T) {
	requireHelp(t, "chown", []string{"--help"}, 0)
	requireHelp(t, "chown", []string{"--h"}, 0)
	requireHelp(t, "chown", []string{"--help", "extra"}, 0)
	requireHelp(t, "chown", []string{"-v", "--help"}, 0)
	requireHelp(t, "chown", []string{"zzznope", "--help"}, 0)
	requireVersion(t, "chown", []string{"--version"}, 0)
	requireVersion(t, "chown", []string{"--versio"}, 0)
	requireVersion(t, "chown", []string{"--version", "extra"}, 0)
}
