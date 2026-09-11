package coreutils

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// chmod(1)'s whole output is usually silence, so most of what these cases
// prove is the TREE each side leaves behind: `seedTree` gives every case its
// own working directory and compares the twelve-bit mode of every entry
// under it, which is exactly what this utility is for.
//
// Three things the fixtures are shaped around:
//
//   - A DIRECTORY keeps its setuid and setgid through a `=` that does not
//     name them, so `chmod 755 sd` on 6777 is 6755 where `chmod 755 s` on a
//     file of the same mode is 0755. `sd` and `td` are here to catch an
//     implementation that treats the two alike.
//   - `X` reads the execute bits as they stand AT ITS CLAUSE, so `u+x,+X`
//     and `+X` disagree on the same file.
//   - The umask only reaches a clause with no `ugoa` prefix, and only for
//     the value it sets or clears. TestMain pins the process mask at 022
//     (umask_unix_test.go) and the harness has no way to hand a child a
//     different one, so every case here is at 022 — enough to separate `u`
//     from `g` and `o` for `w`, and enough to reach the complaint a `-`
//     clause raises when the mask stops it clearing what it named, but NOT
//     a second mask value. Seven others were checked against the reference
//     by hand while this was written.
//
// What is NOT here, and why: every case runs as uid 0 in this container, so
// the EACCES legs — a directory chmod(2) refuses, a directory whose children
// cannot be listed — are unreachable from a corpus, because root bypasses
// the check that produces them. `/proc/self/fd` stands in for the first:
// it is EPERM even for root. The second (`cannot read directory`) was
// checked by hand under `setpriv --reuid=65534` and is not in the corpus.

func chmodChmod(t *testing.T, path string, mode uint32) {
	t.Helper()
	if err := syscall.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func chmodFile(t *testing.T, dir, name string, mode uint32) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(name+"\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	chmodChmod(t, p, mode)
}

func chmodDir(t *testing.T, dir, name string, mode uint32) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	chmodChmod(t, p, mode)
}

func chmodLink(t *testing.T, dir, target, name string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		t.Fatalf("symlink %s: %v", name, err)
	}
}

// chmodFlat is the workhorse: one file of each interesting starting mode and
// one directory of each, so a single mode argument exercises the file rule
// and the directory rule side by side.
//
//	f  0644   the ordinary file
//	x  0777   everything set, so a `-` clause has something to fail at
//	z  0000   nothing set, so a `+` clause has somewhere to go
//	s  6755   a FILE with both special bits — they are cleared by a `=`
//	d  0755   the ordinary directory
//	sd 6777   a DIRECTORY with both special bits — they survive a `=`
//	td 7777   the same plus sticky, which does NOT survive
func chmodFlat(t *testing.T, dir string) {
	t.Helper()
	chmodFile(t, dir, "f", 0o644)
	chmodFile(t, dir, "x", 0o777)
	chmodFile(t, dir, "z", 0o000)
	chmodFile(t, dir, "s", 0o6755)
	chmodDir(t, dir, "d", 0o755)
	chmodDir(t, dir, "sd", 0o6777)
	chmodDir(t, dir, "td", 0o7777)
}

// chmodOne is a single file, for the diagnostics where anything else is
// noise.
func chmodOne(t *testing.T, dir string) {
	t.Helper()
	chmodFile(t, dir, "f", 0o644)
}

// chmodTree is the `-R` fixture: a directory with a file, a subdirectory
// with its own file, a symlink to a sibling and a dangling one. The walk is
// pre-order in raw readdir order, so `-v` prints the parent, then whatever
// the kernel hands back, descending into a directory as it is reached.
func chmodTree(t *testing.T, dir string) {
	t.Helper()
	chmodDir(t, dir, "t", 0o755)
	chmodFile(t, dir, "t/a", 0o644)
	chmodDir(t, dir, "t/sub", 0o755)
	chmodFile(t, dir, "t/sub/b", 0o644)
	chmodLink(t, dir, "a", "t/link")
	chmodLink(t, dir, "nowhere", "t/dangle")
	chmodFile(t, dir, "ref", 0o750)
}

// chmodDeep is a tree whose second level has its own directory, so the walk
// has to come back UP to the entries it had not reached yet.
func chmodDeep(t *testing.T, dir string) {
	t.Helper()
	chmodDir(t, dir, "t", 0o755)
	for _, n := range []string{"t/m", "t/n", "t/o", "t/p"} {
		chmodFile(t, dir, n, 0o644)
	}
	chmodDir(t, dir, "t/zdir", 0o700)
	chmodFile(t, dir, "t/zdir/inner", 0o600)
	chmodDir(t, dir, "t/zdir/deeper", 0o755)
	chmodFile(t, dir, "t/zdir/deeper/leaf", 0o444)
}

// chmodLinks is every way a symlink can refuse to resolve: a target that is
// not there, one whose parent is a file, a pair pointing at each other, and
// one pointing at itself. A command-line symlink is FOLLOWED, so each of
// these is an error rather than a silent skip, and each has its own wording.
func chmodLinks(t *testing.T, dir string) {
	t.Helper()
	chmodFile(t, dir, "f", 0o644)
	chmodLink(t, dir, "f", "good")
	chmodLink(t, dir, "nowhere", "gone")
	chmodLink(t, dir, "f/x", "notdir")
	chmodLink(t, dir, "loopb", "loopa")
	chmodLink(t, dir, "loopa", "loopb")
	chmodLink(t, dir, "self", "self")
	chmodDir(t, dir, "d", 0o755)
	chmodFile(t, dir, "d/inner", 0o600)
	chmodLink(t, dir, "d", "dlink")
}

// chmodRefs are the `--reference` sources: an ordinary mode, one with both
// special bits, and a symlink to the first, which --reference dereferences.
func chmodRefs(t *testing.T, dir string) {
	t.Helper()
	chmodFile(t, dir, "f", 0o644)
	chmodFile(t, dir, "r640", 0o640)
	chmodFile(t, dir, "r6755", 0o6755)
	chmodLink(t, dir, "r640", "rlink")
	chmodLink(t, dir, "nowhere", "rgone")
	chmodDir(t, dir, "d", 0o755)
	chmodDir(t, dir, "sd", 0o6777)
}

// chmodOdd is the names the diagnostics have to quote: one needing single
// quotes, one whose only trouble is the apostrophe (which goes in double
// quotes), one with a control character, and one that is not valid UTF-8 —
// a file name is bytes, and the quoting has to render it as octal escapes
// rather than refuse it.
func chmodOdd(t *testing.T, dir string) {
	t.Helper()
	chmodFile(t, dir, "a b", 0o777)
	chmodFile(t, dir, "a'b", 0o777)
	chmodFile(t, dir, "tab\there", 0o777)
	chmodFile(t, dir, "-lead", 0o777)
	chmodFile(t, dir, "raw\xff\xfe", 0o777)
}

// chmodKinds is the two non-regular shapes a corpus can build: a fifo, which
// carries permission bits like anything else, and a hard-link pair, whose
// mode follows the INODE rather than either name.
func chmodKinds(t *testing.T, dir string) {
	t.Helper()
	chmodFile(t, dir, "f", 0o644)
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	if err := os.Link(filepath.Join(dir, "f"), filepath.Join(dir, "hard")); err != nil {
		t.Fatalf("link: %v", err)
	}
}

func init() {
	registerCorpus("chmod", chmodCases)
}

// chmodCases is chmod(1)'s corpus.
func chmodCases(t *testing.T) []invocation {
	return []invocation{
		// ---- octal modes -------------------------------------------------
		{name: "four digits", args: []string{"-v", "0755", "f"}, seedTree: chmodFlat},
		{name: "three digits", args: []string{"-v", "755", "f"}, seedTree: chmodFlat},
		{name: "one digit", args: []string{"-v", "7", "f"}, seedTree: chmodFlat},
		{name: "zero", args: []string{"-v", "0", "f"}, seedTree: chmodFlat},
		{name: "two zeros", args: []string{"-v", "00", "f"}, seedTree: chmodFlat},
		{name: "the widest value", args: []string{"-v", "07777", "f"}, seedTree: chmodFlat},
		{name: "setuid", args: []string{"-v", "4755", "f"}, seedTree: chmodFlat},
		{name: "setgid", args: []string{"-v", "2755", "f"}, seedTree: chmodFlat},
		{name: "sticky", args: []string{"-v", "1755", "f"}, seedTree: chmodFlat},
		{name: "the three special bits alone", args: []string{"-v", "7000", "f"}, seedTree: chmodFlat},
		{name: "a file loses its special bits", args: []string{"-v", "755", "s"}, seedTree: chmodFlat},
		// Fewer than five digits leaves a directory's setuid and setgid
		// alone; five or more is explicit and clears them.
		{name: "a directory keeps them", args: []string{"-v", "755", "sd"}, seedTree: chmodFlat},
		{name: "four digits keep them too", args: []string{"-v", "0755", "sd"}, seedTree: chmodFlat},
		{name: "five digits clear them", args: []string{"-v", "00755", "sd"}, seedTree: chmodFlat},
		{name: "seven digits clear them", args: []string{"-v", "0000755", "sd"}, seedTree: chmodFlat},
		{name: "sticky is not kept", args: []string{"-v", "0755", "td"}, seedTree: chmodFlat},
		{name: "a named setuid, an unnamed setgid", args: []string{"-v", "4755", "sd"}, seedTree: chmodFlat},
		{name: "a named setgid, an unnamed setuid", args: []string{"-v", "2755", "sd"}, seedTree: chmodFlat},
		{name: "zero keeps both", args: []string{"-v", "0000", "sd"}, seedTree: chmodFlat},
		{name: "five zeros keep neither", args: []string{"-v", "00000", "sd"}, seedTree: chmodFlat},
		{name: "an operator makes the octal explicit", args: []string{"-v", "=755", "td"}, seedTree: chmodFlat},
		{name: "an added octal", args: []string{"-v", "+4000", "z"}, seedTree: chmodFlat},
		{name: "a removed octal", args: []string{"-v", "-0777", "x"}, seedTree: chmodFlat},
		{name: "a removed octal on a directory", args: []string{"-v", "-4000", "sd"}, seedTree: chmodFlat},
		{name: "an octal is not masked", args: []string{"-v", "+7", "z"}, seedTree: chmodFlat},

		// ---- octal modes that are not modes ------------------------------
		{name: "past the twelve bits", args: []string{"-v", "010000", "f"}, seedTree: chmodFlat},
		{name: "five sevens", args: []string{"-v", "77777", "f"}, seedTree: chmodFlat},
		{name: "far past any integer", args: []string{"-v", "7777777777777777777777", "f"}, seedTree: chmodFlat},
		{name: "an eight", args: []string{"-v", "8", "f"}, seedTree: chmodFlat},
		{name: "a digit and a letter", args: []string{"-v", "08", "f"}, seedTree: chmodFlat},
		{name: "digits then a letter", args: []string{"-v", "0644x", "f"}, seedTree: chmodFlat},
		{name: "a letter then digits", args: []string{"-v", "x0644", "f"}, seedTree: chmodFlat},
		{name: "an octal with a who", args: []string{"-v", "u+1", "f"}, seedTree: chmodFlat},
		{name: "leading blank", args: []string{"-v", " 644", "f"}, seedTree: chmodFlat},
		{name: "trailing blank", args: []string{"-v", "644 ", "f"}, seedTree: chmodFlat},

		// ---- the symbolic grammar ----------------------------------------
		{name: "u plus x", args: []string{"-v", "u+x", "f"}, seedTree: chmodFlat},
		{name: "an omitted who is masked", args: []string{"-v", "+w", "z"}, seedTree: chmodFlat},
		{name: "a is not masked", args: []string{"-v", "a+w", "z"}, seedTree: chmodFlat},
		{name: "equals with no perms clears", args: []string{"-v", "=", "x"}, seedTree: chmodFlat},
		{name: "equals on a directory keeps the special bits", args: []string{"-v", "=", "sd"}, seedTree: chmodFlat},
		{name: "plus with no perms", args: []string{"-v", "+", "f"}, seedTree: chmodFlat},
		{name: "minus with no perms", args: []string{"-v", "-", "f"}, seedTree: chmodFlat},
		{name: "u minus w", args: []string{"-v", "u-w", "x"}, seedTree: chmodFlat},
		{name: "a minus w", args: []string{"-v", "a-w", "x"}, seedTree: chmodFlat},
		{name: "ug equals rw", args: []string{"-v", "ug=rw", "f"}, seedTree: chmodFlat},
		{name: "a repeated who", args: []string{"-v", "uu+x", "f"}, seedTree: chmodFlat},
		{name: "a and o together", args: []string{"-v", "ao+x", "f"}, seedTree: chmodFlat},
		{name: "ugo is a", args: []string{"-v", "ugo+w", "z"}, seedTree: chmodFlat},
		{name: "everything at once", args: []string{"-v", "a+rwx", "z"}, seedTree: chmodFlat},
		{name: "equals rwx is masked", args: []string{"-v", "=rwx", "z"}, seedTree: chmodFlat},
		{name: "a equals rwx is not", args: []string{"-v", "a=rwx", "z"}, seedTree: chmodFlat},
		{name: "g equals clears the group", args: []string{"-v", "g=", "f"}, seedTree: chmodFlat},
		{name: "two clauses", args: []string{"-v", "o=,g=", "f"}, seedTree: chmodFlat},
		{name: "three clauses", args: []string{"-v", "u+rwx,g=u,o=g", "f"}, seedTree: chmodFlat},
		{name: "two operators in one clause", args: []string{"-v", "u+rwx-w", "z"}, seedTree: chmodFlat},
		{name: "four operators in one clause", args: []string{"-v", "a+rwx-w-r", "z"}, seedTree: chmodFlat},
		{name: "the who spans the operators", args: []string{"-v", "ug+r-w", "z"}, seedTree: chmodFlat},

		// ---- the special bits and who they ride on -----------------------
		{name: "u plus s is setuid", args: []string{"-v", "u+s", "f"}, seedTree: chmodFlat},
		{name: "g plus s is setgid", args: []string{"-v", "g+s", "f"}, seedTree: chmodFlat},
		{name: "o plus s is nothing", args: []string{"-v", "o+s", "z"}, seedTree: chmodFlat},
		{name: "a bare plus s is both", args: []string{"-v", "+s", "z"}, seedTree: chmodFlat},
		{name: "a plus s is both", args: []string{"-v", "a+s", "z"}, seedTree: chmodFlat},
		{name: "o plus t is sticky", args: []string{"-v", "o+t", "z"}, seedTree: chmodFlat},
		{name: "u plus t is nothing", args: []string{"-v", "u+t", "z"}, seedTree: chmodFlat},
		{name: "g plus t is nothing", args: []string{"-v", "g+t", "z"}, seedTree: chmodFlat},
		{name: "a bare plus t is sticky", args: []string{"-v", "+t", "z"}, seedTree: chmodFlat},
		{name: "ug plus t is nothing", args: []string{"-v", "ug+t", "z"}, seedTree: chmodFlat},
		{name: "u equals s keeps the group's", args: []string{"-v", "u=s", "s"}, seedTree: chmodFlat},
		{name: "u equals clears only u's", args: []string{"-v", "u=", "s"}, seedTree: chmodFlat},
		{name: "u minus s", args: []string{"-v", "u-s", "s"}, seedTree: chmodFlat},
		{name: "g minus s", args: []string{"-v", "g-s", "s"}, seedTree: chmodFlat},
		{name: "o minus s is nothing", args: []string{"-v", "o-s", "s"}, seedTree: chmodFlat},
		{name: "a bare minus s is both", args: []string{"-v", "-s", "s"}, seedTree: chmodFlat},
		{name: "minus t on a directory", args: []string{"-v", "-t", "td"}, seedTree: chmodFlat},
		{name: "s survives a directory's equals", args: []string{"-v", "a=rwx", "sd"}, seedTree: chmodFlat},
		{name: "unless the clause names it", args: []string{"-v", "a=rwx,g-s", "td"}, seedTree: chmodFlat},
		{name: "a named s on a directory", args: []string{"-v", "a=s", "sd"}, seedTree: chmodFlat},
		{name: "t does not name s", args: []string{"-v", "a=t", "sd"}, seedTree: chmodFlat},
		{name: "the running mode is what survives", args: []string{"-v", "g-s,a=rwx", "td"}, seedTree: chmodFlat},

		// ---- the copy form -----------------------------------------------
		{name: "o equals u", args: []string{"-v", "o=u", "f"}, seedTree: chmodFlat},
		{name: "g equals u", args: []string{"-v", "g=u", "f"}, seedTree: chmodFlat},
		{name: "u equals g", args: []string{"-v", "u=g", "f"}, seedTree: chmodFlat},
		{name: "a equals u", args: []string{"-v", "a=u", "f"}, seedTree: chmodFlat},
		{name: "an omitted who copies masked", args: []string{"-v", "=u", "f"}, seedTree: chmodFlat},
		{name: "plus u", args: []string{"-v", "+u", "f"}, seedTree: chmodFlat},
		{name: "minus u", args: []string{"-v", "-u", "x"}, seedTree: chmodFlat},
		{name: "a copy drops the source's special bit", args: []string{"-v", "a=u", "s"}, seedTree: chmodFlat},
		{name: "a copy on a directory", args: []string{"-v", "o=u", "sd"}, seedTree: chmodFlat},
		{name: "a copy letter followed by an operator", args: []string{"-v", "-u=rwx", "f"}, seedTree: chmodFlat},
		{name: "a copy letter followed by a comma", args: []string{"-v", "g=u,o=u", "f"}, seedTree: chmodFlat},

		// ---- X, whose condition moves ------------------------------------
		{name: "X on a file with no execute", args: []string{"-v", "+X", "f"}, seedTree: chmodFlat},
		{name: "X after an execute bit arrives", args: []string{"-v", "u+x,+X", "f"}, seedTree: chmodFlat},
		{name: "X on a directory", args: []string{"-v", "+X", "d"}, seedTree: chmodFlat},
		{name: "X on a directory with nothing set", args: []string{"-v", "a=,+X", "d"}, seedTree: chmodFlat},
		{name: "X with a who", args: []string{"-v", "ug+X", "f"}, seedTree: chmodFlat},
		{name: "X beside r", args: []string{"-v", "+rX", "f"}, seedTree: chmodFlat},
		{name: "X in an equals reads the mode before it", args: []string{"-v", "=X", "x"}, seedTree: chmodFlat},
		{name: "X in an equals on a directory", args: []string{"-v", "a=X", "sd"}, seedTree: chmodFlat},
		{name: "X removes what is there", args: []string{"-v", "a-X", "x"}, seedTree: chmodFlat},
		{name: "X removes nothing when nothing is", args: []string{"-v", "-X", "f"}, seedTree: chmodFlat},
		{name: "any execute bit satisfies X", args: []string{"-v", "a=,g+x,+X", "f"}, seedTree: chmodFlat},

		// ---- symbolic modes that are not modes ---------------------------
		{name: "an empty mode", args: []string{"-v", "", "f"}, seedTree: chmodFlat},
		{name: "a lone comma", args: []string{"-v", ",", "f"}, seedTree: chmodFlat},
		{name: "two commas", args: []string{"-v", ",,", "f"}, seedTree: chmodFlat},
		{name: "a who with no operator", args: []string{"-v", "u", "f"}, seedTree: chmodFlat},
		{name: "two whos with no operator", args: []string{"-v", "ug", "f"}, seedTree: chmodFlat},
		{name: "a alone", args: []string{"-v", "a", "f"}, seedTree: chmodFlat},
		{name: "a trailing comma", args: []string{"-v", "u+r,", "f"}, seedTree: chmodFlat},
		{name: "a leading comma", args: []string{"-v", ",u+r", "f"}, seedTree: chmodFlat},
		{name: "an empty clause between two", args: []string{"-v", "u+r,,g+r", "f"}, seedTree: chmodFlat},
		{name: "a letter that is nothing", args: []string{"-v", "q", "f"}, seedTree: chmodFlat},
		{name: "a perm that is nothing", args: []string{"-v", "u+q", "f"}, seedTree: chmodFlat},
		{name: "a copy letter with a perm after it", args: []string{"-v", "+ux", "f"}, seedTree: chmodFlat},
		{name: "a perm with a copy letter after it", args: []string{"-v", "+xu", "f"}, seedTree: chmodFlat},
		{name: "a who after a perm", args: []string{"-v", "ux+r", "f"}, seedTree: chmodFlat},
		{name: "two copy letters", args: []string{"-v", "--", "-uu", "f"}, seedTree: chmodFlat},

		// ---- -v and -c wording -------------------------------------------
		{name: "verbose on a change", args: []string{"-v", "0700", "f"}, seedTree: chmodFlat},
		{name: "verbose on no change", args: []string{"-v", "0644", "f"}, seedTree: chmodFlat},
		{name: "changes on a change", args: []string{"-c", "0700", "f"}, seedTree: chmodFlat},
		{name: "changes on no change", args: []string{"-c", "0644", "f"}, seedTree: chmodFlat},
		{name: "silence on a change", args: []string{"0700", "f"}, seedTree: chmodFlat},
		{name: "verbose renders setuid with x", args: []string{"-v", "4755", "f"}, seedTree: chmodFlat},
		{name: "verbose renders setuid without x", args: []string{"-v", "4644", "f"}, seedTree: chmodFlat},
		{name: "verbose renders setgid without x", args: []string{"-v", "2644", "f"}, seedTree: chmodFlat},
		{name: "verbose renders sticky without x", args: []string{"-v", "1644", "f"}, seedTree: chmodFlat},
		{name: "verbose renders all three unset", args: []string{"-v", "7000", "f"}, seedTree: chmodFlat},
		{name: "verbose renders all three set", args: []string{"-v", "7777", "f"}, seedTree: chmodFlat},
		{name: "the same file three times", args: []string{"-v", "0644", "f", "f", "f"}, seedTree: chmodFlat},
		{name: "verbose and changes together", args: []string{"-vc", "0700", "f"}, seedTree: chmodFlat},
		{name: "changes then verbose", args: []string{"-cv", "0644", "f"}, seedTree: chmodFlat},

		// ---- the umask complaint -----------------------------------------
		// Raised only for a mode that came in through the option letters:
		// `chmod -w x` says so and `chmod -- -w x` does not.
		{name: "a minus the umask blocks", args: []string{"-w", "x"}, seedTree: chmodFlat},
		{name: "the same with verbose", args: []string{"-v", "-w", "x"}, seedTree: chmodFlat},
		{name: "the same with changes", args: []string{"-c", "-w", "x"}, seedTree: chmodFlat},
		{name: "-f does not silence it", args: []string{"-f", "-w", "x"}, seedTree: chmodFlat},
		{name: "the same mode as an operand is silent", args: []string{"--", "-w", "x"}, seedTree: chmodFlat},
		{name: "a whole clause blocked", args: []string{"-rwx", "x"}, seedTree: chmodFlat},
		{name: "a copy clause blocked", args: []string{"-u", "x"}, seedTree: chmodFlat},
		{name: "two elements join with a comma", args: []string{"-r", "-w", "x"}, seedTree: chmodFlat},
		{name: "an inert clause does not excuse it", args: []string{"-r,+,-w", "x"}, seedTree: chmodFlat},
		{name: "nor a who'd one", args: []string{"-x,u+r,-w", "x"}, seedTree: chmodFlat},
		{name: "a later equals can settle it", args: []string{"-w,=rwx", "x"}, seedTree: chmodFlat},
		{name: "a later plus cannot", args: []string{"-w,u+w", "x"}, seedTree: chmodFlat},
		{name: "a who'd minus is not complained about", args: []string{"--", "u-w,-w", "x"}, seedTree: chmodFlat},
		{name: "nothing to block, nothing to say", args: []string{"-w", "f"}, seedTree: chmodFlat},
		{name: "on a directory", args: []string{"-w", "sd"}, seedTree: chmodFlat},
		{name: "under -R", args: []string{"-R", "-w", "t"}, seedTree: chmodTree},

		// ---- errors ------------------------------------------------------
		{name: "a missing file", args: []string{"0644", "nosuch"}, seedTree: chmodFlat},
		{name: "a missing file with verbose", args: []string{"-v", "0644", "nosuch"}, seedTree: chmodFlat},
		{name: "a missing file with changes", args: []string{"-c", "0644", "nosuch"}, seedTree: chmodFlat},
		{name: "a missing file under -f", args: []string{"-f", "0644", "nosuch"}, seedTree: chmodFlat},
		{name: "a missing file under --silent", args: []string{"--silent", "0644", "nosuch"}, seedTree: chmodFlat},
		{name: "a missing file under --quiet", args: []string{"--quiet", "0644", "nosuch"}, seedTree: chmodFlat},
		{name: "-f keeps the verbose line", args: []string{"-fv", "0644", "nosuch"}, seedTree: chmodFlat},
		{name: "an empty operand", args: []string{"0644", ""}, seedTree: chmodFlat},
		{name: "a failure does not stop the rest", args: []string{"-v", "0700", "nosuch", "f"}, seedTree: chmodFlat},
		{name: "a failure after the rest", args: []string{"-v", "0700", "f", "nosuch"}, seedTree: chmodFlat},
		{name: "a component that is a file", args: []string{"-v", "0644", "f/x"}, seedTree: chmodFlat},
		{name: "a file with a trailing slash", args: []string{"-v", "0644", "f/"}, seedTree: chmodFlat},
		{name: "a directory with a trailing slash", args: []string{"-v", "0644", "d/"}, seedTree: chmodFlat},
		{name: "a run of trailing slashes", args: []string{"-v", "0700", "d///"}, seedTree: chmodFlat},
		{name: "a dot operand", args: []string{"-v", "0755", "d/."}, seedTree: chmodFlat},
		{name: "a dotdot operand", args: []string{"-v", "0755", "d/.."}, seedTree: chmodFlat},
		// EPERM even for root, which is the only way this container can
		// reach the failing-chmod path at all.
		{name: "a chmod the kernel refuses", args: []string{"0700", "/proc/self/fd"}},
		{name: "the same with verbose", args: []string{"-v", "0700", "/proc/self/fd"}},
		{name: "the same with changes", args: []string{"-c", "0700", "/proc/self/fd"}},
		{name: "the same under -f", args: []string{"-f", "0700", "/proc/self/fd"}},
		{name: "-f keeps its verbose line", args: []string{"-fv", "0700", "/proc/self/fd"}},
		{name: "a refused no-op still fails", args: []string{"-v", "0500", "/proc/self/fd"}},

		// ---- symlinks ----------------------------------------------------
		{name: "a symlink operand is followed", args: []string{"-v", "0600", "good"}, seedTree: chmodLinks},
		{name: "a dangling symlink operand", args: []string{"-v", "0600", "gone"}, seedTree: chmodLinks},
		{name: "a dangling symlink under -f", args: []string{"-fv", "0600", "gone"}, seedTree: chmodLinks},
		{name: "a symlink whose parent is a file", args: []string{"-v", "0600", "notdir"}, seedTree: chmodLinks},
		{name: "a symlink loop", args: []string{"-v", "0600", "loopa"}, seedTree: chmodLinks},
		{name: "a symlink to itself", args: []string{"-v", "0600", "self"}, seedTree: chmodLinks},
		{name: "a symlink to a directory", args: []string{"-v", "0700", "dlink"}, seedTree: chmodLinks},
		{name: "-R follows a symlink operand", args: []string{"-Rv", "0700", "dlink"}, seedTree: chmodLinks},
		{name: "-R does not follow one below", args: []string{"-Rv", "0700", "."}, seedTree: chmodLinks},
		{name: "-R on a symlink to a file", args: []string{"-Rv", "0600", "good"}, seedTree: chmodLinks},
		{name: "every symlink at once", args: []string{"-v", "0600", "good", "gone", "notdir", "loopa", "self"}, seedTree: chmodLinks},

		// ---- recursion ---------------------------------------------------
		{name: "a tree", args: []string{"-Rv", "0700", "t"}, seedTree: chmodTree},
		{name: "a tree with --recursive", args: []string{"--recursive", "-v", "0700", "t"}, seedTree: chmodTree},
		{name: "a tree with -c", args: []string{"-Rc", "0700", "t"}, seedTree: chmodTree},
		{name: "a tree in silence", args: []string{"-R", "0700", "t"}, seedTree: chmodTree},
		{name: "the option order does not matter", args: []string{"-vR", "0700", "t"}, seedTree: chmodTree},
		{name: "without -R only the directory moves", args: []string{"-v", "0700", "t"}, seedTree: chmodTree},
		{name: "a tree with a trailing slash", args: []string{"-Rv", "0700", "t/"}, seedTree: chmodTree},
		{name: "a tree with a run of them", args: []string{"-Rv", "0700", "t///"}, seedTree: chmodTree},
		{name: "a tree through a dot", args: []string{"-Rv", "0700", "t/."}, seedTree: chmodTree},
		{name: "a subtree", args: []string{"-Rv", "0700", "t/sub"}, seedTree: chmodTree},
		{name: "-R on a plain file", args: []string{"-Rv", "0700", "t/a"}, seedTree: chmodTree},
		{name: "a deeper tree comes back up", args: []string{"-Rv", "0700", "t"}, seedTree: chmodDeep},
		{name: "a symbolic mode over a tree", args: []string{"-Rv", "+X", "t"}, seedTree: chmodDeep},
		{name: "a tree to nothing", args: []string{"-Rv", "0000", "t"}, seedTree: chmodDeep},
		{name: "a tree and a missing operand", args: []string{"-Rv", "0700", "t", "nosuch"}, seedTree: chmodTree},
		{name: "two trees", args: []string{"-Rv", "0700", "t/sub", "t"}, seedTree: chmodTree},
		{name: "the whole working directory", args: []string{"-Rv", "0700", "."}, seedTree: chmodTree},

		// ---- the root failsafe -------------------------------------------
		// Only the REFUSING spellings are here: chmod's default is
		// --no-preserve-root, so the same case without it would really
		// change every mode on the machine.
		{name: "the root directory", args: []string{"-R", "--preserve-root", "0777", "/"}, seedTree: chmodOne},
		{name: "two slashes say so", args: []string{"-R", "--preserve-root", "0777", "//"}, seedTree: chmodOne},
		{name: "three collapse to one", args: []string{"-R", "--preserve-root", "0777", "///"}, seedTree: chmodOne},
		{name: "the root as dot", args: []string{"-R", "--preserve-root", "0777", "/."}, seedTree: chmodOne},
		{name: "the root through a child", args: []string{"-R", "--preserve-root", "0777", "/tmp/.."}, seedTree: chmodOne},
		{name: "the failsafe is not quieted by -f", args: []string{"-Rf", "--preserve-root", "0777", "/"}, seedTree: chmodOne},
		{name: "nor by -v", args: []string{"-Rv", "--preserve-root", "0777", "/"}, seedTree: chmodOne},
		{name: "a later --preserve-root puts it back", args: []string{"-R", "--no-preserve-root", "--preserve-root", "0777", "/"}, seedTree: chmodOne},
		{name: "it does not apply without -R", args: []string{"--preserve-root", "-v", "0700", "f"}, seedTree: chmodFlat},
		{name: "nor to an ordinary tree", args: []string{"-Rv", "--preserve-root", "0700", "t"}, seedTree: chmodTree},
		{name: "no-preserve-root over an ordinary tree", args: []string{"-Rv", "--no-preserve-root", "0700", "t"}, seedTree: chmodTree},
		{name: "the last one wins", args: []string{"-Rv", "--preserve-root", "--no-preserve-root", "0755", "t"}, seedTree: chmodTree},
		{name: "and the other way round", args: []string{"-Rv", "--no-preserve-root", "--preserve-root", "0755", "t"}, seedTree: chmodTree},
		{name: "a missing operand under the failsafe", args: []string{"-Rv", "--preserve-root", "0700", "/nonexistentzz"}, seedTree: chmodOne},

		// ---- --reference -------------------------------------------------
		{name: "a reference", args: []string{"--reference=r640", "f"}, seedTree: chmodRefs},
		{name: "a reference with verbose", args: []string{"-v", "--reference=r640", "d"}, seedTree: chmodRefs},
		{name: "a reference with special bits", args: []string{"-v", "--reference=r6755", "f"}, seedTree: chmodRefs},
		{name: "a reference is explicit on a directory", args: []string{"-v", "--reference=r640", "sd"}, seedTree: chmodRefs},
		{name: "a reference is dereferenced", args: []string{"-v", "--reference=rlink", "f"}, seedTree: chmodRefs},
		{name: "a dangling reference", args: []string{"-v", "--reference=rgone", "f"}, seedTree: chmodRefs},
		{name: "a missing reference", args: []string{"--reference=nosuch", "f"}, seedTree: chmodRefs},
		{name: "a missing reference under -f", args: []string{"-f", "--reference=nosuch", "f"}, seedTree: chmodRefs},
		{name: "a reference with a split value", args: []string{"--reference", "r640", "f"}, seedTree: chmodRefs},
		{name: "a reference and no file", args: []string{"--reference=r640"}, seedTree: chmodRefs},
		{name: "a missing reference and no file", args: []string{"--reference=nosuch"}, seedTree: chmodRefs},
		{name: "the first operand is a file", args: []string{"--reference=r640", "0644", "f"}, seedTree: chmodRefs},
		{name: "the last reference wins", args: []string{"-v", "--reference=r640", "--reference=r6755", "f"}, seedTree: chmodRefs},
		{name: "a reference with a mode option", args: []string{"--reference=r640", "-w", "f"}, seedTree: chmodRefs},
		{name: "the mode option first", args: []string{"-w", "--reference=r640", "f"}, seedTree: chmodRefs},
		{name: "a reference, a mode and no file", args: []string{"--reference=r640", "-w"}, seedTree: chmodRefs},
		{name: "a reference over a tree", args: []string{"-Rv", "--reference=ref", "t"}, seedTree: chmodTree},

		// ---- operand counting --------------------------------------------
		{name: "no arguments at all"},
		{name: "only options", args: []string{"-R"}},
		{name: "only a verbose flag", args: []string{"-v"}},
		{name: "a mode and nothing else", args: []string{"0644"}},
		{name: "a mode from options and nothing else", args: []string{"-w"}},
		{name: "a verbose mode from options", args: []string{"-v", "-w"}},
		{name: "a mode that is not one, and nothing else", args: []string{"-R", "f"}},
		{name: "a mode that is not one, with a file", args: []string{"f", "g"}},
		{name: "the mode is quoted as typed", args: []string{"u+r'x"}},
		{name: "a control character in the mode", args: []string{"a\nb"}},

		// ---- the mode as an option ---------------------------------------
		{name: "a bare minus w", args: []string{"--", "-w", "x"}, seedTree: chmodFlat},
		{name: "minus w through the options", args: []string{"-w", "f"}, seedTree: chmodFlat},
		{name: "an octal through the options", args: []string{"-0644", "x"}, seedTree: chmodFlat},
		{name: "a single digit through the options", args: []string{"-1", "x"}, seedTree: chmodFlat},
		{name: "a comma through the options", args: []string{"-,-w", "x"}, seedTree: chmodFlat},
		{name: "an equals through the options", args: []string{"-=", "x"}, seedTree: chmodFlat},
		{name: "a plus through the options", args: []string{"-+", "f"}, seedTree: chmodFlat},
		{name: "a copy through the options", args: []string{"-u=rwx", "f"}, seedTree: chmodFlat},
		{name: "a who cannot come first there", args: []string{"-a+x", "f"}, seedTree: chmodFlat},
		{name: "the first mode byte swallows the cluster", args: []string{"-rw", "x"}, seedTree: chmodFlat},
		{name: "all three of them", args: []string{"-rwx", "x"}, seedTree: chmodFlat},
		{name: "an option byte before a mode byte", args: []string{"-Rw", "f"}, seedTree: chmodFlat},
		{name: "two of them", args: []string{"-vRw", "f"}, seedTree: chmodFlat},
		{name: "a mode byte before an option byte", args: []string{"-wR", "f"}, seedTree: chmodFlat},
		{name: "a mode byte and a stray one", args: []string{"-wq", "f"}, seedTree: chmodFlat},
		{name: "a digit and a stray letter", args: []string{"-5w", "f"}, seedTree: chmodFlat},
		{name: "r swallows the rest", args: []string{"-rq", "f"}, seedTree: chmodFlat},
		{name: "X swallows the rest", args: []string{"-Xq", "f"}, seedTree: chmodFlat},
		{name: "s swallows the rest", args: []string{"-sq", "f"}, seedTree: chmodFlat},
		{name: "t swallows the rest", args: []string{"-tq", "f"}, seedTree: chmodFlat},
		{name: "u swallows the rest", args: []string{"-uq", "f"}, seedTree: chmodFlat},
		{name: "a comma swallows the rest", args: []string{"-,q", "f"}, seedTree: chmodFlat},
		{name: "a plus swallows the rest", args: []string{"-+q", "f"}, seedTree: chmodFlat},
		{name: "an equals swallows the rest", args: []string{"-=q", "f"}, seedTree: chmodFlat},
		{name: "a digit swallows the rest", args: []string{"-7q", "f"}, seedTree: chmodFlat},
		{name: "R does not", args: []string{"-Rq", "f"}, seedTree: chmodFlat},
		{name: "c does not", args: []string{"-cq", "f"}, seedTree: chmodFlat},
		{name: "f does not", args: []string{"-fq", "f"}, seedTree: chmodFlat},
		{name: "v does not", args: []string{"-vq", "f"}, seedTree: chmodFlat},
		{name: "elements join in argv order", args: []string{"-uu", "-w", "f"}, seedTree: chmodFlat},
		{name: "the other order", args: []string{"-w", "-uu", "f"}, seedTree: chmodFlat},
		{name: "three of them", args: []string{"-r", "-w", "-uu", "f"}, seedTree: chmodFlat},
		{name: "one element yields one mode", args: []string{"-ww", "x"}, seedTree: chmodFlat},
		{name: "a mode from options makes the first operand a file", args: []string{"-w", "0644", "f"}, seedTree: chmodFlat},
		{name: "-R and a mode from options", args: []string{"-R", "-w", "t"}, seedTree: chmodTree},
		{name: "a dashdash between them", args: []string{"-R", "--", "-w", "t"}, seedTree: chmodTree},
		{name: "an option after the mode operand", args: []string{"-w", "-R", "t"}, seedTree: chmodTree},

		// ---- getopt ------------------------------------------------------
		{name: "an unknown long option", args: []string{"--bogus", "f"}, seedTree: chmodFlat},
		{name: "an unknown short option", args: []string{"-z", "f"}, seedTree: chmodFlat},
		{name: "an unambiguous prefix", args: []string{"--rec", "0644", "f"}, seedTree: chmodFlat},
		{name: "an ambiguous prefix", args: []string{"--re", "0644", "f"}, seedTree: chmodFlat},
		{name: "the empty prefix lists them all", args: []string{"--=x", "f"}, seedTree: chmodFlat},
		{name: "r is ambiguous", args: []string{"--r", "0644", "f"}, seedTree: chmodFlat},
		{name: "v is ambiguous", args: []string{"--v", "0644", "f"}, seedTree: chmodFlat},
		{name: "ve is ambiguous too", args: []string{"--ve", "0644", "f"}, seedTree: chmodFlat},
		{name: "c is not", args: []string{"--c", "0644", "f"}, seedTree: chmodFlat},
		{name: "p is not", args: []string{"--p", "0644", "f"}, seedTree: chmodFlat},
		{name: "no is not", args: []string{"--no", "0644", "f"}, seedTree: chmodFlat},
		{name: "s is not", args: []string{"--s", "0644", "f"}, seedTree: chmodFlat},
		{name: "q is not", args: []string{"--q", "0644", "f"}, seedTree: chmodFlat},
		{name: "preserve-root takes no value", args: []string{"--preserve-root=x", "0644", "f"}, seedTree: chmodFlat},
		{name: "recursive takes no value", args: []string{"--recursive=x", "0644", "f"}, seedTree: chmodFlat},
		{name: "reference needs one", args: []string{"--reference"}, seedTree: chmodFlat},
		{name: "long spellings of the flags", args: []string{"--changes", "--verbose", "0700", "f"}, seedTree: chmodFlat},
		{name: "operands are permuted out", args: []string{"0700", "f", "-v"}, seedTree: chmodFlat},
		{name: "POSIXLY_CORRECT stops the scan", args: []string{"0700", "f", "-v"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: chmodFlat},
		{name: "an option between operands", args: []string{"0700", "-v", "f"}, seedTree: chmodFlat},
		{name: "a dashdash before the mode", args: []string{"--", "0644", "f"}, seedTree: chmodFlat},
		{name: "a dashdash after it", args: []string{"-v", "0644", "--", "f"}, seedTree: chmodFlat},
		{name: "a lone dash is an operand", args: []string{"-v", "0644", "-"}, seedTree: chmodFlat},

		// ---- quoting -----------------------------------------------------
		{name: "a name with a space", args: []string{"-v", "0644", "a b"}, seedTree: chmodOdd},
		{name: "a name with an apostrophe", args: []string{"-v", "0644", "a'b"}, seedTree: chmodOdd},
		{name: "a name with a tab", args: []string{"-v", "0644", "tab\there"}, seedTree: chmodOdd},
		{name: "a missing name with a space", args: []string{"-v", "0644", "no such"}, seedTree: chmodOdd},
		{name: "a missing name with an apostrophe", args: []string{"-v", "0644", "no'such"}, seedTree: chmodOdd},
		{name: "a name that is not valid UTF-8", args: []string{"-v", "0644", "raw\xff\xfe"}, seedTree: chmodOdd},
		// A file whose name begins with `-` reaches chmod only behind `--`
		// or a `./` prefix; bare, its first byte is read as a mode letter
		// and the second as an option that is not one.
		{name: "a leading-dash name behind a dashdash", args: []string{"-v", "0644", "--", "-lead"}, seedTree: chmodOdd},
		{name: "the same name behind a dot slash", args: []string{"-v", "0644", "./-lead"}, seedTree: chmodOdd},
		{name: "the same name bare", args: []string{"-v", "0644", "-lead"}, seedTree: chmodOdd},
		{name: "and as the only argument", args: []string{"-lead"}, seedTree: chmodOdd},
		{name: "a missing one that is not", args: []string{"-v", "0644", "gone\xff"}, seedTree: chmodOdd},
		{name: "a mode that is not valid UTF-8", args: []string{"-v", "\xff\xfe", "f"}, seedTree: chmodOdd},
		{name: "and one with no operand after it", args: []string{"\xff\xfe"}},
		// The umask complaint is the one diagnostic that names the file the
		// way a shell would need it typed rather than always in quotes.
		{name: "the complaint quotes a space", args: []string{"-w", "a b"}, seedTree: chmodOdd},
		{name: "the complaint quotes an apostrophe", args: []string{"-w", "a'b"}, seedTree: chmodOdd},
		{name: "the complaint leaves a plain name bare", args: []string{"-w", "x"}, seedTree: chmodFlat},

		// ---- the other file kinds ----------------------------------------
		{name: "a fifo", args: []string{"-v", "0600", "pipe"}, seedTree: chmodKinds},
		{name: "a hard link names one inode", args: []string{"-v", "0600", "hard"}, seedTree: chmodKinds},
		{name: "-R over every kind", args: []string{"-Rv", "0640", "."}, seedTree: chmodKinds},

		// ---- the write-failure paths -------------------------------------
		{name: "a silent run to a closed stdout", args: []string{"0700", "f"}, seedTree: chmodFlat, stdout: stdoutClosed},
		{name: "a verbose line to a closed stdout", args: []string{"-v", "0700", "f"}, seedTree: chmodFlat, stdout: stdoutClosed},
		{name: "a verbose line to a full stdout", args: []string{"-v", "0700", "f"}, seedTree: chmodFlat, stdout: stdoutFull},
		{name: "a silent run to a full stdout", args: []string{"0700", "f"}, seedTree: chmodFlat, stdout: stdoutFull},
		{name: "a diagnostic flushes the buffer first", args: []string{"-v", "0700", "f", "nosuch"}, seedTree: chmodFlat, stdout: stdoutFull},
		{name: "the same on a closed stdout", args: []string{"-v", "0700", "f", "nosuch"}, seedTree: chmodFlat, stdout: stdoutClosed},
		{name: "a usage error to a closed stdout", args: []string{"--bogus"}, stdout: stdoutClosed},
		{name: "a missing operand to a closed stdout", stdout: stdoutClosed},
		{name: "an invalid mode to a closed stdout", args: []string{"q", "f"}, seedTree: chmodFlat, stdout: stdoutClosed},
		{name: "a whole tree to a full stdout", args: []string{"-Rv", "0700", "t"}, seedTree: chmodTree, stdout: stdoutFull},
	}
}

func TestChmodParity(t *testing.T) {
	requireParity(t, "chmod", chmodCases(t))
}

func TestChmodHelpVersion(t *testing.T) {
	requireHelp(t, "chmod", []string{"--help"}, 0)
	requireHelp(t, "chmod", []string{"--hel"}, 0)
	requireHelp(t, "chmod", []string{"--h"}, 0)
	requireHelp(t, "chmod", []string{"--help", "0644", "f"}, 0)
	requireHelp(t, "chmod", []string{"0644", "f", "--help"}, 0)
	requireVersion(t, "chmod", []string{"--version"}, 0)
	requireVersion(t, "chmod", []string{"--vers"}, 0)
	requireVersion(t, "chmod", []string{"0644", "f", "--version"}, 0)
}
