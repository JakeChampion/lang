package coreutils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func init() {
	registerCorpus("mkdir", mkdirCases)
}

func mkdirBare(t *testing.T, dir string) {
	t.Helper()
}

func mkdirTaken(t *testing.T, dir string) {
	t.Helper()
	mkdirMake(t, dir, "d", 0o700)
	mkdirFile(t, dir, "f")
	mkdirMake(t, dir, "dd", 0o755)
	mkdirLink(t, "dd", dir, "sd")
	mkdirLink(t, "nowhere", dir, "sl")
	mkdirLink(t, "loop", dir, "loop")
}

func mkdirPartial(t *testing.T, dir string) {
	t.Helper()
	mkdirMake(t, dir, "a", 0o755)
	mkdirMake(t, dir, filepath.Join("a", "b"), 0o755)
}

func mkdirBlocked(t *testing.T, dir string) {
	t.Helper()
	mkdirFile(t, dir, "f")
	mkdirMake(t, dir, "a", 0o755)
	mkdirFile(t, dir, filepath.Join("a", "b"))
}

func mkdirGuarded(t *testing.T, dir string) {
	t.Helper()
	mkdirMake(t, dir, "blk", 0o000)
	mkdirMake(t, dir, "ro", 0o555)
}

func mkdirQuotedNames(t *testing.T) []string {
	t.Helper()
	names := []string{"a b", "a'b", "a\"b", "a\\b", "a\tb", "a\nb", "a~b", "a:b"}
	if rawByteNamesHeld(t) {
		names = append(names, "\xff\xfe")
	}
	return names
}

func mkdirNames(t *testing.T, dir string) {
	t.Helper()
	for _, n := range mkdirQuotedNames(t) {
		mkdirFile(t, dir, n)
	}
}

func mkdirMake(t *testing.T, dir, name string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.Mkdir(path, mode); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
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

var mkdirMasks = []int{0o000, 0o022, 0o077, 0o002, 0o027, 0o111, 0o222, 0o777}

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

	add(invocation{name: "no operands"})
	add(invocation{name: "dashdash alone", args: []string{"--"}})
	add(invocation{name: "parents with no operands", args: []string{"-p"}})
	add(invocation{name: "verbose with no operands", args: []string{"-v"}})
	add(invocation{name: "Z with no operands", args: []string{"-Z"}})
	add(invocation{name: "context with no operands", args: []string{"--context"}})
	add(invocation{name: "mode with no operands", args: []string{"-m", "755", "--"}})
	add(invocation{name: "missing operand outranks a bad mode", args: []string{"-m", "bogus"}})
	add(invocation{name: "help with a value", args: []string{"--help=x"}})
	add(invocation{name: "version with a value", args: []string{"--version=1"}})

	add(invocation{name: "invalid short option", args: []string{"-x"}})
	add(invocation{name: "invalid byte in a cluster", args: []string{"-Zq", "d"}})
	add(invocation{name: "unrecognized long option", args: []string{"--foo"}})
	add(invocation{name: "an option after an operand permutes", args: []string{"a", "-x"}})
	add(invocation{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"a", "-v", "b"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: mkdirBare})
	add(invocation{name: "the empty long option", args: []string{"--=x"}})
	add(invocation{name: "the empty long option with an operand", args: []string{"--=x", "d"}})
	add(invocation{name: "ambiguous between verbose and version", args: []string{"--v"}})
	add(invocation{name: "still ambiguous three letters in", args: []string{"--ver"}})
	add(invocation{name: "parents takes no argument", args: []string{"--parents=x", "d"}})
	add(invocation{name: "verbose takes no argument", args: []string{"--verbose=x", "d"}})
	add(invocation{name: "mode requires an argument", args: []string{"-m"}})
	add(invocation{name: "the long mode eats the operand", args: []string{"--mode", "d"}})
	add(invocation{name: "an empty long mode value", args: []string{"--mode=", "d"}})
	add(invocation{name: "mode by one-letter prefix", args: []string{"--m=755", "d"}, seedTree: mkdirBare})
	add(invocation{name: "context by one-letter prefix", args: []string{"--c", "d"}, seedTree: mkdirBare})
	add(invocation{name: "getopt runs before the mode is read", args: []string{"-m", "bogus", "-x", "d"}})
	add(invocation{name: "a bad mode makes nothing", args: []string{"-m", "bogus", "d", "e"}, seedTree: mkdirBare})

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
	for _, mask := range []int{0o000, 0o022, 0o077, 0o300, 0o500, 0o600, 0o700, 0o777, 0o111, 0o222, 0o444, 0o070, 0o007} {
		add(invocation{name: fmt.Sprintf("default mode under %03o", mask), args: []string{"d"}, seedTree: mkdirBare, umask: withMask(mask)})
		add(invocation{name: fmt.Sprintf("ancestors under %03o", mask), args: []string{"-p", "a/b/c"}, seedTree: mkdirBare, umask: withMask(mask)})
	}
	for _, m := range mkdirBadModes {
		add(invocation{name: "refuses mode " + fmt.Sprintf("%q", m), args: []string{"-m", m, "d"}, seedTree: mkdirBare})
	}
	add(invocation{name: "a numeric perm then another clause", args: []string{"-m", "=7,u+r", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a clause then a numeric perm", args: []string{"-m", "u+r,=7", "d"}, seedTree: mkdirBare})
	add(invocation{name: "two actions in one clause", args: []string{"-m", "u+r+w", "d"}, seedTree: mkdirBare})
	add(invocation{name: "two actions the second removing", args: []string{"-m", "u+r-w", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a copy then an action", args: []string{"-m", "u=g+w", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a who repeated", args: []string{"-m", "ugoa+r", "d"}, seedTree: mkdirBare})
	add(invocation{name: "the last -m wins", args: []string{"-m", "755", "-m", "700", "d"}, seedTree: mkdirBare})
	add(invocation{name: "the last -m wins the other way", args: []string{"-m", "700", "-m", "u+rwx,go-w", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a glued short mode", args: []string{"-m700", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a glued mode ends the cluster", args: []string{"-m755pv", "d"}})
	add(invocation{name: "a cluster before the glued mode", args: []string{"-pvm755", "d/e"}, seedTree: mkdirBare})
	add(invocation{name: "a mode that looks like an option", args: []string{"-m", "-1", "d"}, seedTree: mkdirBare})
	add(invocation{name: "two dashes are two empty actions", args: []string{"-m", "--", "d"}, seedTree: mkdirBare})

	add(invocation{name: "parents makes the chain", args: []string{"-v", "-p", "a/b/c"}, seedTree: mkdirBare})
	add(invocation{name: "parents names only what it made", args: []string{"-v", "-p", "a/b/c"}, seedTree: mkdirPartial})
	add(invocation{name: "parents over two operands", args: []string{"-v", "-p", "a/b", "c/d"}, seedTree: mkdirBare})
	add(invocation{name: "doubled slashes are kept verbatim", args: []string{"-v", "-p", "a//b//c"}, seedTree: mkdirBare})
	add(invocation{name: "a dot component is stepped over", args: []string{"-v", "-p", "./a/./b/"}, seedTree: mkdirBare})
	add(invocation{name: "a dotdot component is stepped over", args: []string{"-v", "-p", "a/b/../c"}, seedTree: mkdirBare})
	add(invocation{name: "a trailing slash is part of the name", args: []string{"-v", "-p", "a/"}, seedTree: mkdirBare})
	add(invocation{name: "two trailing slashes", args: []string{"-v", "-p", "a//"}, seedTree: mkdirBare})
	add(invocation{name: "parents ending in dotdot", args: []string{"-v", "-p", "a/.."}, seedTree: mkdirBare})
	add(invocation{name: "parents on dotdot alone", args: []string{"-v", "-p", ".."}, seedTree: mkdirBare})
	add(invocation{name: "parents on dot", args: []string{"-v", "-p", "."}, seedTree: mkdirBare})
	add(invocation{name: "parents on the root", args: []string{"-v", "-p", "/"}, seedTree: mkdirBare})
	add(invocation{name: "the root without parents", args: []string{"-v", "/"}, seedTree: mkdirBare})
	add(invocation{name: "an existing directory is no error under parents", args: []string{"-v", "-p", "d"}, seedTree: mkdirTaken})
	add(invocation{name: "an existing directory IS an error without", args: []string{"-v", "d"}, seedTree: mkdirTaken})
	add(invocation{name: "parents leaves an existing mode alone", args: []string{"-p", "-m", "755", "d"}, seedTree: mkdirTaken})
	add(invocation{name: "without parents that is an error", args: []string{"-m", "755", "d"}, seedTree: mkdirTaken})
	add(invocation{name: "parents on a regular file", args: []string{"-p", "f"}, seedTree: mkdirTaken})
	add(invocation{name: "parents through a regular file", args: []string{"-p", "f/g"}, seedTree: mkdirTaken})
	add(invocation{name: "parents two levels through a file", args: []string{"-p", "f/g/h"}, seedTree: mkdirTaken})
	add(invocation{name: "parents where an ancestor is a file", args: []string{"-v", "-p", "a/b/c"}, seedTree: mkdirBlocked})
	add(invocation{name: "parents where the last component is a file", args: []string{"-v", "-p", "a/b"}, seedTree: mkdirBlocked})
	add(invocation{name: "parents past a failed operand", args: []string{"-v", "-p", "a/b", "f", "c/d"}, seedTree: mkdirTaken})
	add(invocation{name: "parents on a file with a trailing slash", args: []string{"-p", "f/"}, seedTree: mkdirTaken})
	add(invocation{name: "a file with a trailing slash without parents", args: []string{"f/"}, seedTree: mkdirTaken})
	add(invocation{name: "parents on a dangling symlink", args: []string{"-p", "sl"}, seedTree: mkdirTaken})
	add(invocation{name: "parents through a dangling symlink", args: []string{"-p", "sl/x"}, seedTree: mkdirTaken})
	add(invocation{name: "a dangling symlink without parents", args: []string{"sl"}, seedTree: mkdirTaken})
	add(invocation{name: "parents on a symlink loop", args: []string{"-p", "loop"}, seedTree: mkdirTaken})
	add(invocation{name: "parents through a symlink loop", args: []string{"-p", "loop/x"}, seedTree: mkdirTaken})
	add(invocation{name: "a symlink loop without parents", args: []string{"loop"}, seedTree: mkdirTaken})
	add(invocation{name: "parents on a symlink to a directory", args: []string{"-p", "sd"}, seedTree: mkdirTaken})
	add(invocation{name: "parents through a symlink to a directory", args: []string{"-v", "-p", "sd/x"}, seedTree: mkdirTaken})
	add(invocation{name: "through a symlink to a directory", args: []string{"-v", "sd/x"}, seedTree: mkdirTaken})
	add(invocation{name: "a symlink to a directory without parents", args: []string{"sd"}, seedTree: mkdirTaken})

	add(invocation{name: "parents through an unsearchable directory", args: []string{"-p", "blk/x"}, seedTree: mkdirGuarded})
	add(invocation{name: "through an unsearchable directory", args: []string{"blk/x"}, seedTree: mkdirGuarded})
	add(invocation{name: "parents under an unwritable directory", args: []string{"-p", "ro/x/y"}, seedTree: mkdirGuarded})
	add(invocation{name: "under an unwritable directory", args: []string{"ro/x"}, seedTree: mkdirGuarded})

	add(invocation{name: "one directory", args: []string{"-v", "d"}, seedTree: mkdirBare})
	add(invocation{name: "three directories", args: []string{"-v", "x", "y", "z"}, seedTree: mkdirBare})
	add(invocation{name: "a missing parent without parents", args: []string{"-v", "a/b"}, seedTree: mkdirBare})
	add(invocation{name: "a trailing slash", args: []string{"-v", "d/"}, seedTree: mkdirBare})
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

	for _, n := range mkdirQuotedNames(t) {
		add(invocation{name: "created " + fmt.Sprintf("%q", n), args: []string{"-v", "--", n}, seedTree: mkdirBare})
		add(invocation{name: "taken " + fmt.Sprintf("%q", n), args: []string{"-v", "--", n}, seedTree: mkdirNames})
	}
	add(invocation{name: "an invalid mode is quoted the error way", args: []string{"-m", "a\tb", "d"}})
	add(invocation{name: "an invalid mode with an apostrophe", args: []string{"-m", "a'b", "d"}})
	add(invocation{name: "an invalid mode with a high byte", args: []string{"-m", "\xff", "d"}})

	add(invocation{name: "Z alone", args: []string{"-Z", "-v", "d"}, seedTree: mkdirBare})
	add(invocation{name: "Z twice", args: []string{"-ZZ", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a bare context", args: []string{"--context", "-v", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a context with a value", args: []string{"--context=foo", "-v", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a context with an empty value", args: []string{"--context=", "-v", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a context value twice warns twice", args: []string{"--context=a", "--context=b", "d"}, seedTree: mkdirBare})
	add(invocation{name: "two spellings warn twice", args: []string{"--context=a", "--contex=b", "d"}, seedTree: mkdirBare})
	add(invocation{name: "context by two-letter prefix", args: []string{"--co", "d"}, seedTree: mkdirBare})
	add(invocation{name: "context by three-letter prefix", args: []string{"--con=x", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a value glued to Z", args: []string{"-Zfoo", "d"}})
	add(invocation{name: "the warning precedes a getopt fault", args: []string{"--context=foo", "-x", "d"}})
	add(invocation{name: "the warning precedes the missing operand", args: []string{"--context=foo"}})
	add(invocation{name: "Z and a context value", args: []string{"-Z", "--context=foo", "d"}, seedTree: mkdirBare})
	add(invocation{name: "a context value under parents", args: []string{"--context=foo", "-p", "a/b"}, seedTree: mkdirBare})

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

func TestMkdirHelp(t *testing.T) {
	requireHelp(t, "mkdir", []string{"--help"}, 0)
	requireHelp(t, "mkdir", []string{"--hel"}, 0)
	requireHelp(t, "mkdir", []string{"--help", "extra"}, 0)
	requireHelp(t, "mkdir", []string{"d", "--help"}, 0)
	requireVersion(t, "mkdir", []string{"--version"}, 0)
	requireVersion(t, "mkdir", []string{"--vers"}, 0)
	requireVersion(t, "mkdir", []string{"--version", "d"}, 0)
}
