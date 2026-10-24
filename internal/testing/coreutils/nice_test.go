package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func init() {
	registerCorpus("nice", niceCases)
}

func niceProbe(t *testing.T) string {
	t.Helper()
	return referenceBin(t, "nice")
}

func niceCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }
	probe := niceProbe(t)

	add(invocation{name: "no arguments prints the niceness"})
	add(invocation{name: "dashdash alone still prints", args: []string{"--"}})

	add(invocation{name: "the adjustment adds to a raised niceness", args: []string{"-n", "4", probe, "-n", "5", probe}})
	add(invocation{name: "lowering from a raised niceness", args: []string{"-n", "8", probe, "-n", "-3", probe}})

	add(invocation{name: "an adjustment and a command", args: []string{"-n", "5", probe}})
	add(invocation{name: "the adjustment glued to the option", args: []string{"-n5", probe}})
	add(invocation{name: "the long form", args: []string{"--adjustment=5", probe}})
	add(invocation{name: "the long form by prefix", args: []string{"--adjust=5", probe}})
	add(invocation{name: "the long form with a separate value", args: []string{"--ad", "4", probe}})
	add(invocation{name: "a plus sign is allowed", args: []string{"-n", "+5", probe}})
	add(invocation{name: "leading zeros are decimal, not octal", args: []string{"-n", "007", probe}})
	add(invocation{name: "the default adjustment is 10", args: []string{probe}})

	add(invocation{name: "a bare number is an adjustment", args: []string{"-5", probe}})
	add(invocation{name: "a bare zero is an adjustment", args: []string{"-0", probe}})
	add(invocation{name: "two zeros are still one adjustment", args: []string{"-00", probe}})
	add(invocation{name: "a double dash before digits is negative", args: []string{"--5", probe}})
	add(invocation{name: "a double dash before one digit is negative", args: []string{"--9", probe}})
	add(invocation{name: "a plus inside the obsolescent form", args: []string{"-+5", probe}})
	add(invocation{name: "a plus with no digits is an invalid option", args: []string{"-+"}})
	add(invocation{name: "a plus after two dashes is a long option", args: []string{"--+5"}})
	add(invocation{name: "three dashes is a long option", args: []string{"---5"}})
	add(invocation{name: "a letter after two dashes is a long option", args: []string{"--x"}})
	add(invocation{name: "trailing junk in the obsolescent form", args: []string{"-5x", probe}})
	add(invocation{name: "a second sign in the obsolescent form", args: []string{"-1-2", probe}})
	add(invocation{name: "an option letter after the digits", args: []string{"-5n3", probe}})

	add(invocation{name: "the last -n wins", args: []string{"-n", "7", "-n", "3", probe}})
	add(invocation{name: "a bare number after -n wins", args: []string{"-n", "3", "-5", probe}})
	add(invocation{name: "-n after a bare number wins", args: []string{"-5", "-n", "3", probe}})
	add(invocation{name: "the long form and -n together", args: []string{"--adjustment=5", "-n", "3", probe}})

	add(invocation{name: "past the top of the range", args: []string{"-n", "100", probe}})
	add(invocation{name: "past the bottom of the range", args: []string{"-n", "-100", probe}})
	add(invocation{name: "the top of the range exactly", args: []string{"-n", "19", probe}})
	add(invocation{name: "one past the top of the range", args: []string{"-n", "20", probe}})
	add(invocation{name: "the bottom of the range exactly", args: []string{"-n", "-20", probe}})
	add(invocation{name: "int max saturates", args: []string{"-n", "2147483647", probe}})
	add(invocation{name: "int min saturates", args: []string{"-n", "-2147483648", probe}})
	add(invocation{name: "past the width saturates", args: []string{"-n", "99999999999999999999", probe}})
	add(invocation{name: "past the width saturates negative", args: []string{"-n", "-99999999999999999999", probe}})

	add(invocation{name: "lowering the niceness", args: []string{"-n", "-3", probe}})

	add(invocation{name: "a word is not an adjustment", args: []string{"-n", "abc", "/bin/true"}})
	add(invocation{name: "an empty adjustment", args: []string{"-n", "", "/bin/true"}})
	add(invocation{name: "an empty long adjustment", args: []string{"--adjustment="}})
	add(invocation{name: "hexadecimal is not accepted", args: []string{"-n", "0x5", probe}})
	add(invocation{name: "trailing junk after the digits", args: []string{"-n", "5x", probe}})
	add(invocation{name: "a leading blank is allowed", args: []string{"-n", " 5", probe}})
	add(invocation{name: "a trailing blank is not", args: []string{"-n", "5 ", "/bin/true"}})
	add(invocation{name: "blanks around a negative", args: []string{"-n", "  -3  ", probe}})
	add(invocation{name: "the option letter repeated in the value", args: []string{"-nn5", probe}})
	add(invocation{name: "a letter as the value", args: []string{"-nx"}})
	add(invocation{name: "dashdash as the adjustment", args: []string{"-n", "--", "5"}})

	add(invocation{name: "an adjustment with no command", args: []string{"-n", "5"}})
	add(invocation{name: "an adjustment then dashdash", args: []string{"-n", "5", "--"}})
	add(invocation{name: "a bare number then dashdash", args: []string{"-5", "--"}})
	add(invocation{name: "a long adjustment then dashdash", args: []string{"--adjustment=5", "--"}})

	add(invocation{name: "an unknown option", args: []string{"-z"}})
	add(invocation{name: "an unknown option after a good one", args: []string{"-n", "5", "-x", probe}})
	add(invocation{name: "an unknown option before a good one", args: []string{"-x", "-n", "5", probe}})
	add(invocation{name: "the long option with no value", args: []string{"--adjustment"}})
	add(invocation{name: "the short option with no value", args: []string{"-n"}})
	add(invocation{name: "help with a value", args: []string{"--help=x"}})
	add(invocation{name: "version with a value", args: []string{"--version=1"}})

	add(invocation{name: "a command found on PATH", args: []string{"echo", "hi"}})
	add(invocation{name: "a command not found", args: []string{"nosuchcmd9090"}})
	add(invocation{name: "an empty command", args: []string{""}})
	add(invocation{name: "a bare dash is a command", args: []string{"-"}})
	add(invocation{name: "a number after dashdash is a command", args: []string{"--", "-5"}})
	add(invocation{name: "dashdash before the command", args: []string{"-n", "5", "--", probe}})
	add(invocation{name: "a data file is not executable", args: []string{"/etc/passwd"}})
	add(invocation{name: "a directory is not executable", args: []string{"/tmp"}})
	add(invocation{name: "options after the command are the command's", args: []string{"/bin/echo", "-n", "hi"}})
	add(invocation{name: "an adjustment then the command's own options", args: []string{"-n", "5", "/bin/echo", "-n", "hi"}})
	add(invocation{name: "a bare number after the command is the command's", args: []string{"/bin/echo", "-5"}})

	add(invocation{name: "stdout full", stdout: stdoutFull})
	add(invocation{name: "stdout closed", stdout: stdoutClosed})
	add(invocation{name: "stdout full with a command", args: []string{"/bin/true"}, stdout: stdoutFull})

	execTree := niceExecTree(t)
	add(invocation{name: "a script with no shebang runs under the shell", dir: execTree, args: []string{"./noshebang"}})
	add(invocation{name: "the shell sees the resolved path and the arguments", dir: execTree, args: []string{"-n", "5", "./showargs", "a", "b"}})
	add(invocation{name: "a script without the execute bit is refused", dir: execTree, args: []string{"./unreadable"}})

	return cases
}

func niceExecTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("noshebang", "echo from-script\n", 0o755)
	write("showargs", "echo \"dollar0=[$0] count=$# args=[$*]\"\n", 0o755)
	write("unreadable", "echo unreachable\n", 0o644)
	return dir
}

func TestNiceReadsTheNicenessItWasStartedAt(t *testing.T) {
	ref := referenceBin(t, "nice")
	run := func(adj int, inner string) string {
		cmd := exec.Command(ref, "-n", strconv.Itoa(adj), inner)
		cmd.Env = baseEnv()
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("nice -n %d %s: %v", adj, inner, err)
		}
		return strings.TrimRight(string(out), "\n")
	}
	for _, adj := range []int{5, 12, 19} {
		if got, want := run(adj, fernBin(t, "nice")), run(adj, ref); got != want {
			t.Errorf("nice printed %q under nice -n %d, GNU printed %q", got, adj, want)
		}
	}
}

func TestNice(t *testing.T) {
	requireParity(t, "nice", niceCases(t))
}

func TestNiceHelpVersion(t *testing.T) {
	requireHelp(t, "nice", []string{"--help"}, 0)
	requireHelp(t, "nice", []string{"--h"}, 0)
	requireHelp(t, "nice", []string{"--help", "extra"}, 0)
	requireHelp(t, "nice", []string{"-n", "5", "--help"}, 0)
	requireVersion(t, "nice", []string{"--version"}, 0)
	requireVersion(t, "nice", []string{"--v"}, 0)
	requireVersion(t, "nice", []string{"--version", "x"}, 0)
}
