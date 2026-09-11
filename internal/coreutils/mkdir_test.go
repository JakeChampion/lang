package coreutils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// mkdir(1) is the first utility here whose answer depends on the process
// CREATION MASK, so most of the corpus below names one. The mode a
// directory ends up with is the umask applied to 0777, `-m` moves the
// base to 0777 and changes which CLAUSES the mask still reaches, and
// `-p` gives the ancestors a third mode again — none of which is visible
// on stdout, so every case that gets as far as creating anything runs
// under `seedTree` and the comparison is of the two directories left
// behind as well as of the two streams.
//
// The mode grammar is the substance and it is bigger than it looks. Four
// of its rules are in no man page and each has cases of its own here:
//
//   - `-m 755` ignores the umask outright while `-m =rwx` is `0777 &
//     ~umask`: the mask reaches a clause that names no who, and nothing
//     else.
//   - A numeric perm (`+7`, `=0`) is a clause form, it is refused after
//     a who, and it ENDS its clause — `=7,u+r` is two clauses and
//     `=7=7` is not a mode.
//   - `=` on a directory keeps the set-user-ID and set-group-ID bits
//     that are already there. `-m +s,=rwx` is 6777, not 0777.
//   - A mode carrying a special bit is created with group and other
//     write held back, and they come back only if the MODE MENTIONED
//     them: `-m 1777` is 1777 and `-m +t` is 1755, though the two name
//     the same value, and `-m +t,g+w` is 1777 again.
//
// The `mkdirGuarded` cases say different things depending on who runs
// the suite, and are worth having either way because both sides meet the
// same answer: a directory with no permission bits is one a non-root
// user cannot enter, which is the `-p` walk's EACCES arm, and one root
// walks straight into. `-Z` and `--context` have no such second reading:
// no machine this corpus runs on has SELinux, so only the
// kernel-has-none path is ever compared, and docs/COREUTILS.md records
// that.

func init() {
	registerCorpus("mkdir", mkdirCases)
}

// mkdirBare is the usual fixture: an empty directory, so everything in
// the tree comparison is something the run created.
func mkdirBare(t *testing.T, dir string) {
	t.Helper()
}

// mkdirTaken holds one of every name mkdir can collide with: a
// directory, a regular file, a dangling symlink, a symlink to a
// directory, and a symlink to itself. The last three are what separate
// the three ways `-p` forgives — or does not forgive — a name that is
// already there.
func mkdirTaken(t *testing.T, dir string) {
	t.Helper()
	mkdirMake(t, dir, "d", 0o700)
	mkdirFile(t, dir, "f")
	mkdirMake(t, dir, "dd", 0o755)
	mkdirLink(t, "dd", dir, "sd")
	mkdirLink(t, "nowhere", dir, "sl")
	mkdirLink(t, "loop", dir, "loop")
}

// mkdirPartial is an ancestor chain that already exists, so `-v` shows
// which components a `-p` run actually created.
func mkdirPartial(t *testing.T, dir string) {
	t.Helper()
	mkdirMake(t, dir, "a", 0o755)
	mkdirMake(t, dir, filepath.Join("a", "b"), 0o755)
}

// mkdirBlocked is a regular file where the walk wants a directory.
func mkdirBlocked(t *testing.T, dir string) {
	t.Helper()
	mkdirFile(t, dir, "f")
	mkdirMake(t, dir, "a", 0o755)
	mkdirFile(t, dir, filepath.Join("a", "b"))
}

// mkdirGuarded is the pair of directories a non-root user is stopped by:
// one it cannot search, which is what the `-p` walk's enter check is
// about, and one it cannot write, where the mkdir itself is refused. A
// suite running as root walks into both, and the two sides still agree.
func mkdirGuarded(t *testing.T, dir string) {
	t.Helper()
	mkdirMake(t, dir, "blk", 0o000)
	mkdirMake(t, dir, "ro", 0o555)
}

// mkdirNames seeds the names whose diagnostics show GNU's two quoting
// styles: the `-v` line quotes a file name as a shell would need it
// typed and the error line escapes it in C.
func mkdirNames(t *testing.T, dir string) {
	t.Helper()
	for _, n := range []string{"a b", "a'b", "a\"b", "a\\b", "a\tb", "a\nb", "\xff\xfe", "a~b", "a:b"} {
		mkdirFile(t, dir, n)
	}
}

func mkdirMake(t *testing.T, dir, name string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.Mkdir(path, mode); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	// Mkdir applies the creation mask, which a case may have changed;
	// Chmod sets exactly the bits asked for, so the fixture is the same
	// on both sides whatever mask the case runs under.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", name, err)
	}
}

func mkdirFile(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod %s: %v", name, err)
	}
}

func mkdirLink(t *testing.T, target, dir, name string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		t.Fatalf("symlink %s: %v", name, err)
	}
}

// The masks the corpus runs modes under. 000 and 777 are the two ends —
// under 777 the DEFAULT mode is 0 and a directory the run made cannot
// be entered again — 022 is the usual one, 002 and 027 differ from it in
// the group bits alone, and 111 and 222 take away a class of bit rather
// than a class of user, which is what tells `~umask` apart from a
// per-triple rule.
var mkdirMasks = []int{0o000, 0o022, 0o077, 0o002, 0o027, 0o111, 0o222, 0o777}

// The MODE operands whose result depends on the mask, run under every
// mask above. Each line is a rule: the first four are octal (which the
// mask never touches), then the who-less clauses it does, then the
// copy, numeric and X forms.
var mkdirMaskedModes = []string{
	"755", "777", "0", "7777",
	"=rwx", "+w", "-w", "=", "+", "-",
	"u+rwx,go-w", "a=rx", "a+rwX",
	"+t", "+s", "-s", "-t", "=t", "=s",
	"g+s", "u+s", "1777", "1755", "2755", "4755",
	"+t,+w", "+t,g+w", "+t,u+w", "+t,+X", "+t,+u", "+t,g=u", "+t,u=g",
	"+u", "-u", "=u", "u=g", "g=u", "o=u",
	"=7", "-7", "+7", "+10", "=1", "=0",
	"+X", "=X", "-X", "+rwxst", "o=t", "o+t", "u+t",
	"+s,=rwx", "+s,a=", "u+s,o=s", "+t,-w", "a=rwx,+t", "u+rwxXst",
}

// The MODE operands GNU refuses. The mask cannot change a refusal, so
// these run once.
var mkdirBadModes = []string{
	"", "8", "9", "a", "u", "rwx", "z+r", "ax+r", "+a", "a=a", "u=A",
	"010000", "17777", "777777777", "0x755", " 755", "755 ", "+ ",
	"u+rwx,", ",u+rwx", "u+r,,g+w", "a+r,u", "o+w,", "0755,", ",0755", "755,755",
	"u=7", "u+4", "a=0", "u=07", "u=10", "u=8", "=7=7", "=7-t", "u+7,g-2",
	"u=go", "u+ug", "+uu", "u=gr", "u=rg", "u=r7", "u=7r", "-m",
}

func mkdirCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

	// ---- the arities and the two standard options ----
	add(invocation{name: "no operands"})
	add(invocation{name: "dashdash alone", args: []string{"--"}})
	add(invocation{name: "parents with no operands", args: []string{"-p"}})
	add(invocation{name: "verbose with no operands", args: []string{"-v"}})
	add(invocation{name: "Z with no operands", args: []string{"-Z"}})
	add(invocation{name: "context with no operands", args: []string{"--context"}})
	add(invocation{name: "mode with no operands", args: []string{"-m", "755", "--"}})
	// The MODE is validated only once there is something to create, so a
	// mode that is not one loses to the missing operand.
	add(invocation{name: "missing operand outranks a bad mode", args: []string{"-m", "bogus"}})
	add(invocation{name: "help with a value", args: []string{"--help=x"}})
	add(invocation{name: "version with a value", args: []string{"--version=1"}})

	// ---- getopt faults ----
	add(invocation{name: "invalid short option", args: []string{"-x"}})
	add(invocation{name: "invalid byte in a cluster", args: []string{"-Zq", "d"}})
	add(invocation{name: "unrecognized long option", args: []string{"--foo"}})
	add(invocation{name: "an option after an operand permutes", args: []string{"a", "-x"}})
	// POSIXLY_CORRECT stops the scan at the first operand, so `-v` is a
	// third directory to make rather than an option.
	add(invocation{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"a", "-v", "b"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: mkdirBare})
	// The ambiguity list is in declaration order, and `--context` leads it.
	add(invocation{name: "the empty long option", args: []string{"--=x"}})
	add(invocation{name: "the empty long option with an operand", args: []string{"--=x", "d"}})
	add(invocation{name: "ambiguous between verbose and version", args: []string{"--v"}})
	add(invocation{name: "still ambiguous three letters in", args: []string{"--ver"}})
	add(invocation{name: "parents takes no argument", args: []string{"--parents=x", "d"}})
	add(invocation{name: "verbose takes no argument", args: []string{"--verbose=x", "d"}})
	add(invocation{name: "mode requires an argument", args: []string{"-m"}})
	// `--mode d` eats the operand as its value, so what is left is nothing.
	add(invocation{name: "the long mode eats the operand", args: []string{"--mode", "d"}})
	add(invocation{name: "an empty long mode value", args: []string{"--mode=", "d"}})
	add(invocation{name: "mode by one-letter prefix", args: []string{"--m=755", "d"}, seedTree: mkdirBare})
	add(invocation{name: "context by one-letter prefix", args: []string{"--c", "d"}, seedTree: mkdirBare})
	add(invocation{name: "getopt runs before the mode is read", args: []string{"-m", "bogus", "-x", "d"}})
	add(invocation{name: "a bad mode makes nothing", args: []string{"-m", "bogus", "d", "e"}, seedTree: mkdirBare})

	// ---- the mode grammar, against the mask ----
	for _, m := range mkdirMaskedModes {
		for _, mask := range mkdirMasks {
			add(invocation{
				name:     fmt.Sprintf("mode %s under %03o", m, mask),
				args:     []string{"-m", m, "d"},
				seedTree: mkdirBare,
				umask:    withMask(mask),
			})
		}
	}
	// `-p` does not change the final component's mode and does not give
	// the ancestors any of it: their mode is the default with u+wx
	// forced on, which only a restrictive mask makes visible.
	for _, m := range []string{"755", "777", "0", "1777", "2755", "+t", "a=rx", "u+rwx,go-w"} {
		for _, mask := range mkdirMasks {
			add(invocation{
				name:     fmt.Sprintf("parents mode %s under %03o", m, mask),
				args:     []string{"-p", "-m", m, "a/b/c"},
				seedTree: mkdirBare,
				umask:    withMask(mask),
			})
		}
	}
	// The default mode, and the ancestors' u+wx, over the masks that
	// take a class of bit away rather than a class of user.
	for _, mask := range []int{0o000, 0o022, 0o077, 0o300, 0o500, 0o600, 0o700, 0o777, 0o111, 0o222, 0o444, 0o070, 0o007} {
		add(invocation{name: fmt.Sprintf("default mode under %03o", mask), args: []string{"d"}, seedTree: mkdirBare, umask: withMask(mask)})
		add(invocation{name: fmt.Sprintf("ancestors under %03o", mask), args: []string{"-p", "a/b/c"}, seedTree: mkdirBare, umask: withMask(mask)})
	}
	for _, m := range mkdirBadModes {
		add(invocation{name: "refuses mode " + fmt.Sprintf("%q", m), args: []string{"-m", m, "d"}, seedTree: mkdirBare})
	}
	// A numeric perm ends its clause but not the MODE.
	add(invocation{name: "a numeric perm then another clause", args: []string{"-m", "=7,u+r", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a clause then a numeric perm", args: []string{"-m", "u+r,=7", "d"}, seedTree: mkdirBare})
	// Several actions in one clause, which the copy form has to end on.
	add(invocation{name: "two actions in one clause", args: []string{"-m", "u+r+w", "d"}, seedTree: mkdirBare})
	add(invocation{name: "two actions the second removing", args: []string{"-m", "u+r-w", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a copy then an action", args: []string{"-m", "u=g+w", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a who repeated", args: []string{"-m", "ugoa+r", "d"}, seedTree: mkdirBare})
	add(invocation{name: "the last -m wins", args: []string{"-m", "755", "-m", "700", "d"}, seedTree: mkdirBare})
	add(invocation{name: "the last -m wins the other way", args: []string{"-m", "700", "-m", "u+rwx,go-w", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a glued short mode", args: []string{"-m700", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a glued mode ends the cluster", args: []string{"-m755pv", "d"}})
	add(invocation{name: "a cluster before the glued mode", args: []string{"-pvm755", "d/e"}, seedTree: mkdirBare})
	// `-m -1` looks like an option and is a mode: a `-` action over the
	// numeric perm 1.
	add(invocation{name: "a mode that looks like an option", args: []string{"-m", "-1", "d"}, seedTree: mkdirBare})
	add(invocation{name: "two dashes are two empty actions", args: []string{"-m", "--", "d"}, seedTree: mkdirBare})

	// ---- -p ----
	add(invocation{name: "parents makes the chain", args: []string{"-v", "-p", "a/b/c"}, seedTree: mkdirBare})
	add(invocation{name: "parents names only what it made", args: []string{"-v", "-p", "a/b/c"}, seedTree: mkdirPartial})
	add(invocation{name: "parents over two operands", args: []string{"-v", "-p", "a/b", "c/d"}, seedTree: mkdirBare})
	add(invocation{name: "doubled slashes are kept verbatim", args: []string{"-v", "-p", "a//b//c"}, seedTree: mkdirBare})
	add(invocation{name: "a dot component is stepped over", args: []string{"-v", "-p", "./a/./b/"}, seedTree: mkdirBare})
	add(invocation{name: "a dotdot component is stepped over", args: []string{"-v", "-p", "a/b/../c"}, seedTree: mkdirBare})
	add(invocation{name: "a trailing slash is part of the name", args: []string{"-v", "-p", "a/"}, seedTree: mkdirBare})
	add(invocation{name: "two trailing slashes", args: []string{"-v", "-p", "a//"}, seedTree: mkdirBare})
	// A final `..` is still mkdir'ed, unlike an intermediate one.
	add(invocation{name: "parents ending in dotdot", args: []string{"-v", "-p", "a/.."}, seedTree: mkdirBare})
	add(invocation{name: "parents on dotdot alone", args: []string{"-v", "-p", ".."}, seedTree: mkdirBare})
	add(invocation{name: "parents on dot", args: []string{"-v", "-p", "."}, seedTree: mkdirBare})
	add(invocation{name: "parents on the root", args: []string{"-v", "-p", "/"}, seedTree: mkdirBare})
	add(invocation{name: "the root without parents", args: []string{"-v", "/"}, seedTree: mkdirBare})
	add(invocation{name: "an existing directory is no error under parents", args: []string{"-v", "-p", "d"}, seedTree: mkdirTaken})
	add(invocation{name: "an existing directory IS an error without", args: []string{"-v", "d"}, seedTree: mkdirTaken})
	// `-m` reaches a directory this run created and nothing else.
	add(invocation{name: "parents leaves an existing mode alone", args: []string{"-p", "-m", "755", "d"}, seedTree: mkdirTaken})
	add(invocation{name: "without parents that is an error", args: []string{"-m", "755", "d"}, seedTree: mkdirTaken})
	add(invocation{name: "parents on a regular file", args: []string{"-p", "f"}, seedTree: mkdirTaken})
	add(invocation{name: "parents through a regular file", args: []string{"-p", "f/g"}, seedTree: mkdirTaken})
	add(invocation{name: "parents two levels through a file", args: []string{"-p", "f/g/h"}, seedTree: mkdirTaken})
	add(invocation{name: "parents where an ancestor is a file", args: []string{"-v", "-p", "a/b/c"}, seedTree: mkdirBlocked})
	add(invocation{name: "parents where the last component is a file", args: []string{"-v", "-p", "a/b"}, seedTree: mkdirBlocked})
	add(invocation{name: "parents past a failed operand", args: []string{"-v", "-p", "a/b", "f", "c/d"}, seedTree: mkdirTaken})
	// The file with a trailing slash: the stat fails ENOTDIR and the
	// mkdir's own EEXIST is what gets reported.
	add(invocation{name: "parents on a file with a trailing slash", args: []string{"-p", "f/"}, seedTree: mkdirTaken})
	add(invocation{name: "a file with a trailing slash without parents", args: []string{"f/"}, seedTree: mkdirTaken})
	// A dangling symlink exists to mkdir and to nothing else.
	add(invocation{name: "parents on a dangling symlink", args: []string{"-p", "sl"}, seedTree: mkdirTaken})
	add(invocation{name: "parents through a dangling symlink", args: []string{"-p", "sl/x"}, seedTree: mkdirTaken})
	add(invocation{name: "a dangling symlink without parents", args: []string{"sl"}, seedTree: mkdirTaken})
	// A symlink to itself: the stat says ELOOP, and THAT is the one
	// diagnostic worded `cannot stat`.
	add(invocation{name: "parents on a symlink loop", args: []string{"-p", "loop"}, seedTree: mkdirTaken})
	add(invocation{name: "parents through a symlink loop", args: []string{"-p", "loop/x"}, seedTree: mkdirTaken})
	add(invocation{name: "a symlink loop without parents", args: []string{"loop"}, seedTree: mkdirTaken})
	// A symlink to a directory is a directory to the -p check.
	add(invocation{name: "parents on a symlink to a directory", args: []string{"-p", "sd"}, seedTree: mkdirTaken})
	add(invocation{name: "parents through a symlink to a directory", args: []string{"-v", "-p", "sd/x"}, seedTree: mkdirTaken})
	add(invocation{name: "through a symlink to a directory", args: []string{"-v", "sd/x"}, seedTree: mkdirTaken})
	add(invocation{name: "a symlink to a directory without parents", args: []string{"sd"}, seedTree: mkdirTaken})

	// An ancestor that exists and cannot be entered is named by the
	// ancestor's own path; the mkdir of a child under it names the
	// child. The two are the same refusal reported from different steps.
	add(invocation{name: "parents through an unsearchable directory", args: []string{"-p", "blk/x"}, seedTree: mkdirGuarded})
	add(invocation{name: "through an unsearchable directory", args: []string{"blk/x"}, seedTree: mkdirGuarded})
	add(invocation{name: "parents under an unwritable directory", args: []string{"-p", "ro/x/y"}, seedTree: mkdirGuarded})
	add(invocation{name: "under an unwritable directory", args: []string{"ro/x"}, seedTree: mkdirGuarded})

	// ---- what a plain run does ----
	add(invocation{name: "one directory", args: []string{"-v", "d"}, seedTree: mkdirBare})
	add(invocation{name: "three directories", args: []string{"-v", "x", "y", "z"}, seedTree: mkdirBare})
	add(invocation{name: "a missing parent without parents", args: []string{"-v", "a/b"}, seedTree: mkdirBare})
	add(invocation{name: "a trailing slash", args: []string{"-v", "d/"}, seedTree: mkdirBare})
	// A failed operand costs the status and not the run.
	add(invocation{name: "the run carries on past a failure", args: []string{"-v", "x", "f", "y"}, seedTree: mkdirTaken})
	add(invocation{name: "the same name twice", args: []string{"-v", "a", "a"}, seedTree: mkdirBare})
	add(invocation{name: "an empty operand", args: []string{"-v", ""}, seedTree: mkdirBare})
	add(invocation{name: "an empty operand under parents", args: []string{"-v", "-p", ""}, seedTree: mkdirBare})
	add(invocation{name: "a lone dash is a name", args: []string{"-v", "-"}, seedTree: mkdirBare})
	add(invocation{name: "dashdash then an option-shaped name", args: []string{"-v", "--", "-x"}, seedTree: mkdirBare})
	add(invocation{name: "dashdash then dashdash", args: []string{"-v", "--", "--"}, seedTree: mkdirBare})
	add(invocation{name: "a name that is not valid UTF-8", args: []string{"-v", "\xff\xfe"}, seedTree: mkdirBare})
	add(invocation{name: "a component past NAME_MAX", args: []string{"-v", mkdirLong(256)}, seedTree: mkdirBare})
	add(invocation{name: "a component at NAME_MAX", args: []string{"-v", mkdirLong(255)}, seedTree: mkdirBare})
	add(invocation{name: "an ancestor past NAME_MAX", args: []string{"-v", "-p", mkdirLong(256) + "/x"}, seedTree: mkdirBare})

	// ---- quoting: the -v line and the error line differ ----
	for _, n := range []string{"a b", "a'b", "a\"b", "a\\b", "a\tb", "a\nb", "\xff\xfe", "a~b", "a:b"} {
		add(invocation{name: "created " + fmt.Sprintf("%q", n), args: []string{"-v", "--", n}, seedTree: mkdirBare})
		add(invocation{name: "taken " + fmt.Sprintf("%q", n), args: []string{"-v", "--", n}, seedTree: mkdirNames})
	}
	add(invocation{name: "an invalid mode is quoted the error way", args: []string{"-m", "a\tb", "d"}})
	add(invocation{name: "an invalid mode with an apostrophe", args: []string{"-m", "a'b", "d"}})
	add(invocation{name: "an invalid mode with a high byte", args: []string{"-m", "\xff", "d"}})

	// ---- -Z and --context ----
	add(invocation{name: "Z alone", args: []string{"-Z", "-v", "d"}, seedTree: mkdirBare})
	add(invocation{name: "Z twice", args: []string{"-ZZ", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a bare context", args: []string{"--context", "-v", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a context with a value", args: []string{"--context=foo", "-v", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a context with an empty value", args: []string{"--context=", "-v", "d"}, seedTree: mkdirBare})
	// The warning is per OCCURRENCE and comes out where the option was
	// read, so it precedes a later getopt fault and the missing operand.
	add(invocation{name: "a context value twice warns twice", args: []string{"--context=a", "--context=b", "d"}, seedTree: mkdirBare})
	add(invocation{name: "two spellings warn twice", args: []string{"--context=a", "--contex=b", "d"}, seedTree: mkdirBare})
	add(invocation{name: "context by two-letter prefix", args: []string{"--co", "d"}, seedTree: mkdirBare})
	add(invocation{name: "context by three-letter prefix", args: []string{"--con=x", "d"}, seedTree: mkdirBare})
	// -Z takes no value, so the rest of the cluster is read as options.
	add(invocation{name: "a value glued to Z", args: []string{"-Zfoo", "d"}})
	add(invocation{name: "the warning precedes a getopt fault", args: []string{"--context=foo", "-x", "d"}})
	add(invocation{name: "the warning precedes the missing operand", args: []string{"--context=foo"}})
	add(invocation{name: "Z and a context value", args: []string{"-Z", "--context=foo", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a context value under parents", args: []string{"--context=foo", "-p", "a/b"}, seedTree: mkdirBare})

	// ---- the write-failure paths ----
	// Nothing is written without -v, so a stdout that cannot be written
	// is not noticed at all.
	add(invocation{name: "a closed stdout with nothing to say", args: []string{"d"}, seedTree: mkdirBare, stdout: stdoutClosed})
	add(invocation{name: "a closed stdout with a verbose line", args: []string{"-v", "d"}, seedTree: mkdirBare, stdout: stdoutClosed})
	add(invocation{name: "a full stdout with nothing to say", args: []string{"d"}, seedTree: mkdirBare, stdout: stdoutFull})
	add(invocation{name: "a full stdout with a verbose line", args: []string{"-v", "d"}, seedTree: mkdirBare, stdout: stdoutFull})
	add(invocation{name: "a full stdout and a failed operand", args: []string{"-v", "x", "f"}, seedTree: mkdirTaken, stdout: stdoutFull})

	return cases
}

func mkdirLong(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}

func TestMkdir(t *testing.T) {
	requireParity(t, "mkdir", mkdirCases(t))
}

// The two options whose TEXT is ours by design (docs/COREUTILS.md).
// Everything about them still has to match GNU, including that an
// operand on either side of them changes nothing: `mkdir d --help`
// prints the help and makes no directory. The corpus cannot hold those
// two because it would compare the text, so the operand forms are here.
func TestMkdirHelp(t *testing.T) {
	requireHelp(t, "mkdir", []string{"--help"}, 0)
	requireHelp(t, "mkdir", []string{"--hel"}, 0)
	requireHelp(t, "mkdir", []string{"--help", "extra"}, 0)
	requireHelp(t, "mkdir", []string{"d", "--help"}, 0)
	requireVersion(t, "mkdir", []string{"--version"}, 0)
	requireVersion(t, "mkdir", []string{"--vers"}, 0)
	requireVersion(t, "mkdir", []string{"--version", "d"}, 0)
}
