package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// pathchk is three checks wearing one name, and the corpus is built around
// telling them apart: the DEFAULT asks the filesystem (one `lstat`, and a
// component walk only for a name that does not exist), `-p` never touches the
// filesystem at all and holds the name to POSIX's minimums and its portable
// character set, and `-P` adds two rules about the name's SHAPE to the
// default's. `-p -P` is none of the three: `-p`'s refusal to look at the
// filesystem wins and `-P`'s two rules are added to it.
//
// Every rule here is a boundary, so each length is asked AT the limit and on
// both sides of it, and every case that can fail two checks at once is here
// for the PRECEDENCE rather than for the fault — that is the part a
// reimplementation gets wrong.
//
// The operands are relative and the fixture is the working directory, so the
// two sides diagnose the same text: an absolute operand would carry the
// harness's temporary directory into stderr, where the two runs differ.

func init() {
	registerCorpus("pathchk", pathchkCases)
}

// pathchkTree is the fixture the filesystem-mode cases are asked about: one
// of every path shape whose errno the default mode reports, plus a directory
// whose own name is at NAME_MAX so that a component under it can be asked
// about while the path still exists.
func pathchkTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mk := func(name string) {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	mk("dir/sub")
	mk(strings.Repeat("n", 255))
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	link := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatalf("symlink %s: %v", name, err)
		}
	}
	link("nowhere", "dangling")
	// A symlink to itself: `loop` alone is a path that exists under lstat,
	// `loop/x` is ELOOP.
	link("loop", "loop")
	link("dir", "dirlink")
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	// A directory nothing may search. The mode goes back before t.TempDir's
	// own cleanup, which was registered first and so runs last.
	noperm := filepath.Join(dir, "noperm")
	mk("noperm/sub")
	if err := os.Chmod(noperm, 0); err != nil {
		t.Fatalf("chmod noperm: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(noperm, 0o755) })
	return dir
}

func pathchkCases(t *testing.T) []invocation {
	dir := pathchkTree(t)
	// Names at and around the two limits the default mode holds a name to.
	// 255 is NAME_MAX on every filesystem the gate runs on; the path limit is
	// the kernel's, which differs between Linux and Darwin — both sides meet
	// the same one, so the case still compares.
	a := func(n int) string { return strings.Repeat("a", n) }
	deep := func(n int) string {
		p := "nodir"
		for len(p) < n {
			p += "/a"
		}
		return p[:n]
	}
	// A path whose components are all short enough for POSIX's own minimum,
	// grown to a given total length: the -p total-length check with nothing
	// else to trip over.
	wide := func(n int) string {
		p := "a"
		for len(p) < n {
			p += "/aaaaaaaaaaaaa"
		}
		return p[:n]
	}

	return []invocation{
		// ---- the default mode: the filesystem answers -------------------
		// An existing path is valid by construction whatever is in it.
		{name: "an existing file", args: []string{"file"}, dir: dir},
		{name: "an existing directory", args: []string{"dir"}, dir: dir},
		{name: "an existing path with a trailing slash", args: []string{"dir/"}, dir: dir},
		{name: "an existing path with doubled slashes", args: []string{"dir//sub"}, dir: dir},
		{name: "a symlink is not followed", args: []string{"dangling"}, dir: dir},
		{name: "a symlink loop is a path", args: []string{"loop"}, dir: dir},
		{name: "a fifo", args: []string{"fifo"}, dir: dir},
		{name: "the root", args: []string{"/"}, dir: dir},
		{name: "doubled root", args: []string{"//"}, dir: dir},
		{name: "dot", args: []string{"."}, dir: dir},
		{name: "dot dot", args: []string{".."}, dir: dir},

		// A path that does not exist is NOT a fault: pathchk asks whether the
		// name could be created, not whether it was.
		{name: "a missing name", args: []string{"nosuch"}, dir: dir},
		{name: "a missing path", args: []string{"nodir/deeper/still"}, dir: dir},
		{name: "a missing name under a dangling symlink", args: []string{"dangling/x"}, dir: dir},

		// Every other errno IS the diagnostic, quoted as gnulib quotes a name
		// in an errno message: bare unless it needs a shell's quotes.
		{name: "a file used as a directory", args: []string{"file/x"}, dir: dir},
		{name: "a file with a trailing slash", args: []string{"file/"}, dir: dir},
		{name: "a fifo used as a directory", args: []string{"fifo/x"}, dir: dir},
		{name: "a symlink loop as a directory", args: []string{"loop/x"}, dir: dir},
		{name: "the empty name", args: []string{""}, dir: dir},
		{name: "a name that is not valid UTF-8", args: []string{"\xff\xfe"}, dir: dir},
		{name: "a name needing quotes in an errno message", args: []string{"a b/x"}, dir: dir},
		// EACCES, on a runner that is not root. As root the directory is
		// searchable and both sides say so, which is still an agreement.
		{name: "a directory that cannot be searched", args: []string{"noperm/sub"}, dir: dir},
		{name: "a name under a directory that cannot be searched", args: []string{"noperm/sub/x"}, dir: dir},
		{name: "-P under a directory that cannot be searched", args: []string{"-P", "noperm/sub/x"}, dir: dir},
		{name: "an errno message for a name with a newline", args: []string{"a\nb/x"}, dir: dir},

		// ---- the default mode: the component walk -----------------------
		// Reached only once the path does not exist. Under an existing
		// directory the kernel answers first, and its wording is different
		// from the walk's; at and on both sides of NAME_MAX.
		{name: "a name at NAME_MAX", args: []string{a(255)}, dir: dir},
		{name: "a name one under NAME_MAX", args: []string{a(254)}, dir: dir},
		{name: "a name two under NAME_MAX", args: []string{a(253)}, dir: dir},
		{name: "a name one over NAME_MAX", args: []string{a(256)}, dir: dir},
		{name: "a name well over NAME_MAX", args: []string{a(300)}, dir: dir},
		{name: "a long name under an existing directory", args: []string{"dir/" + a(256)}, dir: dir},
		{name: "a name at the limit under an existing directory", args: []string{"dir/" + a(255)}, dir: dir},
		{name: "a long name under a missing directory", args: []string{"nodir/" + a(256)}, dir: dir},
		{name: "a name at the limit under a missing directory", args: []string{"nodir/" + a(255)}, dir: dir},
		{name: "a long name below a directory at the limit", args: []string{strings.Repeat("n", 255) + "/" + a(256)}, dir: dir},
		{name: "a name at the limit below one at the limit", args: []string{strings.Repeat("n", 255) + "/" + a(255)}, dir: dir},
		{name: "the walk reports the FIRST long component", args: []string{"nodir/" + a(300) + "/" + a(260)}, dir: dir},
		{name: "a long component before a missing one", args: []string{a(256) + "/x"}, dir: dir},
		{name: "a long middle component", args: []string{"dir/" + a(256) + "/b"}, dir: dir},
		{name: "an absolute long component", args: []string{"/" + a(256)}, dir: dir},
		{name: "the walk skips empty components", args: []string{"nodir//" + a(256)}, dir: dir},
		{name: "a long component with a trailing slash", args: []string{"nodir/" + a(256) + "/"}, dir: dir},
		// A component just past POSIX's own minimum is where GNU starts
		// asking the filesystem at all; either side of that.
		{name: "a component at the POSIX minimum", args: []string{"nodir/" + a(14)}, dir: dir},
		{name: "a component one past the POSIX minimum", args: []string{"nodir/" + a(15)}, dir: dir},

		// The whole path's length is the kernel's answer, not a check of its
		// own: at and around PATH_MAX, with every component short.
		{name: "a path near PATH_MAX", args: []string{deep(4094)}, dir: dir},
		{name: "a path at PATH_MAX less one", args: []string{deep(4095)}, dir: dir},
		{name: "a path at PATH_MAX", args: []string{deep(4096)}, dir: dir},
		{name: "a path one past PATH_MAX", args: []string{deep(4097)}, dir: dir},
		{name: "a long path with a long component", args: []string{"nodir/" + a(300) + "/" + deep(4000)}, dir: dir},
		{name: "a long path is not the POSIX limit", args: []string{deep(300)}, dir: dir},

		// ---- -p: POSIX minimums, and never the filesystem ---------------
		{name: "-p on an existing file", args: []string{"-p", "file"}, dir: dir},
		{name: "-p ignores a file used as a directory", args: []string{"-p", "file/x"}, dir: dir},
		{name: "-p ignores a symlink loop", args: []string{"-p", "loop/x"}, dir: dir},
		{name: "-p ignores a missing path", args: []string{"-p", "nodir/deeper"}, dir: dir},
		{name: "-p the empty name", args: []string{"-p", ""}, dir: dir},
		{name: "-p the root", args: []string{"-p", "/"}, dir: dir},
		{name: "-p doubled slashes", args: []string{"-p", "a//b"}, dir: dir},
		{name: "-p allows a leading dash", args: []string{"-p", "--", "-foo"}, dir: dir},

		// The name limit: _POSIX_NAME_MAX, at and on both sides.
		{name: "-p a component one under the limit", args: []string{"-p", a(13)}, dir: dir},
		{name: "-p a component at the limit", args: []string{"-p", a(14)}, dir: dir},
		{name: "-p a component one over the limit", args: []string{"-p", a(15)}, dir: dir},
		{name: "-p a component well over the limit", args: []string{"-p", a(16)}, dir: dir},
		{name: "-p reports the first long component", args: []string{"-p", a(15) + "/" + a(20)}, dir: dir},
		{name: "-p a long component after a short one", args: []string{"-p", "ok/" + a(15)}, dir: dir},

		// The path limit: _POSIX_PATH_MAX, reported as one less because the
		// limit counts the NUL a length does not.
		{name: "-p a path two under the limit", args: []string{"-p", wide(254)}, dir: dir},
		{name: "-p a path at the limit", args: []string{"-p", wide(255)}, dir: dir},
		{name: "-p a path one over the limit", args: []string{"-p", wide(256)}, dir: dir},
		{name: "-p a path two over the limit", args: []string{"-p", wide(257)}, dir: dir},

		// The portable filename character set: the set itself, and its edges.
		{name: "-p the portable set", args: []string{"-p", "aZ09._-/x"}, dir: dir},
		{name: "-p a space", args: []string{"-p", "a b"}, dir: dir},
		{name: "-p a plus", args: []string{"-p", "a+b"}, dir: dir},
		{name: "-p a comma", args: []string{"-p", "a,b"}, dir: dir},
		{name: "-p a colon", args: []string{"-p", "a:b"}, dir: dir},
		{name: "-p an at sign", args: []string{"-p", "a@b"}, dir: dir},
		{name: "-p a tilde", args: []string{"-p", "a~b"}, dir: dir},
		{name: "-p an equals sign", args: []string{"-p", "a=b"}, dir: dir},
		{name: "-p a percent", args: []string{"-p", "a%b"}, dir: dir},
		{name: "-p a hash", args: []string{"-p", "a#b"}, dir: dir},
		{name: "-p a star", args: []string{"-p", "a*b"}, dir: dir},
		// The offending byte is quoted as a C escape, and so is the name.
		{name: "-p a newline", args: []string{"-p", "a\nb"}, dir: dir},
		{name: "-p a tab", args: []string{"-p", "a\tb"}, dir: dir},
		{name: "-p a backslash", args: []string{"-p", "a\\b"}, dir: dir},
		{name: "-p an apostrophe", args: []string{"-p", "a'b"}, dir: dir},
		{name: "-p a double quote", args: []string{"-p", "a\"b"}, dir: dir},
		{name: "-p a byte that is not ASCII", args: []string{"-p", "ab\xc3\xa9"}, dir: dir},
		{name: "-p a byte that is not valid UTF-8", args: []string{"-p", "ab\xff"}, dir: dir},
		{name: "-p a leading tilde", args: []string{"-p", "~ab"}, dir: dir},

		// ---- -P: the two shape rules on top of the default --------------
		{name: "-P the empty name", args: []string{"-P", ""}, dir: dir},
		{name: "-P a leading dash", args: []string{"-P", "--", "-foo"}, dir: dir},
		{name: "-P a lone dash", args: []string{"-P", "--", "-"}, dir: dir},
		{name: "-P a dash in a later component", args: []string{"-P", "a/-b/c"}, dir: dir},
		{name: "-P a dash after a slash", args: []string{"-P", "./-x"}, dir: dir},
		{name: "-P a dash after the root", args: []string{"-P", "/-x"}, dir: dir},
		{name: "-P a dash inside a component is fine", args: []string{"-P", "a-b"}, dir: dir},
		{name: "-P still asks the filesystem", args: []string{"-P", "file/x"}, dir: dir},
		{name: "-P still walks components", args: []string{"-P", "nodir/" + a(256)}, dir: dir},
		{name: "-P still reports a long name", args: []string{"-P", a(300)}, dir: dir},
		{name: "-P an empty component is not an empty name", args: []string{"-P", "a//b"}, dir: dir},
		{name: "-P a trailing slash is not an empty component", args: []string{"-P", "a/"}, dir: dir},
		{name: "-P on an existing file", args: []string{"-P", "file"}, dir: dir},

		// ---- the precedence between the checks --------------------------
		// Measured rather than derived: a name can fail several at once.
		{name: "empty beats everything", args: []string{"-p", "-P", ""}, dir: dir},
		{name: "a dash beats a non-portable character", args: []string{"-p", "-P", "--", "-a%b"}, dir: dir},
		{name: "a later dash still beats an earlier bad character", args: []string{"-p", "-P", "--", "a%b/-c"}, dir: dir},
		{name: "a dash beats a long component", args: []string{"-p", "-P", "--", "-" + a(20)}, dir: dir},
		{name: "a dash beats a long path", args: []string{"-p", "-P", "--", "-" + wide(300)}, dir: dir},
		{name: "a bad character beats a long component", args: []string{"-p", a(20) + "%"}, dir: dir},
		{name: "a bad character in a later component still wins", args: []string{"-p", a(20) + "/x%y"}, dir: dir},
		{name: "a bad character beats a long path", args: []string{"-p", "x%y" + wide(300)}, dir: dir},
		{name: "a long path beats a long component", args: []string{"-p", a(25) + wide(300)}, dir: dir},
		{name: "-P a dash beats an errno", args: []string{"-P", "file/-x"}, dir: dir},
		{name: "-P a dash beats a long name", args: []string{"-P", "--", "-" + a(300)}, dir: dir},
		{name: "-P a dash in the first component beats a later errno", args: []string{"-P", "--", "-dir/file/x"}, dir: dir},

		// ---- -p -P is not either of them --------------------------------
		{name: "-p -P leaves the filesystem alone", args: []string{"-p", "-P", "file/x"}, dir: dir},
		{name: "--portability leaves the filesystem alone", args: []string{"--portability", "file/x"}, dir: dir},
		{name: "--portability is -p -P", args: []string{"--portability", "--", "-a"}, dir: dir},
		{name: "--portability holds POSIX's name limit", args: []string{"--portability", a(15)}, dir: dir},
		{name: "-p alone allows a leading dash", args: []string{"-p", "--", "-a"}, dir: dir},
		{name: "-P alone allows a non-portable character", args: []string{"-P", "a%b"}, dir: dir},
		{name: "-P alone allows a long component", args: []string{"-P", "nodir/" + a(20)}, dir: dir},
		{name: "-pP clustered", args: []string{"-pP", "--", "-a%b"}, dir: dir},
		{name: "-Pp clustered", args: []string{"-Pp", "--", "-a%b"}, dir: dir},
		{name: "-p repeated", args: []string{"-p", "-p", "a%b"}, dir: dir},
		{name: "-P repeated", args: []string{"-P", "-P", "--", "-a"}, dir: dir},
		{name: "--portability then -p", args: []string{"--portability", "-p", "--", "-a"}, dir: dir},

		// ---- several operands -------------------------------------------
		{name: "every operand is checked", args: []string{"-p", "a%b", "c@d", "ok"}, dir: dir},
		{name: "one bad operand among good ones", args: []string{"-p", "ok", "a%b", "ok2"}, dir: dir},
		{name: "a good operand after a bad one", args: []string{"file", "file/x", "dir"}, dir: dir},
		{name: "two faults of different kinds", args: []string{"-P", "", "--", "-a"}, dir: dir},
		{name: "all operands good", args: []string{"file", "dir", "nosuch"}, dir: dir},

		// ---- the option scan --------------------------------------------
		// It does NOT permute: the first operand ends the options, so an
		// option after a name is another name.
		{name: "an option after an operand is an operand", args: []string{"file", "-p"}, dir: dir},
		{name: "the scan stops before a long option too", args: []string{"file", "--portability"}, dir: dir},
		{name: "an unknown option after an operand", args: []string{"file", "-x"}, dir: dir},
		{name: "--help after an operand", args: []string{"file", "--help"}, dir: dir},
		{name: "an operand before -p is checked in the default mode", args: []string{a(20), "-p"}, dir: dir},
		{name: "the empty operand before -p", args: []string{"", "-p"}, dir: dir},
		{name: "POSIXLY_CORRECT changes nothing", args: []string{"file", "-p"}, env: []string{"POSIXLY_CORRECT=1"}, dir: dir},

		{name: "no operands", args: []string{}, dir: dir},
		{name: "only options", args: []string{"-p"}, dir: dir},
		{name: "only dashdash", args: []string{"--"}, dir: dir},
		{name: "dashdash then a dash name", args: []string{"--", "-foo"}, dir: dir},
		{name: "a lone dash is an operand", args: []string{"-"}, dir: dir},
		{name: "a lone dash after dashdash", args: []string{"--", "-"}, dir: dir},
		{name: "dashdash is not a name", args: []string{"--", "--"}, dir: dir},
		{name: "an unknown short option", args: []string{"-x"}, dir: dir},
		{name: "an unknown option in a cluster", args: []string{"-px", "a"}, dir: dir},
		{name: "an unknown long option", args: []string{"--foo", "a"}, dir: dir},
		{name: "a long option by prefix", args: []string{"--port", ""}, dir: dir},
		{name: "a long option by one letter", args: []string{"--p", ""}, dir: dir},
		{name: "a long option given a value", args: []string{"--portability=x", ""}, dir: dir},
		{name: "--help given a value", args: []string{"--help=x"}, dir: dir},
		{name: "--version given a value", args: []string{"--version=x"}, dir: dir},
		{name: "an uppercase long option does not exist", args: []string{"--P", "a"}, dir: dir},

		// ---- the two standard options and a failing stdout ---------------
		{name: "stdout closed on help", args: []string{"--help"}, stdout: stdoutClosed, dir: dir},
		{name: "stdout full on help", args: []string{"--help"}, stdout: stdoutFull, dir: dir},
		{name: "stdout closed on version", args: []string{"--version"}, stdout: stdoutClosed, dir: dir},
		{name: "stdout full on version", args: []string{"--version"}, stdout: stdoutFull, dir: dir},
		// pathchk writes nothing on stdout, so an unusable one costs it
		// nothing on the paths that are not --help / --version.
		{name: "stdout closed on a good name", args: []string{"file"}, stdout: stdoutClosed, dir: dir},
		{name: "stdout full on a good name", args: []string{"file"}, stdout: stdoutFull, dir: dir},
		{name: "stdout closed on a bad name", args: []string{"-p", "a%b"}, stdout: stdoutClosed, dir: dir},
		{name: "stdout closed on a usage error", args: []string{"-x"}, stdout: stdoutClosed, dir: dir},
	}
}

func TestPathchkParity(t *testing.T) {
	requireParity(t, "pathchk", pathchkCases(t))
}

func TestPathchkHelpVersion(t *testing.T) {
	requireHelp(t, "pathchk", []string{"--help"}, 0)
	requireHelp(t, "pathchk", []string{"--hel"}, 0)
	requireHelp(t, "pathchk", []string{"--help", "a"}, 0)
	requireHelp(t, "pathchk", []string{"-p", "--help"}, 0)
	requireVersion(t, "pathchk", []string{"--version"}, 0)
	requireVersion(t, "pathchk", []string{"--vers"}, 0)
	requireVersion(t, "pathchk", []string{"-P", "--version"}, 0)
}
