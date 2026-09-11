package coreutils

import (
	"os/user"
	"testing"
)

// A user name that exists on the machine the suite runs on, and one
// that does not. The first is read from the passwd database rather than
// assumed: `root` is there on every Linux and macOS, but the point of
// the operand cases is a name whose group list is not the caller's, and
// the caller may BE root.
func idUserNames(t *testing.T) (self string, other string) {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	self = u.Username
	other = "root"
	if self == "root" {
		other = "daemon"
		if _, err := user.Lookup(other); err != nil {
			// A machine without `daemon` still has the caller; the
			// operand cases then compare the caller by name, which
			// still exercises the operand path.
			other = self
		}
	}
	return self, other
}

func init() {
	registerCorpus("id", idCases)
}

// id(1) — the composite line, the four "only" modes, and the option
// combinations each of them refuses.
//
// Everything here is machine-dependent (the ids, the names, the group
// list), so every case is a comparison between two siblings of this
// process rather than a pinned string. The cases that are NOT about
// this machine are the ones worth reading:
//
//   - The operand is looked up by name and then as a decimal uid, so
//     `id 0` is root where `groups 0` is `no such user`. `0x0`, `4a`,
//     `4 ` and 4294967295 are all refused, and ` +4` is not.
//   - `-n` / `-r` / `-z` are each refused in the default format, and
//     two of `-ugGZ` together are refused whatever they are.
//   - `-Z` is refused before any of that on a kernel with no SELinux.
//   - `-G` with `-z` and more than one operand writes a second NUL per
//     operand. `-G` without `-z`, and `-u` with it, do not.
func idCases(t *testing.T) []invocation {
	self, other := idUserNames(t)
	return []invocation{
		// The composite line, and the four "only" modes.
		{name: "no arguments"},
		{name: "user id", args: []string{"-u"}},
		{name: "group id", args: []string{"-g"}},
		{name: "group list", args: []string{"-G"}},
		{name: "user name", args: []string{"-un"}},
		{name: "group name", args: []string{"-gn"}},
		{name: "group list names", args: []string{"-Gn"}},
		{name: "real user id", args: []string{"-ur"}},
		{name: "real group id", args: []string{"-gr"}},
		{name: "real group list", args: []string{"-Gr"}},
		{name: "real user name", args: []string{"-unr"}},
		{name: "long user", args: []string{"--user"}},
		{name: "long group", args: []string{"--group"}},
		{name: "long groups", args: []string{"--groups"}},
		{name: "long name and user", args: []string{"--name", "--user"}},
		{name: "long real and group", args: []string{"--real", "--group"}},

		// -a is accepted and does nothing.
		{name: "a alone", args: []string{"-a"}},
		{name: "a with user", args: []string{"-a", "-u"}},
		{name: "a after user", args: []string{"-ua"}},

		// --zero, and the second terminator -G alone grows.
		{name: "zero user", args: []string{"-zu"}},
		{name: "zero group", args: []string{"-zg"}},
		{name: "zero group list", args: []string{"-zG"}},
		{name: "zero group list names", args: []string{"-zGn"}},
		{name: "zero user one operand", args: []string{"-zu", other}},
		{name: "zero user two operands", args: []string{"-zu", other, other}},
		{name: "zero group list one operand", args: []string{"-zG", other}},
		{name: "zero group list two operands", args: []string{"-zG", other, other}},
		{name: "zero group list two different operands", args: []string{"-zG", other, self}},
		{name: "group list two operands without zero", args: []string{"-G", other, other}},
		{name: "composite two operands", args: []string{other, other}},

		// Operands.
		{name: "own name as an operand", args: []string{self}},
		{name: "another name as an operand", args: []string{other}},
		{name: "operand group list", args: []string{"-G", other}},
		{name: "operand group list names", args: []string{"-Gn", other}},
		{name: "operand user id", args: []string{"-u", other}},
		{name: "operand user name", args: []string{"-un", other}},
		{name: "operand group id", args: []string{"-g", other}},
		{name: "numeric operand", args: []string{"0"}},
		{name: "numeric operand with leading zeros", args: []string{"0000000000000000000"}},
		{name: "numeric operand with a plus", args: []string{"+0"}},
		{name: "numeric operand with leading blanks", args: []string{"  0"}},
		{name: "numeric operand in hex is a name", args: []string{"0x0"}},
		{name: "numeric operand with a trailing blank", args: []string{"0 "}},
		{name: "numeric operand with a trailing letter", args: []string{"0a"}},
		{name: "the reserved uid", args: []string{"4294967295"}},
		{name: "past the reserved uid", args: []string{"4294967296"}},
		{name: "a uid no entry claims", args: []string{"99999"}},
		{name: "no such user", args: []string{"nosuchuser"}},
		{name: "no such user with -u", args: []string{"-u", "nosuchuser"}},
		{name: "empty operand", args: []string{""}},
		{name: "blank operand", args: []string{" "}},
		{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}},
		{name: "operand with a colon", args: []string{"a:b"}},
		{name: "operand with a trailing space", args: []string{"root "}},
		{name: "good then bad operand", args: []string{other, "nosuchuser"}},
		{name: "bad then good operand", args: []string{"nosuchuser", other}},
		{name: "bad operand between two good", args: []string{other, "nosuchuser", other}},
		{name: "operand after dashdash", args: []string{"--", other}},
		{name: "dashdash alone", args: []string{"--"}},
		{name: "negative-looking operand after dashdash", args: []string{"--", "-0"}},

		// The option combinations id refuses.
		{name: "name in default format", args: []string{"-n"}},
		{name: "real in default format", args: []string{"-r"}},
		{name: "name and real in default format", args: []string{"-nr"}},
		{name: "zero in default format", args: []string{"-z"}},
		{name: "zero and name in default format", args: []string{"-zn"}},
		{name: "two only choices", args: []string{"-u", "-g"}},
		{name: "two only choices glued", args: []string{"-ug"}},
		{name: "user and groups", args: []string{"-uG"}},
		{name: "the same only choice twice", args: []string{"-uu"}},
		{name: "context on a kernel without SELinux", args: []string{"-Z"}},
		{name: "context and user", args: []string{"-Zu"}},
		{name: "context with an operand", args: []string{"-Z", other}},
		{name: "long context", args: []string{"--context"}},

		// getopt faults, including the ambiguity id's own options make.
		{name: "invalid short option", args: []string{"-x"}},
		{name: "digit option", args: []string{"-1"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "ambiguous long option", args: []string{"--g"}},
		{name: "ambiguous long option two letters", args: []string{"--gr"}},
		{name: "unambiguous long prefix", args: []string{"--na", "--us"}},
		{name: "empty long option is ambiguous", args: []string{"--=x"}},
		{name: "long option with a value it does not take", args: []string{"--user=x"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "version with a value", args: []string{"--version=1"}},
		{name: "bad option before help", args: []string{"--foo", "--help"}},
		{name: "operand before an option is permuted", args: []string{other, "-u"}},
		{name: "POSIXLY_CORRECT stops options at the first operand",
			args: []string{other, "-u"}, env: []string{"POSIXLY_CORRECT=1"}},

		// The write-failure paths.
		{name: "stdout closed", stdout: stdoutClosed},
		{name: "stdout full", stdout: stdoutFull},
		{name: "stdout closed with -u", args: []string{"-u"}, stdout: stdoutClosed},
		{name: "stdout closed on a fault", args: []string{"-n"}, stdout: stdoutClosed},
		{name: "stdout full with a bad operand", args: []string{"nosuchuser"}, stdout: stdoutFull},
	}
}

func TestIdParity(t *testing.T) {
	requireParity(t, "id", idCases(t))
}

func TestIdHelpVersion(t *testing.T) {
	requireHelp(t, "id", []string{"--help"}, 0)
	requireHelp(t, "id", []string{"--hel"}, 0)
	requireHelp(t, "id", []string{"--help", "x"}, 0)
	requireHelp(t, "id", []string{"x", "--help"}, 0)
	requireVersion(t, "id", []string{"--version"}, 0)
	requireVersion(t, "id", []string{"--vers"}, 0)
	requireVersion(t, "id", []string{"--version", "x"}, 0)
}
