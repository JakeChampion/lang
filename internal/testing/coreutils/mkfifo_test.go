package coreutils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// mkfifo(1)'s whole subject is a mode, so most of the corpus names a
// creation mask. Three rules separate it from `mkdir -m`, and each has
// cases here:
//
//   - The base is 0666, not 0777. `-m u+X` therefore leaves 0666 — there
//     is no execute bit already set for `X` to copy — where the same
//     option on a directory adds all three.
//   - A MODE that names any bit outside the nine permission bits is
//     refused outright, so `+t`, `u+s` and `1777` are all `mode must
//     specify only file permission bits` rather than modes that work.
//     mkdir accepts every one of them.
//   - The refusal of a MODE that is not one does NOT name it: `mkfifo -m
//     bogus` is the bare `invalid mode`, where mkdir quotes the operand.
//
// The umask reaches a `-m` run twice: once through the mknod, which
// filters whatever mode it is handed, and once through the chmod that
// follows and does not. That is why `-m 777` is 0777 under every mask
// while `-m +w` is 0666 under every mask — the mask reaches the MODE's
// who-less clauses and then the chmod puts the result on unfiltered.
//
// `-Z` and `--context` have only one reading here: no machine this
// corpus runs on has SELinux, so only the kernel-has-none path is ever
// compared, and docs/COREUTILS.md records that.

func init() {
	registerCorpus("mkfifo", mkfifoCases)
}

// mkfifoBare is an empty directory, so everything in the tree comparison
// is something the run created.
func mkfifoBare(t *testing.T, dir string) {
	t.Helper()
}

// mkfifoTaken holds one of every name the creation can collide with.
func mkfifoTaken(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "p"), nil, 0o644); err != nil {
		t.Fatalf("write p: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, "p"), 0o644); err != nil {
		t.Fatalf("chmod p: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "q"), 0o755); err != nil {
		t.Fatalf("mkdir q: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, "q"), 0o755); err != nil {
		t.Fatalf("chmod q: %v", err)
	}
	if err := os.Symlink("nowhere", filepath.Join(dir, "sl")); err != nil {
		t.Fatalf("symlink sl: %v", err)
	}
}

// The masks the modes run under. 000 and 777 are the two ends, 022 the
// usual one, 002 and 027 differ from it in the group bits alone, and 111
// and 222 take away a class of BIT rather than a class of user — which
// is what tells `~umask` apart from a per-triple rule.
var mkfifoMasks = []int{0o000, 0o022, 0o077, 0o002, 0o027, 0o111, 0o222, 0o777}

// The MODE operands GNU accepts, run under every mask. The first group
// is octal (which the mask never reaches), then the who-less clauses it
// does, then the copy and X forms that make the 0666 base visible.
var mkfifoModes = []string{
	"755", "777", "0", "666", "600", "444",
	"=rwx", "+w", "-w", "=", "+", "-", "+r", "-r", "+x", "-x",
	"u+rwx,go-w", "a=rx", "a+rwX", "a=", "u=rw,go=r",
	"+u", "-u", "=u", "u=g", "g=u", "o=u",
	"=7", "-7", "+7", "+10", "=1", "=0",
	"+X", "=X", "-X", "u+X", "g+X", "o=t",
	"ugoa+r", "u+r+w", "u+r-w", "u=g+w",
}

// The MODE operands GNU refuses, and the two ways it refuses them: a
// MODE that is not one at all is `invalid mode`, and one that is a mode
// but names a bit outside the permission nine is `mode must specify only
// file permission bits`. A mask cannot change either, so these run once.
var mkfifoBadModes = []string{
	"", "8", "9", "a", "u", "rwx", "z+r", "ax+r", "+a", "a=a", "u=A",
	"010000", "17777", "0x755", " 755", "755 ", "+ ",
	"u+rwx,", ",u+rwx", "a+r,u", "0755,", ",0755", "755,755",
	"u=7", "u+4", "=7=7", "u=go", "-m",
	"+t", "+s", "-s", "-t", "=t", "=s", "g+s", "u+s",
	"1777", "1755", "2755", "4755", "7777", "+t,g+w", "+rwxst", "u+rwxXst",
}

func mkfifoCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

	// ---- the arities and the two standard options ----
	add(invocation{name: "no operands"})
	add(invocation{name: "dashdash alone", args: []string{"--"}})
	add(invocation{name: "Z with no operands", args: []string{"-Z"}})
	add(invocation{name: "context with no operands", args: []string{"--context"}})
	add(invocation{name: "mode with no operands", args: []string{"-m", "755", "--"}})
	// The MODE is compiled only once there is something to create, so a
	// mode that is not one loses to the missing operand — which is the
	// other way round from mknod.
	add(invocation{name: "missing operand outranks a bad mode", args: []string{"-m", "bogus"}})
	add(invocation{name: "missing operand outranks a non-permission mode", args: []string{"-m", "7777"}})
	add(invocation{name: "help with a value", args: []string{"--help=x"}})
	add(invocation{name: "version with a value", args: []string{"--version=1"}})

	// ---- getopt faults ----
	add(invocation{name: "invalid short option", args: []string{"-x", "p"}})
	add(invocation{name: "invalid byte in a cluster", args: []string{"-Zq", "p"}})
	add(invocation{name: "unrecognized long option", args: []string{"--foo", "p"}})
	add(invocation{name: "an option after an operand permutes", args: []string{"p", "-x"}})
	add(invocation{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"a", "-Z", "b"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: mkfifoBare})
	// The ambiguity list is in declaration order, and `--context` leads it.
	add(invocation{name: "the empty long option", args: []string{"--=x"}})
	add(invocation{name: "the empty long option with an operand", args: []string{"--=x", "p"}})
	add(invocation{name: "mode requires an argument", args: []string{"-m"}})
	add(invocation{name: "the long mode requires an argument", args: []string{"--mode"}})
	add(invocation{name: "the long mode eats the operand", args: []string{"--mode", "p"}})
	add(invocation{name: "an empty long mode value", args: []string{"--mode=", "p"}})
	add(invocation{name: "mode by one-letter prefix", args: []string{"--m=700", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "getopt runs before the mode is read", args: []string{"-m", "bogus", "-x", "p"}})
	add(invocation{name: "a bad mode makes nothing", args: []string{"-m", "bogus", "p", "q"}, seedTree: mkfifoBare})
	add(invocation{name: "a glued short mode", args: []string{"-m700", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "the last mode wins", args: []string{"-m", "666", "-m", "700", "p"}, seedTree: mkfifoBare})
	// `-m -1` looks like an option and is a mode: a `-` action over the
	// numeric perm 1.
	add(invocation{name: "a mode that looks like an option", args: []string{"-m", "-1", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "two dashes are two empty actions", args: []string{"-m", "--", "p"}, seedTree: mkfifoBare})

	// ---- the mode grammar, against the mask ----
	for _, m := range mkfifoModes {
		for _, mask := range mkfifoMasks {
			add(invocation{
				name:     fmt.Sprintf("mode %s under %03o", m, mask),
				args:     []string{"-m", m, "p"},
				seedTree: mkfifoBare,
				umask:    withMask(mask),
			})
		}
	}
	// The default mode is 0666 filtered by the mask and nothing else.
	for _, mask := range []int{0o000, 0o022, 0o077, 0o002, 0o111, 0o222, 0o444, 0o777, 0o070, 0o007} {
		add(invocation{name: fmt.Sprintf("the default mode under %03o", mask), args: []string{"p"}, seedTree: mkfifoBare, umask: withMask(mask)})
	}
	for _, m := range mkfifoBadModes {
		add(invocation{name: "refuses mode " + fmt.Sprintf("%q", m), args: []string{"-m", m, "p"}, seedTree: mkfifoBare})
	}

	// ---- what a plain run does ----
	add(invocation{name: "one pipe", args: []string{"p"}, seedTree: mkfifoBare})
	add(invocation{name: "three pipes", args: []string{"a", "b", "c"}, seedTree: mkfifoBare})
	add(invocation{name: "three pipes with a mode", args: []string{"-m", "777", "a", "b", "c"}, seedTree: mkfifoBare})
	// A failed operand costs the status and not the run.
	add(invocation{name: "the run carries on past a failure", args: []string{"a", "p", "b"}, seedTree: mkfifoTaken})
	add(invocation{name: "the same name twice", args: []string{"a", "a"}, seedTree: mkfifoBare})
	add(invocation{name: "a name that is already a file", args: []string{"p"}, seedTree: mkfifoTaken})
	add(invocation{name: "a name that is already a directory", args: []string{"q"}, seedTree: mkfifoTaken})
	add(invocation{name: "a name that is a dangling symlink", args: []string{"sl"}, seedTree: mkfifoTaken})
	add(invocation{name: "a name that is already a pipe", args: []string{"p", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "a missing directory component", args: []string{"nodir/p"}, seedTree: mkfifoBare})
	add(invocation{name: "an empty operand", args: []string{""}, seedTree: mkfifoBare})
	add(invocation{name: "a lone dash is a name", args: []string{"-"}, seedTree: mkfifoBare})
	add(invocation{name: "dashdash then an option-shaped name", args: []string{"--", "-p"}, seedTree: mkfifoBare})
	add(invocation{name: "dashdash then dashdash", args: []string{"--", "--"}, seedTree: mkfifoBare})
	add(invocation{name: "a trailing slash", args: []string{"p/"}, seedTree: mkfifoBare})
	add(invocation{name: "a name past NAME_MAX", args: []string{mkfifoLong(256)}, seedTree: mkfifoBare})
	add(invocation{name: "a name at NAME_MAX", args: []string{mkfifoLong(255)}, seedTree: mkfifoBare})
	add(invocation{name: "a name that is not valid UTF-8", args: []string{"--", "\xff\xfe"}, seedTree: mkfifoBare})

	// ---- quoting ----
	for _, n := range []string{"a b", "a'b", "a\"b", "a\\b", "a\tb", "a\nb", "a~b", "a:b"} {
		add(invocation{name: "created " + fmt.Sprintf("%q", n), args: []string{"--", n}, seedTree: mkfifoBare})
		add(invocation{name: "taken " + fmt.Sprintf("%q", n), args: []string{"--", n, n}, seedTree: mkfifoBare})
	}

	// ---- -Z and --context ----
	add(invocation{name: "Z alone", args: []string{"-Z", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "Z twice", args: []string{"-ZZ", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "a bare context", args: []string{"--context", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "a context with a value", args: []string{"--context=foo", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "a context with an empty value", args: []string{"--context=", "p"}, seedTree: mkfifoBare})
	// The warning is per OCCURRENCE and comes out where the option was
	// read, so it precedes a later getopt fault and the missing operand.
	add(invocation{name: "a context value twice warns twice", args: []string{"--context=a", "--context=b", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "two spellings warn twice", args: []string{"--context=a", "--contex=b", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "context by two-letter prefix", args: []string{"--co", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "context by three-letter prefix", args: []string{"--con=x", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "a value glued to Z", args: []string{"-Zfoo", "p"}})
	add(invocation{name: "the warning precedes a getopt fault", args: []string{"--context=foo", "-x", "p"}})
	add(invocation{name: "the warning precedes the missing operand", args: []string{"--context=foo"}})
	add(invocation{name: "Z and a context value", args: []string{"-Z", "--context=foo", "p"}, seedTree: mkfifoBare})
	add(invocation{name: "Z and a mode", args: []string{"-Z", "-m", "700", "p"}, seedTree: mkfifoBare})

	// ---- the write-failure paths ----
	// Nothing is written to stdout, so a stdout that cannot be written is
	// not noticed at all.
	add(invocation{name: "a closed stdout", args: []string{"p"}, seedTree: mkfifoBare, stdout: stdoutClosed})
	add(invocation{name: "a closed stdout with a diagnostic", args: []string{"p"}, seedTree: mkfifoTaken, stdout: stdoutClosed})
	add(invocation{name: "a full stdout", args: []string{"p"}, seedTree: mkfifoBare, stdout: stdoutFull})
	add(invocation{name: "a full stdout with a diagnostic", args: []string{"p"}, seedTree: mkfifoTaken, stdout: stdoutFull})

	return cases
}

func mkfifoLong(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}

func TestMkfifoParity(t *testing.T) {
	requireParity(t, "mkfifo", mkfifoCases(t))
}

func TestMkfifoHelpVersion(t *testing.T) {
	requireHelp(t, "mkfifo", []string{"--help"}, 0)
	requireHelp(t, "mkfifo", []string{"--hel"}, 0)
	requireHelp(t, "mkfifo", []string{"--help", "extra"}, 0)
	requireHelp(t, "mkfifo", []string{"p", "--help"}, 0)
	requireVersion(t, "mkfifo", []string{"--version"}, 0)
	requireVersion(t, "mkfifo", []string{"--vers"}, 0)
	// `--v` is unambiguous here: there is no `--verbose` to collide with.
	requireVersion(t, "mkfifo", []string{"--v"}, 0)
	requireVersion(t, "mkfifo", []string{"--version", "p"}, 0)
}
