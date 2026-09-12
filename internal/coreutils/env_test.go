package coreutils

import (
	"testing"
)

// env(1) is the first utility here that EXECS, and the first whose whole
// output is the environment, so the harness's fixed `baseEnv` does double duty:
// it is the determinism that makes the dump comparable at all. Both sides see
// exactly `LC_ALL=C LANG=C TZ=UTC PATH=/usr/bin:/bin`, in that order, so `env`
// with no COMMAND prints four known lines rather than the session's sixty.
//
// Four things about it are worth knowing before reading the cases.
//
//   - **The action order is not the command-line order.** Clear (`-i`), unset
//     (`-u`), set (`NAME=VALUE`), the signal options, `-C`, then exec. `-v`
//     makes it visible, which is why so many cases below carry `-v`: it is the
//     only way to assert the order rather than just the outcome.
//   - **`-C` lands before the PATH search.** `env -C DIR cmd` resolves `cmd`
//     from DIR, so a relative PATH entry means something different after it.
//   - **`-S` is spliced into argv and re-parsed**, so options inside the string
//     are real options and `-S'A=1 echo hi'` really does set A.
//   - **`-i` suppresses `-u` entirely** — there is nothing to unset — in either
//     option order.
//
// The `-S` grammar is the substance and it is bigger than it looks. The escape
// set is `\\ \# \$ \" \' \_ \c \f \n \r \t \v` and NOTHING else: `\a`, `\b`,
// `\e` and `\0` are all `invalid sequence`. `\_` separates words outside quotes
// and is a literal space inside them; `\c` truncates the rest of the string;
// `#` opens a comment only at the start of a token; an unquoted `$A` is an
// error that echoes the whole remainder; and `${UNSET}` makes its token
// DISAPPEAR while `""` leaves an empty one behind.
//
// Signal numbers are the host kernel's and the two kernels disagree about most
// of them, so the cases below name signals rather than numbering them, except
// where the number is the point.

func init() {
	registerCorpus("env", envCases)
}

// The `-S` strings, kept apart because there are so many of them and because
// each is one measured rule.
var envSplitStrings = []struct {
	name string
	s    string
}{
	{"plain", "/bin/echo hi"},
	{"runs of whitespace collapse", "   /bin/echo   a    b "},
	{"tab separates", "/bin/echo\ta\tb"},
	{"escape backslash", `/bin/echo x\\y`},
	{"escape hash", `/bin/echo x\#y`},
	{"escape dollar", `/bin/echo x\$y`},
	{"escape double quote", `/bin/echo x\"y`},
	{"escape single quote", `/bin/echo x\'y`},
	{"escape underscore separates", `/bin/echo a\_b`},
	{"escape underscore in double quotes is a space", `/bin/echo "a\_b"`},
	{"escape c truncates", `/bin/echo a\cb c`},
	{"escape c alone drops the rest", `/bin/echo \ca b`},
	{"escape f", `/bin/echo x\fy`},
	{"escape n", `/bin/echo x\ny`},
	{"escape r", `/bin/echo x\ry`},
	{"escape t", `/bin/echo x\ty`},
	{"escape v", `/bin/echo x\vy`},
	{"escape a is refused", `/bin/echo x\ay`},
	{"escape b is refused", `/bin/echo x\by`},
	{"escape e is refused", `/bin/echo x\ey`},
	{"escape zero is refused", `/bin/echo x\0y`},
	{"escape z is refused", `/bin/echo x\zy`},
	{"trailing backslash is refused", `/bin/echo x\`},
	{"trailing backslash after a space", `/bin/echo x \`},
	{"hash at token start is a comment", "/bin/echo a #comment here"},
	{"hash alone is a comment", "/bin/echo a #"},
	{"hash mid word is literal", "/bin/echo a#b"},
	{"hash in double quotes is literal", `/bin/echo "a#b"`},
	{"hash in single quotes is literal", `/bin/echo 'a#b'`},
	{"hash after a quote is literal", `/bin/echo 'a'#b`},
	{"hash after empty quotes is literal", `/bin/echo ''#b`},
	{"escaped hash is literal", `/bin/echo \#a b`},
	{"bare dollar is refused", "/bin/echo $A"},
	{"bare dollar echoes the remainder", "/bin/echo $A more text"},
	{"dollar at the end is refused", "/bin/echo abc$"},
	{"unclosed brace is refused", "/bin/echo ${A"},
	{"empty brace is refused", "/bin/echo ${}"},
	{"brace expands a set name", "/bin/echo [${LANG}]"},
	{"brace in double quotes expands", `/bin/echo "[${LANG}]"`},
	{"brace in single quotes is literal", `/bin/echo '${LANG}'`},
	{"unset brace disappears", "/bin/echo x ${NOPE9090} y"},
	{"unset brace at the end", "/bin/echo x ${NOPE9090}"},
	{"unset brace glued to text survives", "/bin/echo x a${NOPE9090}b y"},
	{"empty quotes survive", `/bin/echo x "" y`},
	{"quoted unset brace survives", `/bin/echo x "${NOPE9090}" y`},
	{"single quotes hold spaces", `/bin/echo 'a b' c`},
	{"double quotes hold spaces", `/bin/echo "a b" c`},
	{"adjacent quotes concatenate", `/bin/echo 'a'"b"c`},
	{"single quote inside double", `/bin/echo "a'b"`},
	{"double quote inside single", `/bin/echo 'a"b'`},
	{"unterminated single quote", `/bin/echo 'abc`},
	{"unterminated double quote", `/bin/echo "abc`},
	{"an option inside the string", "-i /bin/echo hi"},
	{"an assignment inside the string", "A=1 /bin/echo hi"},
	{"empty string yields no command", ""},
	{"only whitespace yields no command", "   "},
}

func envCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

	// ---- the bare forms ----
	add(invocation{name: "no arguments dumps the environment"})
	add(invocation{name: "dashdash alone still dumps", args: []string{"--"}})
	add(invocation{name: "a command with no changes", args: []string{"/bin/echo", "hi"}})
	add(invocation{name: "help with a value", args: []string{"--help=x"}})
	add(invocation{name: "version with a value", args: []string{"--version=1"}})

	// ---- -i and the bare dash ----
	add(invocation{name: "ignore environment", args: []string{"-i"}})
	add(invocation{name: "ignore environment long", args: []string{"--ignore-environment"}})
	add(invocation{name: "bare dash implies -i", args: []string{"-"}})
	add(invocation{name: "ignore then assign", args: []string{"-i", "A=1", "B=2"}})
	add(invocation{name: "bare dash then assign", args: []string{"-", "A=1"}})
	add(invocation{name: "dash is only the first operand", args: []string{"Q=1", "-", "R=2"}})
	add(invocation{name: "dash does not re-enable options", args: []string{"-", "-u", "A"}})
	add(invocation{name: "ignore with a command", args: []string{"-i", "/bin/echo", "hi"}})
	add(invocation{name: "ignore twice", args: []string{"-i", "-i"}})
	add(invocation{name: "dash after dashdash", args: []string{"--", "-"}})

	// ---- -u ----
	add(invocation{name: "unset an existing name", args: []string{"-u", "LANG"}})
	add(invocation{name: "unset glued", args: []string{"-uLANG"}})
	add(invocation{name: "unset long", args: []string{"--unset=LANG"}})
	add(invocation{name: "unset long separate", args: []string{"--unset", "LANG"}})
	add(invocation{name: "unset a missing name", args: []string{"-u", "NOPE9090"}})
	add(invocation{name: "unset twice", args: []string{"-u", "LANG", "-u", "TZ"}})
	add(invocation{name: "unset the same name twice", args: []string{"-u", "LANG", "-u", "LANG"}})
	add(invocation{name: "unset an empty name", args: []string{"-u", "", "/bin/true"}})
	add(invocation{name: "unset a name holding equals", args: []string{"-u", "A=B", "/bin/true"}})
	add(invocation{name: "unset just an equals", args: []string{"-u", "=", "/bin/true"}})
	add(invocation{name: "unset a name ending in equals", args: []string{"-u", "FOO=", "/bin/true"}})
	add(invocation{name: "unset a name with a space is allowed", args: []string{"-u", "a b"}})
	add(invocation{name: "unset outranks the command", args: []string{"-u", "", "/bin/echo", "hi"}})
	add(invocation{name: "unset then set the same name", args: []string{"-u", "LANG", "LANG=new"}})

	// ---- -0 ----
	add(invocation{name: "null separated dump", args: []string{"-0"}})
	add(invocation{name: "null separated long", args: []string{"--null"}})
	add(invocation{name: "null with -i", args: []string{"-0", "-i", "A=1"}})
	add(invocation{name: "null with an empty environment", args: []string{"-0", "-i"}})
	add(invocation{name: "null with a command is refused", args: []string{"-0", "/bin/true"}})
	add(invocation{name: "null refusal beats not found", args: []string{"-0", "/no/such/cmd9090"}})
	add(invocation{name: "null twice", args: []string{"-0", "-0"}})

	// ---- -C ----
	add(invocation{name: "chdir and run", args: []string{"-C", "/", "/bin/echo", "moved"}})
	add(invocation{name: "chdir long", args: []string{"--chdir=/", "/bin/echo", "moved"}})
	add(invocation{name: "chdir to a missing directory", args: []string{"-C", "/no/such/dir9090", "/bin/true"}})
	add(invocation{name: "chdir to a regular file", args: []string{"-C", "/etc/hostname", "/bin/true"}})
	add(invocation{name: "chdir to an empty path", args: []string{"-C", "", "/bin/true"}})
	add(invocation{name: "chdir without a command", args: []string{"-C", "/"}})
	add(invocation{name: "chdir without a command outranks a bad directory", args: []string{"-C", "/no/such/dir9090"}})
	add(invocation{name: "last chdir wins", args: []string{"-C", "/no/such/dir9090", "-C", "/", "/bin/echo", "ok"}})

	// ---- -a / --argv0 is NOT ours to have ----
	// It arrived in coreutils 9.5 and docs/COREUTILS.md holds this corpus to
	// 9.4, which rejects it. Matching the reference means rejecting it too, so
	// these cases pin the refusal rather than the feature.
	add(invocation{name: "argv0 short is not an option", args: []string{"-a", "ZERO", "/bin/echo", "a"}})
	add(invocation{name: "argv0 long is not an option", args: []string{"--argv0=ZERO", "/bin/echo", "a"}})

	// ---- NAME=VALUE operands ----
	add(invocation{name: "one assignment", args: []string{"FOO=bar"}})
	add(invocation{name: "empty value", args: []string{"FOO="}})
	add(invocation{name: "a name that is not one", args: []string{"1FOO=bar"}})
	add(invocation{name: "a name holding a dash", args: []string{"FO-O=bar"}})
	add(invocation{name: "a name holding a space", args: []string{"FO O=bar"}})
	add(invocation{name: "an empty name", args: []string{"=bar"}})
	add(invocation{name: "just an equals", args: []string{"="}})
	add(invocation{name: "the first equals splits", args: []string{"FOO=a=b"}})
	add(invocation{name: "a duplicate assignment keeps the last", args: []string{"-i", "A=1st", "A=2nd"}})
	add(invocation{name: "overwriting an inherited name keeps its slot", args: []string{"LANG=changed"}})
	add(invocation{name: "assignment then command", args: []string{"FOO=bar", "/bin/echo", "hi"}})
	add(invocation{name: "an operand after the command is an argument", args: []string{"/bin/echo", "FOO=bar"}})
	add(invocation{name: "dashdash does not stop assignments", args: []string{"--", "FOO=bar"}})
	add(invocation{name: "an option after an operand is not an option", args: []string{"A=2", "-u", "A"}})

	// ---- exit statuses ----
	add(invocation{name: "command not found", args: []string{"/no/such/cmd9090"}})
	add(invocation{name: "command not found via PATH", args: []string{"nosuchcmd9090"}})
	add(invocation{name: "an empty command", args: []string{""}})
	add(invocation{name: "a directory as the command", args: []string{"/tmp"}})
	add(invocation{name: "a non-executable file", args: []string{"/etc/hostname"}})
	add(invocation{name: "a command name holding a space", args: []string{"a b"}})
	add(invocation{name: "a command name holding a tab", args: []string{"a\tb"}})
	add(invocation{name: "a dash command", args: []string{"--", "-i"}})
	add(invocation{name: "the command's own status passes through", args: []string{"/bin/false"}})

	// ---- the signal options ----
	add(invocation{name: "block a signal", args: []string{"--block-signal=INT", "/bin/true"}})
	add(invocation{name: "block lower case", args: []string{"--block-signal=int", "/bin/true"}})
	add(invocation{name: "block with the SIG prefix", args: []string{"--block-signal=SIGINT", "/bin/true"}})
	add(invocation{name: "block a number", args: []string{"--block-signal=2", "/bin/true"}})
	add(invocation{name: "block a list", args: []string{"--block-signal=INT,TERM", "/bin/true"}})
	add(invocation{name: "block a list with an empty element", args: []string{"--block-signal=INT,,TERM", "/bin/true"}})
	add(invocation{name: "block an empty list is a no-op", args: []string{"--block-signal=", "/bin/true"}})
	add(invocation{name: "block an invalid name", args: []string{"--block-signal=NOPE", "/bin/true"}})
	add(invocation{name: "block signal zero", args: []string{"--block-signal=0", "/bin/true"}})
	add(invocation{name: "block a whitespace name", args: []string{"--block-signal= INT ", "/bin/true"}})
	add(invocation{name: "block KILL is silently allowed", args: []string{"--block-signal=KILL", "/bin/true"}})
	add(invocation{name: "ignore KILL is refused", args: []string{"--ignore-signal=KILL", "/bin/true"}})
	add(invocation{name: "a separate argument is not consumed", args: []string{"--block-signal", "INT", "/bin/true"}})
	add(invocation{name: "signal options need a command", args: []string{"--block-signal=INT"}})
	add(invocation{name: "default a signal", args: []string{"--default-signal=INT", "/bin/true"}})
	add(invocation{name: "ignore a signal", args: []string{"--ignore-signal=INT", "/bin/true"}})

	// ---- --list-signal-handling ----
	add(invocation{name: "list with nothing changed", args: []string{"--list-signal-handling", "/bin/true"}})
	add(invocation{name: "list an ignored signal", args: []string{"--ignore-signal=INT", "--list-signal-handling", "/bin/true"}})
	add(invocation{name: "list a blocked signal", args: []string{"--block-signal=INT", "--list-signal-handling", "/bin/true"}})
	add(invocation{name: "list is order independent", args: []string{"--list-signal-handling", "--block-signal=INT", "/bin/true"}})
	add(invocation{name: "list several", args: []string{"--block-signal=INT,QUIT", "--ignore-signal=TERM", "--list-signal-handling", "/bin/true"}})
	add(invocation{name: "list without a command", args: []string{"--list-signal-handling"}})
	add(invocation{name: "list takes no argument", args: []string{"--list-signal-handling=X", "/bin/true"}})

	// ---- -v, which is the only way to assert the ORDER ----
	add(invocation{name: "debug a plain run", args: []string{"-v", "/bin/true"}})
	add(invocation{name: "debug with arguments", args: []string{"-v", "/bin/echo", "a", "b"}})
	add(invocation{name: "debug with no command", args: []string{"-v"}})
	add(invocation{name: "debug an assignment", args: []string{"-v", "A=1", "/bin/true"}})
	add(invocation{name: "debug -i", args: []string{"-v", "-i", "/bin/true"}})
	add(invocation{name: "debug -u", args: []string{"-v", "-u", "LANG", "/bin/true"}})
	add(invocation{name: "debug -u on a missing name", args: []string{"-v", "-u", "NOPE9090", "/bin/true"}})
	add(invocation{name: "debug -i suppresses -u", args: []string{"-v", "-i", "-u", "LANG", "/bin/true"}})
	add(invocation{name: "debug -u before -i suppresses it too", args: []string{"-v", "-u", "LANG", "-i", "/bin/true"}})
	add(invocation{name: "debug -C", args: []string{"-v", "-C", "/", "/bin/true"}})
	add(invocation{name: "debug the whole order", args: []string{"-v", "-i", "-u", "LANG", "-C", "/", "--block-signal=INT", "B=2", "/bin/true"}})
	add(invocation{name: "debug a missing command", args: []string{"-v", "/no/such/cmd9090"}})
	add(invocation{name: "debug long", args: []string{"--debug", "/bin/true"}})
	add(invocation{name: "debug twice", args: []string{"-v", "-v", "/bin/true"}})

	// ---- getopt faults ----
	add(invocation{name: "unknown short option", args: []string{"-Q", "/bin/true"}})
	add(invocation{name: "unknown short in a cluster", args: []string{"-iQ", "/bin/true"}})
	add(invocation{name: "unknown long option", args: []string{"--bogus", "/bin/true"}})
	add(invocation{name: "unknown long with a value", args: []string{"--bogus=1", "/bin/true"}})
	add(invocation{name: "short option missing its argument", args: []string{"-u"}})
	add(invocation{name: "chdir missing its argument", args: []string{"-C"}})
	add(invocation{name: "split missing its argument", args: []string{"-S"}})
	add(invocation{name: "long option missing its argument", args: []string{"--unset"}})
	add(invocation{name: "long chdir missing its argument", args: []string{"--chdir"}})
	add(invocation{name: "a flag given an argument", args: []string{"--null=x"}})
	add(invocation{name: "debug given an argument", args: []string{"--debug=x"}})
	add(invocation{name: "ambiguous i", args: []string{"--i", "/bin/true"}})
	add(invocation{name: "ambiguous ign", args: []string{"--ign", "/bin/true"}})
	add(invocation{name: "ambiguous ignore", args: []string{"--ignore", "/bin/true"}})
	add(invocation{name: "ambiguous de", args: []string{"--de", "/bin/true"}})
	add(invocation{name: "unambiguous abbreviation s", args: []string{"--s"}})
	add(invocation{name: "unambiguous abbreviation n", args: []string{"--n"}})
	add(invocation{name: "unambiguous abbreviation ignore-e", args: []string{"--ignore-e"}})
	add(invocation{name: "unambiguous abbreviation ignore-si", args: []string{"--ignore-si=INT", "/bin/true"}})

	// ---- options after the command are the command's ----
	add(invocation{name: "options pass through", args: []string{"/bin/echo", "-i", "-u", "X", "--help"}})

	// ---- -S ----
	for _, sp := range envSplitStrings {
		add(invocation{name: "split " + sp.name, args: []string{"-S" + sp.s}})
		add(invocation{name: "split long " + sp.name, args: []string{"--split-string=" + sp.s}})
	}
	add(invocation{name: "split as a separate argument", args: []string{"-S", "/bin/echo hi"}})
	add(invocation{name: "split with -i", args: []string{"-i", "-S/bin/echo hi"}})
	add(invocation{name: "split with -v", args: []string{"-v", "-S/bin/echo hi"}})
	add(invocation{name: "split expansion reads the pre -i environment", args: []string{"-v", "-i", "-S/bin/echo [${LANG}]"}})
	add(invocation{name: "split expansion is not the assignment in the same string", args: []string{"-S", "LANG=NEW /bin/echo [${LANG}]"}})
	add(invocation{name: "arguments after the split string are appended", args: []string{"-S/bin/echo a", "b", "c"}})
	add(invocation{name: "a second split string is a literal argument", args: []string{"-S/bin/echo a", "-Sb c"}})
	add(invocation{name: "split after an operand is not an option", args: []string{"FOO=bar", "-S/bin/echo a"}})
	add(invocation{name: "split in a cluster", args: []string{"-vS/bin/echo hi"}})
	add(invocation{name: "an empty first token is an empty command", args: []string{"-S", "'' /bin/echo"}})

	return cases
}

func TestEnv(t *testing.T) {
	requireParity(t, "env", envCases(t))
}

// The two options whose TEXT is ours by design (docs/COREUTILS.md). Everything
// else about them still has to match, including that `--help` beats a later
// usage error and loses to an earlier one.
func TestEnvHelpVersion(t *testing.T) {
	requireHelp(t, "env", []string{"--help"}, 0)
	requireHelp(t, "env", []string{"--h"}, 0)
	requireHelp(t, "env", []string{"--help", "extra"}, 0)
	requireHelp(t, "env", []string{"-u", "", "--help"}, 0)
	requireVersion(t, "env", []string{"--version"}, 0)
	requireVersion(t, "env", []string{"--v"}, 0)
	requireVersion(t, "env", []string{"--version", "x"}, 0)
}
