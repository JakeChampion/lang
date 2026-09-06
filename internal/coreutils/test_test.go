package coreutils

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestTestParity holds coreutils/test.fern to GNU test, and
// TestBracketParity holds coreutils/[.fern to GNU [. The two share
// `condCases`, the expression corpus, because the two utilities share
// their evaluator: `[` is `test` with a closing `]` and with `--help` /
// `--version` honoured as the sole argument.
//
// The corpus follows the grammar: the argument-count table POSIX fixes
// for one to four arguments and GNU's general parser beyond it, then
// each operator family — strings, integers (compared as digit strings of
// any length), the file predicates over a tree the test builds, the
// file-pair operators, `-t`, and `-l STRING`. Diagnostics quote the
// offending token, so operands with bytes worth escaping appear on the
// error paths.

// condTree is the fixture tree the file predicates are asked about. Every
// kind of file `test` can distinguish is here, so each predicate has a
// path on which it is true and paths on which it is false.
type condTree struct {
	dir, file, empty, subdir, fifo, sock, link, dirlink, dangling string
	setuid, setgid, sticky, stickydir, exec, noperm, hardlink     string
	older, newer, sameA, sameB, unread, read, blk, missing        string
}

// condFixtures builds the tree under t.TempDir().
//
// The timestamps are pinned rather than left to the clock: `-nt` / `-ot`
// compare to the nanosecond, so `sameA` / `sameB` share a second and
// differ below it, and `unread` is a file whose mtime is past its atime
// (what `-N` asks) while `read` is the other way round.
func condFixtures(t *testing.T) condTree {
	t.Helper()
	dir := t.TempDir()
	c := condTree{dir: dir}
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		// WriteFile applies the umask; Chmod sets every bit asked for.
		if err := os.Chmod(p, mode); err != nil {
			t.Fatalf("chmod %s: %v", name, err)
		}
		return p
	}
	c.file = write("file", "hello\n", 0o644)
	c.empty = write("empty", "", 0o644)
	c.setuid = write("setuid", "#!/bin/sh\n", 0o755|os.ModeSetuid)
	c.setgid = write("setgid", "#!/bin/sh\n", 0o755|os.ModeSetgid)
	c.sticky = write("sticky", "x", 0o644|os.ModeSticky)
	c.exec = write("exec", "#!/bin/sh\n", 0o755)
	c.noperm = write("noperm", "secret", 0)
	c.older = write("older", "a", 0o644)
	c.newer = write("newer", "b", 0o644)
	c.sameA = write("sameA", "a", 0o644)
	c.sameB = write("sameB", "b", 0o644)
	c.unread = write("unread", "u", 0o644)
	c.read = write("read", "r", 0o644)

	c.subdir = filepath.Join(dir, "subdir")
	if err := os.Mkdir(c.subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	c.stickydir = filepath.Join(dir, "stickydir")
	if err := os.Mkdir(c.stickydir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(c.stickydir, 0o1777); err != nil {
		t.Fatalf("chmod sticky dir: %v", err)
	}
	c.fifo = filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(c.fifo, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	c.sock = filepath.Join(dir, "sock")
	ln, err := net.Listen("unix", c.sock)
	if err != nil {
		t.Fatalf("unix socket: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	c.link = filepath.Join(dir, "link")
	if err := os.Symlink(c.file, c.link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	c.dirlink = filepath.Join(dir, "dirlink")
	if err := os.Symlink(c.subdir, c.dirlink); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	c.dangling = filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), c.dangling); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	c.hardlink = filepath.Join(dir, "hardlink")
	if err := os.Link(c.file, c.hardlink); err != nil {
		t.Fatalf("hard link: %v", err)
	}
	c.missing = filepath.Join(dir, "missing")
	c.blk = blockDevice(t, dir)

	epoch := time.Unix(1_600_000_000, 0)
	chtimes := func(p string, atime, mtime time.Time) {
		if err := os.Chtimes(p, atime, mtime); err != nil {
			t.Fatalf("chtimes %s: %v", p, err)
		}
	}
	chtimes(c.older, epoch, epoch)
	chtimes(c.newer, epoch, epoch.Add(100*time.Second))
	chtimes(c.sameA, epoch, epoch.Add(500*time.Millisecond))
	chtimes(c.sameB, epoch, epoch.Add(700*time.Millisecond))
	chtimes(c.unread, epoch, epoch.Add(time.Second))
	chtimes(c.read, epoch.Add(time.Second), epoch)
	return c
}

// blockDevice returns a block special file for `-b`: one made in `dir`
// when the process may mknod, else any the system already has. Not
// having one at all is a missing fixture, not a reason to skip.
func blockDevice(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "blk")
	// Loop device 0 — the major is Linux's; the node only has to exist.
	if err := syscall.Mknod(p, syscall.S_IFBLK|0o600, 7<<8); err == nil {
		return p
	}
	matches, _ := filepath.Glob("/dev/*")
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.Mode()&os.ModeDevice != 0 && st.Mode()&os.ModeCharDevice == 0 {
			return m
		}
	}
	t.Fatalf("no block device: mknod in %s is not permitted and /dev has none", dir)
	return ""
}

// condCases is the expression corpus both utilities run. `[` appends
// its `]` in bracketCases.
func condCases(t *testing.T) []invocation {
	t.Helper()
	f := condFixtures(t)
	return []invocation{
		// Zero to four arguments: the count picks the reading.
		{name: "no arguments"},
		{name: "one string", args: []string{"x"}},
		{name: "one empty string", args: []string{""}},
		{name: "one bang", args: []string{"!"}},
		{name: "one dash n", args: []string{"-n"}},
		{name: "one dash z", args: []string{"-z"}},
		{name: "one dash f", args: []string{"-f"}},
		{name: "one dash a", args: []string{"-a"}},
		{name: "one dash o", args: []string{"-o"}},
		{name: "one dash x", args: []string{"-x"}},
		{name: "one dash e", args: []string{"-e"}},
		{name: "one dash l", args: []string{"-l"}},
		{name: "one dash t", args: []string{"-t"}},
		{name: "one dash eq", args: []string{"-eq"}},
		{name: "one dashdash", args: []string{"--"}},
		{name: "one dash", args: []string{"-"}},
		{name: "one open paren", args: []string{"("}},
		{name: "one close paren", args: []string{")"}},
		{name: "one equals", args: []string{"="}},
		{name: "one non UTF-8", args: []string{"\xff"}},
		{name: "two bangs", args: []string{"!", "!"}},
		{name: "three bangs", args: []string{"!", "!", "!"}},
		{name: "four bangs", args: []string{"!", "!", "!", "!"}},
		{name: "bang then string", args: []string{"!", "x"}},
		{name: "bang then empty", args: []string{"!", ""}},
		{name: "bang then dash a", args: []string{"!", "-a"}},
		{name: "bang then dash n", args: []string{"!", "-n"}},
		{name: "two strings", args: []string{"x", "y"}},
		{name: "two empty strings", args: []string{"", ""}},
		{name: "dash a then string", args: []string{"-a", "x"}},
		{name: "dash o then string", args: []string{"-o", "x"}},
		{name: "dashdash then string", args: []string{"--", "x"}},
		{name: "dashdash twice", args: []string{"--", "--"}},
		{name: "dash then string", args: []string{"-", "x"}},
		{name: "string then dash", args: []string{"x", "-"}},
		{name: "string then dash a", args: []string{"x", "-a"}},
		{name: "string then equals", args: []string{"x", "="}},
		{name: "equals then string", args: []string{"=", "x"}},
		{name: "dash eq then one", args: []string{"-eq", "1"}},
		{name: "one then dash eq", args: []string{"1", "-eq"}},
		{name: "dash x twice", args: []string{"-x", "-x"}},
		{name: "dash e twice", args: []string{"-e", "-e"}},
		{name: "dash n then dash a", args: []string{"-n", "-a"}},
		{name: "dash z then dash a", args: []string{"-z", "-a"}},
		{name: "dash n twice", args: []string{"-n", "-n"}},
		{name: "dash z twice", args: []string{"-z", "-z"}},
		{name: "dash z then dash n", args: []string{"-z", "-n"}},
		{name: "dash l then string", args: []string{"-l", "x"}},
		{name: "dash t then dash t", args: []string{"-t", "-t"}},
		{name: "dash t then dash a", args: []string{"-t", "-a"}},
		{name: "open paren then string", args: []string{"(", "x"}},
		{name: "string then close paren", args: []string{"x", ")"}},
		{name: "empty parens", args: []string{"(", ")"}},
		{name: "unary with an unknown letter", args: []string{"-q", "x"}},
		{name: "unary with an upper case letter", args: []string{"-E", "x"}},
		{name: "unary with a digit", args: []string{"-1", "x"}},
		{name: "unary with a non UTF-8 byte", args: []string{"-\xff", "x"}},
		{name: "three strings", args: []string{"x", "y", "z"}},
		{name: "three empty strings", args: []string{"", "", ""}},
		{name: "three dash x", args: []string{"-x", "-x", "-x"}},
		{name: "three dash a", args: []string{"-a", "-a", "-a"}},
		{name: "three dash eq", args: []string{"-eq", "-eq", "-eq"}},
		{name: "three equals", args: []string{"=", "=", "="}},
		{name: "dash a equals dash a", args: []string{"-a", "=", "-a"}},
		{name: "dash o equals dash o", args: []string{"-o", "=", "-o"}},
		{name: "dash n equals dash n", args: []string{"-n", "=", "-n"}},
		{name: "dash a eq dash a", args: []string{"-a", "-eq", "-a"}},
		{name: "dash z eq dash z", args: []string{"-z", "-eq", "-z"}},
		{name: "dash n eq dash n", args: []string{"-n", "-eq", "-n"}},
		{name: "parenthesised string", args: []string{"(", "x", ")"}},
		{name: "parenthesised empty", args: []string{"(", "", ")"}},
		{name: "parenthesised close paren", args: []string{"(", ")", ")"}},
		{name: "parenthesised dash a", args: []string{"(", "-a", ")"}},
		{name: "parenthesised bang", args: []string{"(", "!", ")"}},
		{name: "parenthesised dash n", args: []string{"(", "-n", ")"}},
		{name: "parenthesised equals is a comparison", args: []string{"(", "=", ")"}},
		{name: "parenthesised string then string", args: []string{"(", "x", "y"}},
		{name: "bang then two strings", args: []string{"!", "x", "y"}},
		{name: "bang then dash n string", args: []string{"!", "-n", "x"}},
		{name: "bang then dash z string", args: []string{"!", "-z", "x"}},
		{name: "bang then dash z empty", args: []string{"!", "-z", ""}},
		{name: "bang then dash n empty", args: []string{"!", "-n", ""}},
		{name: "bang then equals string", args: []string{"!", "=", "x"}},
		{name: "bang then string dash a", args: []string{"!", "x", "-a"}},
		{name: "bang then dash a string", args: []string{"!", "-a", "x"}},
		{name: "bang then open paren string", args: []string{"!", "(", "x"}},
		{name: "bang then dash x twice", args: []string{"!", "-x", "-x"}},
		{name: "string dash a string", args: []string{"x", "-a", "y"}},
		{name: "string dash o string", args: []string{"x", "-o", "y"}},
		{name: "empty dash a string", args: []string{"", "-a", "y"}},
		{name: "empty dash o string", args: []string{"", "-o", "y"}},
		{name: "empty dash o empty", args: []string{"", "-o", ""}},
		{name: "string dash a close paren", args: []string{"x", "-a", ")"}},
		{name: "string dash a open paren", args: []string{"x", "-a", "("}},
		{name: "dash n string string", args: []string{"-n", "x", "y"}},
		{name: "dash l string string", args: []string{"-l", "a", "b"}},
		{name: "dash l string dash eq", args: []string{"-l", "x", "-eq"}},
		{name: "dash l dash eq number", args: []string{"-l", "-eq", "3"}},
		{name: "one eq dash l", args: []string{"1", "-eq", "-l"}},
		{name: "string then non binop then string", args: []string{"x", "-e", "y"}},
		{name: "string then three equals then string", args: []string{"x", "===", "y"}},
		{name: "string then dash eqx then string", args: []string{"x", "-eqx", "y"}},
		{name: "string then upper case eq then string", args: []string{"x", "-EQ", "y"}},
		{name: "string then upper case nt then string", args: []string{"x", "-Nt", "y"}},
		{name: "string then less than then string", args: []string{"x", "<", "y"}},
		{name: "string then greater than then string", args: []string{"x", ">", "y"}},
		{name: "string then quote then string", args: []string{"x", "it's", "y"}},
		{name: "string then non UTF-8 then string", args: []string{"x", "\xff", "y"}},
		{name: "string then control bytes then string", args: []string{"x", "a\tb\n", "y"}},
		{name: "string then space then string", args: []string{"x", "a b", "y"}},
		{name: "string then backslash then string", args: []string{"x", `a\b`, "y"}},
		{name: "four strings", args: []string{"x", "y", "z", "w"}},
		{name: "five strings", args: []string{"x", "y", "z", "w", "v"}},
		{name: "bang then three strings", args: []string{"!", "x", "y", "z"}},
		{name: "bang then string equals string", args: []string{"!", "x", "=", "y"}},
		{name: "bang then string equals itself", args: []string{"!", "x", "=", "x"}},
		{name: "two bangs then string equals string", args: []string{"!", "!", "x", "=", "y"}},
		{name: "bang then string dash a string", args: []string{"!", "x", "-a", "y"}},
		{name: "bang then string dash o string", args: []string{"!", "x", "-o", "y"}},
		{name: "bang then one eq two", args: []string{"!", "1", "-eq", "2"}},
		{name: "bang then one eq one", args: []string{"!", "1", "-eq", "1"}},
		{name: "bang then dash x three times", args: []string{"!", "-x", "-x", "-x"}},
		{name: "two bangs then dash x twice", args: []string{"!", "!", "-x", "-x"}},
		{name: "bang then parenthesised string", args: []string{"!", "(", "x", ")"}},
		{name: "two bangs then parenthesised string", args: []string{"!", "!", "(", "x", ")"}},
		{name: "bang then empty parens", args: []string{"!", "(", ")"}},
		{name: "bang then string", args: []string{"!", "x"}},
		{name: "bang bang string", args: []string{"!", "!", "x"}},
		{name: "bang bang bang string", args: []string{"!", "!", "!", "x"}},
		{name: "four bangs then string", args: []string{"!", "!", "!", "!", "x"}},
		{name: "five bangs then string", args: []string{"!", "!", "!", "!", "!", "x"}},
		{name: "parenthesised bang string", args: []string{"(", "!", "x", ")"}},
		{name: "parenthesised two bangs string", args: []string{"(", "!", "!", "x", ")"}},
		{name: "parenthesised bang string and string", args: []string{"(", "!", "x", "-a", "y", ")"}},
		{name: "parenthesised dash z string", args: []string{"(", "-z", "x", ")"}},
		{name: "parenthesised comparison", args: []string{"(", "1", "-eq", "1", ")"}},
		{name: "parenthesised string comparison", args: []string{"(", "x", "=", "x", ")"}},
		{name: "parenthesised and", args: []string{"(", "x", "-a", "y", ")"}},
		{name: "parenthesised or of parens", args: []string{"(", "x", "-o", "(", "y", ")", ")"}},
		{name: "parenthesised then string", args: []string{"(", "x", ")", "y"}},
		{name: "parenthesised then dash a", args: []string{"(", "x", ")", "-a"}},
		{name: "parenthesised then close paren", args: []string{"(", "x", ")", ")"}},
		{name: "parenthesised then two close parens", args: []string{"(", "x", ")", ")", ")"}},
		{name: "doubly parenthesised", args: []string{"(", "(", "x", ")", ")"}},
		{name: "doubly parenthesised then close paren", args: []string{"(", "(", "x", ")", ")", ")"}},
		{name: "triply parenthesised", args: []string{"(", "(", "(", "x", ")", ")", ")"}},
		{name: "quadruply parenthesised", args: []string{"(", "(", "(", "(", "x", ")", ")", ")", ")"}},
		{name: "bang then doubly parenthesised", args: []string{"!", "(", "(", "x", ")", ")"}},
		{name: "two parenthesised ands", args: []string{"(", "x", "=", "x", ")", "-a", "(", "y", "=", "y", ")"}},
		{name: "two parenthesised ors", args: []string{"(", "x", "=", "y", ")", "-o", "(", "y", "=", "y", ")"}},
		{name: "or binds looser than and", args: []string{"(", "x", "=", "y", ")", "-o", "(", "y", "=", "y", ")", "-a", "(", "z", "=", "w", ")"}},
		{name: "unclosed paren with and", args: []string{"(", "x", "-a", "y"}},
		{name: "unclosed paren with three strings", args: []string{"(", "x", "y", "z"}},
		{name: "unclosed paren with a trailing string", args: []string{"(", "1", "-eq", "1", "x"}},
		{name: "paren with five tokens", args: []string{"(", "a", "b", "c", "d", "e", ")"}},
		{name: "unclosed paren with five tokens", args: []string{"(", "a", "b", "c", "d", "e"}},
		{name: "paren with four tokens", args: []string{"(", "a", "b", "c", "d", ")"}},
		{name: "paren with four tokens that parse", args: []string{"(", "x", "-a", "y", "-a", ")"}},
		{name: "paren with an and of three", args: []string{"(", "x", "-a", "y", "-a", "z", ")"}},
		{name: "paren closed by a non UTF-8 token", args: []string{"(", "1", "-eq", "1", "\xff"}},
		{name: "paren closed by an empty token", args: []string{"(", "1", "-eq", "1", ""}},
		{name: "paren closed by a quote", args: []string{"(", "1", "-eq", "1", "'"}},
		{name: "dash n then parenthesised", args: []string{"-n", "(", "x", ")"}},
		{name: "empty and empty parens", args: []string{"", "-a", "(", ")"}},
		{name: "empty and unclosed paren", args: []string{"", "-a", "(", "x"}},
		{name: "string or unclosed paren", args: []string{"x", "-o", "(", "x"}},
		{name: "and chain", args: []string{"x", "-a", "y", "-a", "z"}},
		{name: "and chain with an empty", args: []string{"x", "-a", "", "-a", "z"}},
		{name: "or chain", args: []string{"", "-o", "", "-o", "z"}},
		{name: "and then or", args: []string{"x", "-a", "y", "-o", "z"}},
		{name: "empty and then or", args: []string{"", "-a", "y", "-o", "z"}},
		{name: "empty or then and empty", args: []string{"", "-o", "y", "-a", ""}},
		{name: "or and or", args: []string{"", "-o", "y", "-a", "", "-o", "z"}},
		{name: "and then or of comparisons", args: []string{"a", "=", "a", "-a", "b", "=", "b", "-o", "c"}},
		{name: "and directly followed by or", args: []string{"x", "-a", "-o", "y"}},
		{name: "or directly followed by and", args: []string{"x", "-o", "-a", "y"}},
		{name: "and evaluates its right side", args: []string{"", "-a", "1", "-eq", "a"}},
		{name: "or evaluates its right side", args: []string{"x", "-o", "1", "-eq", "a"}},
		{name: "and with nothing after", args: []string{"x", "=", "y", "-a"}},
		{name: "or with nothing after", args: []string{"x", "=", "y", "-o"}},
		{name: "and then string", args: []string{"x", "=", "y", "-a", "z"}},
		{name: "and then dash z with nothing after", args: []string{"x", "=", "y", "-a", "-z"}},
		{name: "and chain with nothing after", args: []string{"x", "=", "y", "-a", "x", "-a"}},
		{name: "dash n dash a dash n", args: []string{"-n", "-a", "-n"}},
		{name: "comparison then string", args: []string{"1", "-eq", "1", "x"}},
		{name: "comparison then and", args: []string{"1", "-eq", "1", "-a"}},
		{name: "two comparisons anded", args: []string{"1", "-eq", "1", "-a", "2", "-eq", "2"}},
		{name: "chained numeric comparison", args: []string{"1", "-eq", "1", "-eq", "1"}},
		{name: "numeric then string comparison", args: []string{"1", "-eq", "1", "=", "1"}},
		{name: "chained string comparison", args: []string{"x", "=", "x", "=", "x"}},
		{name: "five equals", args: []string{"=", "=", "=", "=", "="}},
		{name: "chained empty comparison", args: []string{"", "=", "", "=", ""}},
		{name: "chained nt", args: []string{"x", "-nt", "y", "-nt", "z"}},
		{name: "extra non UTF-8 argument", args: []string{"1", "-eq", "1", "\xff"}},
		{name: "extra empty argument", args: []string{"1", "-eq", "1", ""}},
		{name: "extra quoted argument", args: []string{"1", "-eq", "1", "it's"}},

		// Strings.
		{name: "equal strings", args: []string{"x", "=", "x"}},
		{name: "unequal strings", args: []string{"x", "=", "y"}},
		{name: "double equals", args: []string{"x", "==", "x"}},
		{name: "double equals unequal", args: []string{"x", "==", "y"}},
		{name: "not equal", args: []string{"x", "!=", "y"}},
		{name: "not equal to itself", args: []string{"x", "!=", "x"}},
		{name: "empty equals empty", args: []string{"", "=", ""}},
		{name: "empty not equal empty", args: []string{"", "!=", ""}},
		{name: "empty equals string", args: []string{"", "=", "x"}},
		{name: "string equals empty", args: []string{"x", "=", ""}},
		{name: "case matters", args: []string{"a", "=", "A"}},
		{name: "prefix is not equal", args: []string{"abc", "=", "ab"}},
		{name: "non UTF-8 equals itself", args: []string{"\xff", "=", "\xff"}},
		{name: "non UTF-8 pair", args: []string{"\xff\xfe", "=", "\xff\xfd"}},
		{name: "space is a string", args: []string{" ", "=", " "}},
		{name: "space is not empty", args: []string{" "}},
		{name: "dash z of a string", args: []string{"-z", "x"}},
		{name: "dash z of empty", args: []string{"-z", ""}},
		{name: "dash z of a space", args: []string{"-z", " "}},
		{name: "dash n of a string", args: []string{"-n", "x"}},
		{name: "dash n of empty", args: []string{"-n", ""}},
		{name: "dash n of dash n", args: []string{"-n", "-n"}},
		{name: "dash n of non UTF-8", args: []string{"-n", "\xff"}},
		{name: "equals compares operators as strings", args: []string{"-eq", "=", "-eq"}},
		{name: "equals compares parens as strings", args: []string{"(", "=", ")"}},
		{name: "equals compares bang as a string", args: []string{"!", "=", "!"}},
		{name: "bang then equals bang", args: []string{"!", "!", "=", "!"}},
		{name: "dashdash equals dashdash", args: []string{"--", "=", "--"}},
		{name: "help equals help", args: []string{"--help", "=", "--help"}},
		{name: "help not equal version", args: []string{"--help", "!=", "--version"}},

		// Integers: any length, a sign, blanks either side.
		{name: "eq", args: []string{"1", "-eq", "1"}},
		{name: "eq unequal", args: []string{"1", "-eq", "2"}},
		{name: "ne", args: []string{"1", "-ne", "2"}},
		{name: "ne equal", args: []string{"1", "-ne", "1"}},
		{name: "lt", args: []string{"1", "-lt", "2"}},
		{name: "lt equal", args: []string{"1", "-lt", "1"}},
		{name: "lt greater", args: []string{"2", "-lt", "1"}},
		{name: "le less", args: []string{"1", "-le", "2"}},
		{name: "le equal", args: []string{"1", "-le", "1"}},
		{name: "le greater", args: []string{"1", "-le", "0"}},
		{name: "gt", args: []string{"2", "-gt", "1"}},
		{name: "gt equal", args: []string{"1", "-gt", "1"}},
		{name: "gt less", args: []string{"1", "-gt", "2"}},
		{name: "ge greater", args: []string{"1", "-ge", "0"}},
		{name: "ge equal", args: []string{"1", "-ge", "1"}},
		{name: "ge less", args: []string{"1", "-ge", "2"}},
		{name: "ten gt nine", args: []string{"10", "-gt", "9"}},
		{name: "nine gt ten", args: []string{"9", "-gt", "10"}},
		{name: "leading zero equality", args: []string{"1", "-eq", "01"}},
		{name: "leading zeros gt", args: []string{"010", "-gt", "9"}},
		{name: "leading zeros eq", args: []string{"010", "-eq", "10"}},
		{name: "many leading zeros", args: []string{"00000000000000000000000000000000000001", "-eq", "1"}},
		{name: "explicit plus", args: []string{"1", "-eq", "+1"}},
		{name: "plus zero eq minus zero", args: []string{"+0", "-eq", "-0"}},
		{name: "minus zero eq zero", args: []string{"-0", "-eq", "0"}},
		{name: "minus zero lt zero", args: []string{"-0", "-lt", "0"}},
		{name: "minus zero gt zero", args: []string{"-0", "-gt", "0"}},
		{name: "two zeros eq zero", args: []string{"00", "-eq", "0"}},
		{name: "minus two zeros eq three zeros", args: []string{"-00", "-eq", "000"}},
		{name: "negative eq", args: []string{"-1", "-eq", "-1"}},
		{name: "negative ne positive", args: []string{"1", "-eq", "-1"}},
		{name: "negative leading zeros eq", args: []string{"-010", "-eq", "-10"}},
		{name: "negative leading zeros lt", args: []string{"-010", "-lt", "-9"}},
		{name: "negative ordering", args: []string{"-1", "-lt", "-2"}},
		{name: "negative ordering the other way", args: []string{"-2", "-lt", "-1"}},
		{name: "negative lt zero", args: []string{"-1", "-lt", "0"}},
		{name: "negative lt positive", args: []string{"-1", "-lt", "1"}},
		{name: "zero lt negative", args: []string{"0", "-lt", "-1"}},
		{name: "positive lt negative", args: []string{"1", "-lt", "-1"}},
		{name: "leading blank", args: []string{"1", "-eq", " 1"}},
		{name: "trailing blank", args: []string{"1", "-eq", "1 "}},
		{name: "blanks both sides", args: []string{"  1  ", "-eq", "1"}},
		{name: "blanks around a negative", args: []string{" -1 ", "-eq", "-1"}},
		{name: "blanks around a plus", args: []string{" +1 ", "-eq", "1"}},
		{name: "blank then plus", args: []string{" +1", "-eq", "1"}},
		{name: "tab before the digits", args: []string{"\t1", "-eq", "1"}},
		{name: "newline before the digits", args: []string{"\n1", "-eq", "1"}},
		{name: "tab after the digits", args: []string{"1\t", "-eq", "1"}},
		{name: "vertical tab before the digits", args: []string{"\v1", "-eq", "1"}},
		{name: "form feed before the digits", args: []string{"\f1", "-eq", "1"}},
		{name: "carriage return before the digits", args: []string{"\r1", "-eq", "1"}},
		{name: "every blank around the digits", args: []string{" \t\n\v\f\r1 \t\n\v\f\r", "-eq", "1"}},
		{name: "past i64 max", args: []string{"9223372036854775808", "-gt", "9223372036854775807"}},
		{name: "i64 max eq itself", args: []string{"9223372036854775807", "-eq", "9223372036854775807"}},
		{name: "past i64 min", args: []string{"-9223372036854775809", "-lt", "-9223372036854775808"}},
		{name: "past u64 max", args: []string{"18446744073709551616", "-gt", "18446744073709551615"}},
		{name: "huge eq itself", args: []string{"99999999999999999999999", "-eq", "99999999999999999999999"}},
		{name: "huge gt one", args: []string{"99999999999999999999999", "-gt", "1"}},
		{name: "huge negative lt one", args: []string{"-99999999999999999999999", "-lt", "1"}},
		{name: "huge gt by length", args: []string{"100000000000000000000", "-gt", "99999999999999999999"}},
		{name: "huge negatives by length", args: []string{"-100000000000000000000", "-lt", "-99999999999999999999"}},
		{name: "huge with leading zeros", args: []string{"000099999999999999999999999", "-eq", "99999999999999999999999"}},
		{name: "huge lt by digit", args: []string{"99999999999999999999998", "-lt", "99999999999999999999999"}},
		{name: "letter is not an integer", args: []string{"a", "-eq", "1"}},
		{name: "letter on the right", args: []string{"1", "-eq", "a"}},
		{name: "letters both sides name the left", args: []string{"a", "-eq", "b"}},
		{name: "decimal is not an integer", args: []string{"1.0", "-eq", "1"}},
		{name: "hex is not an integer", args: []string{"0x1", "-eq", "1"}},
		{name: "exponent is not an integer", args: []string{"1e1", "-eq", "10"}},
		{name: "double minus", args: []string{"1", "-eq", "--1"}},
		{name: "plus minus", args: []string{"1", "-eq", "+-1"}},
		{name: "minus plus", args: []string{"-+1", "-eq", "1"}},
		{name: "empty is not an integer", args: []string{"1", "-eq", ""}},
		{name: "empty on the left", args: []string{"", "-eq", "1"}},
		{name: "blank is not an integer", args: []string{" ", "-eq", "1"}},
		{name: "plus alone", args: []string{"+", "-eq", "1"}},
		{name: "minus alone", args: []string{"-", "-eq", "1"}},
		{name: "plus alone on the right", args: []string{"1", "-eq", "+"}},
		{name: "blank inside", args: []string{"+ 1", "-eq", "1"}},
		{name: "blank after the minus", args: []string{"- 1", "-eq", "-1"}},
		{name: "digits then letters", args: []string{"1x", "-eq", "1"}},
		{name: "letters then digits", args: []string{"x1", "-eq", "1"}},
		{name: "non UTF-8 is not an integer", args: []string{"\xff", "-eq", "1"}},
		{name: "quote is not an integer", args: []string{"it's", "-eq", "1"}},
		{name: "control bytes are not an integer", args: []string{"1\a\b", "-eq", "1"}},
		{name: "operator is not an integer", args: []string{"-eq", "-eq", "-eq"}},
		{name: "invalid integer under and", args: []string{"x", "-a", "1", "-eq", "a"}},
		{name: "invalid integer under bang", args: []string{"!", "1", "-eq", "a"}},

		// -l STRING as an integer operand.
		{name: "length eq", args: []string{"-l", "abc", "-eq", "3"}},
		{name: "length eq wrong", args: []string{"-l", "abc", "-eq", "4"}},
		{name: "length ne", args: []string{"-l", "abc", "-ne", "4"}},
		{name: "length gt then and", args: []string{"-l", "abc", "-gt", "2", "-a", "x"}},
		{name: "length eq length", args: []string{"-l", "abc", "-eq", "-l", "xyz"}},
		{name: "length eq shorter length", args: []string{"-l", "abc", "-eq", "-l", "xy"}},
		{name: "length eq length then and", args: []string{"-l", "ab", "-eq", "-l", "xyz", "-a", "x"}},
		{name: "number eq length", args: []string{"3", "-eq", "-l", "abc"}},
		{name: "number eq wrong length", args: []string{"4", "-eq", "-l", "abc"}},
		{name: "number eq length then extra", args: []string{"1", "-eq", "-l", "a", "b"}},
		{name: "length of empty", args: []string{"-l", "", "-eq", "0"}},
		{name: "length of non UTF-8", args: []string{"-l", "\xff\xfe", "-eq", "2"}},
		{name: "length of dash l", args: []string{"-l", "-l", "-eq", "2"}},
		{name: "length of open paren", args: []string{"-l", "(", "-eq", "1"}},
		{name: "length with a bad right side", args: []string{"-l", "x", "-eq", "y"}},
		{name: "length equals string", args: []string{"-l", "abc", "=", "3"}},
		{name: "length equals length", args: []string{"-l", "abc", "=", "-l", "abc"}},
		{name: "string equals length", args: []string{"abc", "=", "-l", "abc"}},
		{name: "equals equals length", args: []string{"=", "=", "-l", "="}},
		{name: "length not equal string", args: []string{"-l", "abc", "!=", "3"}},
		{name: "bang then length eq", args: []string{"!", "-l", "abc", "-eq", "3"}},
		{name: "bang then length eq wrong", args: []string{"!", "-l", "abc", "-eq", "4"}},
		{name: "parenthesised length eq", args: []string{"(", "-l", "abc", "-eq", "3", ")"}},
		{name: "string and length eq", args: []string{"x", "-a", "-l", "abc", "-eq", "3"}},
		{name: "length equals length then and", args: []string{"-l", "x", "=", "-l", "y", "-a", "z"}},
		{name: "length nt", args: []string{"-l", "a", "-nt", "b"}},
		{name: "nt length", args: []string{"a", "-nt", "-l", "b"}},
		{name: "length ot", args: []string{"-l", "a", "-ot", "b"}},
		{name: "ot length", args: []string{"a", "-ot", "-l", "b"}},
		{name: "length ef", args: []string{"-l", "a", "-ef", "b"}},
		{name: "ef length", args: []string{"a", "-ef", "-l", "b"}},
		{name: "length of a bad token", args: []string{"-l", "a", "b", "c"}},

		// -t FD: pipes on 0-2, a terminal on 3 when the case asks.
		{name: "t zero", args: []string{"-t", "0"}},
		{name: "t one", args: []string{"-t", "1"}},
		{name: "t two", args: []string{"-t", "2"}},
		{name: "t three closed", args: []string{"-t", "3"}},
		{name: "t three is a terminal", args: []string{"-t", "3"}, tty: true},
		{name: "t three with blanks is a terminal", args: []string{"-t", " 3 "}, tty: true},
		{name: "t plus three is a terminal", args: []string{"-t", "+3"}, tty: true},
		{name: "t zero padded three is a terminal", args: []string{"-t", "0003"}, tty: true},
		{name: "t three under bang", args: []string{"!", "-t", "3"}, tty: true},
		{name: "t three and t zero", args: []string{"-t", "3", "-a", "-t", "0"}, tty: true},
		{name: "t three or t zero", args: []string{"-t", "3", "-o", "-t", "0"}, tty: true},
		{name: "t one with a terminal on three", args: []string{"-t", "1"}, tty: true},
		{name: "t letter", args: []string{"-t", "x"}},
		{name: "t empty", args: []string{"-t", ""}},
		{name: "t blank", args: []string{"-t", " "}},
		{name: "t plus", args: []string{"-t", "+"}},
		{name: "t minus", args: []string{"-t", "-"}},
		{name: "t hex", args: []string{"-t", "0x1"}},
		{name: "t decimal", args: []string{"-t", "1.0"}},
		{name: "t digits then letter", args: []string{"-t", "1x"}},
		{name: "t negative", args: []string{"-t", "-1"}},
		{name: "t minus zero", args: []string{"-t", "-0"}},
		{name: "t int max", args: []string{"-t", "2147483647"}},
		{name: "t past int max", args: []string{"-t", "2147483648"}},
		{name: "t i64 max", args: []string{"-t", "9223372036854775807"}},
		{name: "t past i64 max", args: []string{"-t", "9223372036854775808"}},
		{name: "t past i64 min", args: []string{"-t", "-9223372036854775809"}},
		{name: "t huge", args: []string{"-t", "99999999999999999999"}},
		{name: "t huge with a terminal on three", args: []string{"-t", "99999999999999999999"}, tty: true},
		{name: "t zero padded one", args: []string{"-t", "00000000000000000000000000000000000001"}},
		{name: "t zero or string", args: []string{"-t", "0", "-o", "x"}},

		// File predicates over the fixture tree.
		{name: "e file", args: []string{"-e", f.file}},
		{name: "e missing", args: []string{"-e", f.missing}},
		{name: "e empty path", args: []string{"-e", ""}},
		{name: "e dot", args: []string{"-e", "."}},
		{name: "e root", args: []string{"-e", "/"}},
		{name: "e dir", args: []string{"-e", f.subdir}},
		{name: "e dir with a slash", args: []string{"-e", f.subdir + "/"}},
		{name: "e file with a slash", args: []string{"-e", f.file + "/"}},
		{name: "e under a file", args: []string{"-e", f.file + "/x"}},
		{name: "e fifo", args: []string{"-e", f.fifo}},
		{name: "e socket", args: []string{"-e", f.sock}},
		{name: "e link", args: []string{"-e", f.link}},
		{name: "e dangling link", args: []string{"-e", f.dangling}},
		{name: "e dev null", args: []string{"-e", "/dev/null"}},
		{name: "e noperm", args: []string{"-e", f.noperm}},
		{name: "f file", args: []string{"-f", f.file}},
		{name: "f empty file", args: []string{"-f", f.empty}},
		{name: "f dir", args: []string{"-f", f.subdir}},
		{name: "f dot", args: []string{"-f", "."}},
		{name: "f missing", args: []string{"-f", f.missing}},
		{name: "f empty path", args: []string{"-f", ""}},
		{name: "f fifo", args: []string{"-f", f.fifo}},
		{name: "f socket", args: []string{"-f", f.sock}},
		{name: "f link to file", args: []string{"-f", f.link}},
		{name: "f link to dir", args: []string{"-f", f.dirlink}},
		{name: "f dangling link", args: []string{"-f", f.dangling}},
		{name: "f dev null", args: []string{"-f", "/dev/null"}},
		{name: "f block device", args: []string{"-f", f.blk}},
		{name: "d dir", args: []string{"-d", f.subdir}},
		{name: "d dir with a slash", args: []string{"-d", f.subdir + "/"}},
		{name: "d file", args: []string{"-d", f.file}},
		{name: "d dot", args: []string{"-d", "."}},
		{name: "d dotdot", args: []string{"-d", ".."}},
		{name: "d root", args: []string{"-d", "/"}},
		{name: "d missing", args: []string{"-d", f.missing}},
		{name: "d empty path", args: []string{"-d", ""}},
		{name: "d link to dir", args: []string{"-d", f.dirlink}},
		{name: "d link to file", args: []string{"-d", f.link}},
		{name: "d under a file", args: []string{"-d", f.file + "/x"}},
		{name: "d dev null", args: []string{"-d", "/dev/null"}},
		{name: "s file", args: []string{"-s", f.file}},
		{name: "s empty file", args: []string{"-s", f.empty}},
		{name: "s dir", args: []string{"-s", f.subdir}},
		{name: "s missing", args: []string{"-s", f.missing}},
		{name: "s fifo", args: []string{"-s", f.fifo}},
		{name: "s socket", args: []string{"-s", f.sock}},
		{name: "s link", args: []string{"-s", f.link}},
		{name: "s dangling link", args: []string{"-s", f.dangling}},
		{name: "s dev null", args: []string{"-s", "/dev/null"}},
		{name: "p fifo", args: []string{"-p", f.fifo}},
		{name: "p file", args: []string{"-p", f.file}},
		{name: "p socket", args: []string{"-p", f.sock}},
		{name: "p dir", args: []string{"-p", f.subdir}},
		{name: "p missing", args: []string{"-p", f.missing}},
		{name: "p dev null", args: []string{"-p", "/dev/null"}},
		{name: "S socket", args: []string{"-S", f.sock}},
		{name: "S fifo", args: []string{"-S", f.fifo}},
		{name: "S file", args: []string{"-S", f.file}},
		{name: "S dir", args: []string{"-S", f.subdir}},
		{name: "S missing", args: []string{"-S", f.missing}},
		{name: "S dev null", args: []string{"-S", "/dev/null"}},
		{name: "c dev null", args: []string{"-c", "/dev/null"}},
		{name: "c dev zero", args: []string{"-c", "/dev/zero"}},
		{name: "c block device", args: []string{"-c", f.blk}},
		{name: "c file", args: []string{"-c", f.file}},
		{name: "c fifo", args: []string{"-c", f.fifo}},
		{name: "c dir", args: []string{"-c", f.subdir}},
		{name: "c missing", args: []string{"-c", f.missing}},
		{name: "b block device", args: []string{"-b", f.blk}},
		{name: "b dev null", args: []string{"-b", "/dev/null"}},
		{name: "b file", args: []string{"-b", f.file}},
		{name: "b dir", args: []string{"-b", f.subdir}},
		{name: "b missing", args: []string{"-b", f.missing}},
		{name: "h link", args: []string{"-h", f.link}},
		{name: "h link to dir", args: []string{"-h", f.dirlink}},
		{name: "h dangling link", args: []string{"-h", f.dangling}},
		{name: "h file", args: []string{"-h", f.file}},
		{name: "h dir", args: []string{"-h", f.subdir}},
		{name: "h link with a slash", args: []string{"-h", f.link + "/"}},
		{name: "h dir link with a slash", args: []string{"-h", f.dirlink + "/"}},
		{name: "h missing", args: []string{"-h", f.missing}},
		{name: "h empty path", args: []string{"-h", ""}},
		{name: "h dev null", args: []string{"-h", "/dev/null"}},
		{name: "L link", args: []string{"-L", f.link}},
		{name: "L link to dir", args: []string{"-L", f.dirlink}},
		{name: "L dangling link", args: []string{"-L", f.dangling}},
		{name: "L file", args: []string{"-L", f.file}},
		{name: "L dir", args: []string{"-L", f.subdir}},
		{name: "L missing", args: []string{"-L", f.missing}},
		{name: "u setuid", args: []string{"-u", f.setuid}},
		{name: "u setgid", args: []string{"-u", f.setgid}},
		{name: "u sticky", args: []string{"-u", f.sticky}},
		{name: "u plain", args: []string{"-u", f.file}},
		{name: "u dir", args: []string{"-u", f.subdir}},
		{name: "u missing", args: []string{"-u", f.missing}},
		{name: "g setgid", args: []string{"-g", f.setgid}},
		{name: "g setuid", args: []string{"-g", f.setuid}},
		{name: "g sticky", args: []string{"-g", f.sticky}},
		{name: "g plain", args: []string{"-g", f.file}},
		{name: "g dir", args: []string{"-g", f.subdir}},
		{name: "g missing", args: []string{"-g", f.missing}},
		{name: "k sticky file", args: []string{"-k", f.sticky}},
		{name: "k sticky dir", args: []string{"-k", f.stickydir}},
		{name: "k setuid", args: []string{"-k", f.setuid}},
		{name: "k setgid", args: []string{"-k", f.setgid}},
		{name: "k plain", args: []string{"-k", f.file}},
		{name: "k dir", args: []string{"-k", f.subdir}},
		{name: "k missing", args: []string{"-k", f.missing}},
		{name: "r file", args: []string{"-r", f.file}},
		{name: "r noperm", args: []string{"-r", f.noperm}},
		{name: "r dir", args: []string{"-r", f.subdir}},
		{name: "r missing", args: []string{"-r", f.missing}},
		{name: "r empty path", args: []string{"-r", ""}},
		{name: "r link", args: []string{"-r", f.link}},
		{name: "r dangling link", args: []string{"-r", f.dangling}},
		{name: "r dev null", args: []string{"-r", "/dev/null"}},
		{name: "r fifo", args: []string{"-r", f.fifo}},
		{name: "w file", args: []string{"-w", f.file}},
		{name: "w noperm", args: []string{"-w", f.noperm}},
		{name: "w dir", args: []string{"-w", f.subdir}},
		{name: "w missing", args: []string{"-w", f.missing}},
		{name: "w dev null", args: []string{"-w", "/dev/null"}},
		{name: "w root", args: []string{"-w", "/"}},
		{name: "x exec", args: []string{"-x", f.exec}},
		{name: "x setuid", args: []string{"-x", f.setuid}},
		{name: "x plain file", args: []string{"-x", f.file}},
		{name: "x noperm", args: []string{"-x", f.noperm}},
		{name: "x dir", args: []string{"-x", f.subdir}},
		{name: "x missing", args: []string{"-x", f.missing}},
		{name: "x dev null", args: []string{"-x", "/dev/null"}},
		{name: "x root", args: []string{"-x", "/"}},
		{name: "x link to exec dir", args: []string{"-x", f.dirlink}},
		{name: "O file", args: []string{"-O", f.file}},
		{name: "O dir", args: []string{"-O", f.subdir}},
		{name: "O root", args: []string{"-O", "/"}},
		{name: "O dev null", args: []string{"-O", "/dev/null"}},
		{name: "O missing", args: []string{"-O", f.missing}},
		{name: "O link", args: []string{"-O", f.link}},
		{name: "G file", args: []string{"-G", f.file}},
		{name: "G dir", args: []string{"-G", f.subdir}},
		{name: "G root", args: []string{"-G", "/"}},
		{name: "G dev null", args: []string{"-G", "/dev/null"}},
		{name: "G missing", args: []string{"-G", f.missing}},
		{name: "N unread", args: []string{"-N", f.unread}},
		{name: "N read", args: []string{"-N", f.read}},
		{name: "N missing", args: []string{"-N", f.missing}},
		{name: "N dev null", args: []string{"-N", "/dev/null"}},
		{name: "N same times", args: []string{"-N", f.older}},
		{name: "predicate on a path with a space", args: []string{"-e", f.file + " x"}},
		{name: "predicate on a non UTF-8 path", args: []string{"-e", f.dir + "/\xff"}},
		{name: "predicate under bang", args: []string{"!", "-e", f.missing}},
		{name: "two predicates anded", args: []string{"-f", f.file, "-a", "-d", f.subdir}},
		{name: "two predicates anded false", args: []string{"-f", f.file, "-a", "-d", f.file}},
		{name: "two predicates ored", args: []string{"-f", f.subdir, "-o", "-d", f.subdir}},
		{name: "parenthesised predicate", args: []string{"(", "-f", f.file, ")"}},
		{name: "predicate and comparison", args: []string{"-e", f.file, "-a", "1", "-eq", "1"}},
		{name: "predicate with nothing after", args: []string{"x", "-a", "-e"}},

		// File pairs.
		{name: "nt newer older", args: []string{f.newer, "-nt", f.older}},
		{name: "nt older newer", args: []string{f.older, "-nt", f.newer}},
		{name: "nt same file", args: []string{f.older, "-nt", f.older}},
		{name: "nt by nanoseconds", args: []string{f.sameB, "-nt", f.sameA}},
		{name: "nt by nanoseconds the other way", args: []string{f.sameA, "-nt", f.sameB}},
		{name: "nt missing right", args: []string{f.older, "-nt", f.missing}},
		{name: "nt missing left", args: []string{f.missing, "-nt", f.older}},
		{name: "nt both missing", args: []string{f.missing, "-nt", f.missing}},
		{name: "nt empty right", args: []string{f.older, "-nt", ""}},
		{name: "nt empty left", args: []string{"", "-nt", f.older}},
		{name: "nt through a link", args: []string{f.link, "-nt", f.older}},
		{name: "nt dangling left", args: []string{f.dangling, "-nt", f.older}},
		{name: "ot older newer", args: []string{f.older, "-ot", f.newer}},
		{name: "ot newer older", args: []string{f.newer, "-ot", f.older}},
		{name: "ot same file", args: []string{f.older, "-ot", f.older}},
		{name: "ot by nanoseconds", args: []string{f.sameA, "-ot", f.sameB}},
		{name: "ot by nanoseconds the other way", args: []string{f.sameB, "-ot", f.sameA}},
		{name: "ot missing right", args: []string{f.older, "-ot", f.missing}},
		{name: "ot missing left", args: []string{f.missing, "-ot", f.older}},
		{name: "ot both missing", args: []string{f.missing, "-ot", f.missing}},
		{name: "ot empty left", args: []string{"", "-ot", f.older}},
		{name: "ot empty right", args: []string{f.older, "-ot", ""}},
		{name: "ef same path", args: []string{f.file, "-ef", f.file}},
		{name: "ef hard link", args: []string{f.file, "-ef", f.hardlink}},
		{name: "ef symlink", args: []string{f.file, "-ef", f.link}},
		{name: "ef dir link", args: []string{f.subdir, "-ef", f.dirlink}},
		{name: "ef different files", args: []string{f.file, "-ef", f.empty}},
		{name: "ef missing right", args: []string{f.file, "-ef", f.missing}},
		{name: "ef missing left", args: []string{f.missing, "-ef", f.file}},
		{name: "ef both missing", args: []string{f.missing, "-ef", f.missing}},
		{name: "ef both empty", args: []string{"", "-ef", ""}},
		{name: "ef dangling", args: []string{f.dangling, "-ef", f.dangling}},
		{name: "ef dot and dir", args: []string{".", "-ef", "."}},
		{name: "ef root", args: []string{"/", "-ef", "/"}},
		{name: "ef root and dev null", args: []string{"/", "-ef", "/dev/null"}},
		{name: "nt with nothing after", args: []string{"x", "-nt"}},
		{name: "ef with nothing after", args: []string{"x", "-ef"}},
		{name: "nt with nothing before", args: []string{"-nt", "x"}},
		{name: "pair under bang", args: []string{"!", f.newer, "-nt", f.older}},
		{name: "pair anded", args: []string{f.newer, "-nt", f.older, "-a", f.file, "-ef", f.hardlink}},

		// --help and --version are strings to test(1), whatever their
		// position; only `[` without a `]` honours them.
		{name: "help alone is a string", args: []string{"--help"}},
		{name: "version alone is a string", args: []string{"--version"}},
		{name: "abbreviated help is a string", args: []string{"--hel"}},
		{name: "help then string", args: []string{"--help", "x"}},
		{name: "help twice", args: []string{"--help", "--help"}},
		{name: "version twice", args: []string{"--version", "--version"}},
		{name: "dashdash then help", args: []string{"--", "--help"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "upper case help", args: []string{"--HELP"}},
		{name: "short h", args: []string{"-h"}},
		{name: "unknown long option is a string", args: []string{"--foo"}},
		{name: "dash n of help", args: []string{"-n", "--help"}},
		{name: "dash z of help", args: []string{"-z", "--help"}},
		{name: "help under posix", args: []string{"--help"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "expression under posix", args: []string{"x", "-a", "-o", "y"}, env: []string{"POSIXLY_CORRECT=1"}},

		// Nothing is written to stdout, so a closed one changes nothing.
		{name: "true with stdout closed", args: []string{"x"}, stdout: stdoutClosed},
		{name: "false with stdout closed", args: []string{""}, stdout: stdoutClosed},
		{name: "error with stdout closed", args: []string{"x", "y"}, stdout: stdoutClosed},
		{name: "true with stdout full", args: []string{"x"}, stdout: stdoutFull},
	}
}

// testCases is test(1)'s corpus: the shared one, with the cases that
// distinguish it from `[` — `test` reads `]` as a string.
func testCases(t *testing.T) []invocation {
	t.Helper()
	return append(condCases(t),
		invocation{name: "close bracket is a string", args: []string{"]"}},
		invocation{name: "string then close bracket", args: []string{"x", "]"}},
		invocation{name: "comparison then close bracket", args: []string{"x", "=", "x", "]"}},
		invocation{name: "two close brackets", args: []string{"]", "]"}},
		invocation{name: "open bracket is a string", args: []string{"["}},
		invocation{name: "brackets around a string", args: []string{"[", "x", "]"}},
	)
}

func TestTestParity(t *testing.T) {
	requireParity(t, "test", testCases(t))
}
