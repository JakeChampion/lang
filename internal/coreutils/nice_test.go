package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// nice(1) has one option and a great deal of grammar around it.
//
// Four things are worth knowing before reading the cases.
//
//   - **The last adjustment wins.** `-n 7 -n 3` is 3 and `-5 -n 3` is 3.
//     GNU's documentation reads as though they accumulate; measured, they do
//     not.
//   - **`-N` is an obsolescent spelling handled BEFORE getopt**, and the
//     boundary is narrow: `--5` is an adjustment of -5 while `--x` is an
//     unrecognized long option, `-+5` is 5 while `-+` alone is an invalid
//     option, and `--adjustment=5` is of course the long option. The trigger
//     is a digit after the optional sign, and the value is everything after
//     the FIRST dash — which is why `--5` parses the string `-5`.
//   - **The sum is CLAMPED, not refused.** `-n 2147483647` is 19 and
//     `-n -99999999999999999999` is -20, with no diagnostic: strtol
//     saturates and nothing reads its ERANGE.
//   - **An adjustment with no command is an error, no adjustment with no
//     command is the current niceness.** `nice` and `nice --` both print a
//     number; `nice -n 5` and `nice -n 5 --` are both `a command must be
//     given with an adjustment`, with the `Try …` line — while `invalid
//     adjustment` has no such line, because gnulib reports it as a bad
//     number rather than a bad option.
//
// The `-n -3` case is the renice FAILURE path, and which branch it takes is
// the environment's: lowering the value needs privilege, so an unprivileged
// runner gets `cannot set niceness: Permission denied` and the command runs
// anyway, while a root one simply succeeds. Both sides meet the same
// environment, so the case gates whichever answer it is — and on CI, where
// the runner is unprivileged, it gates the warning's wording.

func init() {
	registerCorpus("nice", niceCases)
}

// niceProbe is the command the cases run: GNU `nice` with no arguments,
// which prints the niceness it was started at. Using the reference binary on
// both sides keeps the only difference the OUTER nice, which is what is
// under test.
func niceProbe(t *testing.T) string {
	t.Helper()
	return referenceBin(t, "nice")
}

func niceCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }
	probe := niceProbe(t)

	// ---- reading the niceness ----
	add(invocation{name: "no arguments prints the niceness"})
	add(invocation{name: "dashdash alone still prints", args: []string{"--"}})

	// A nonzero STARTING niceness cannot come from the harness: setting
	// one means moving process-global (and on Linux per-thread) state that
	// an unprivileged runner cannot move back, so it races the other cases
	// and ratchets. It comes from a wrapper instead — GNU `nice` raising
	// the value for a child, which needs no privilege — and the cases that
	// need it are the two below plus
	// TestNiceReadsTheNicenessItWasStartedAt.
	add(invocation{name: "the adjustment adds to a raised niceness", args: []string{"-n", "4", probe, "-n", "5", probe}})
	add(invocation{name: "lowering from a raised niceness", args: []string{"-n", "8", probe, "-n", "-3", probe}})

	// ---- -n ----
	add(invocation{name: "an adjustment and a command", args: []string{"-n", "5", probe}})
	add(invocation{name: "the adjustment glued to the option", args: []string{"-n5", probe}})
	add(invocation{name: "the long form", args: []string{"--adjustment=5", probe}})
	add(invocation{name: "the long form by prefix", args: []string{"--adjust=5", probe}})
	add(invocation{name: "the long form with a separate value", args: []string{"--ad", "4", probe}})
	add(invocation{name: "a plus sign is allowed", args: []string{"-n", "+5", probe}})
	add(invocation{name: "leading zeros are decimal, not octal", args: []string{"-n", "007", probe}})
	add(invocation{name: "the default adjustment is 10", args: []string{probe}})

	// ---- the obsolescent -N form ----
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

	// ---- the last one wins ----
	add(invocation{name: "the last -n wins", args: []string{"-n", "7", "-n", "3", probe}})
	add(invocation{name: "a bare number after -n wins", args: []string{"-n", "3", "-5", probe}})
	add(invocation{name: "-n after a bare number wins", args: []string{"-5", "-n", "3", probe}})
	add(invocation{name: "the long form and -n together", args: []string{"--adjustment=5", "-n", "3", probe}})

	// ---- clamping and saturation ----
	add(invocation{name: "past the top of the range", args: []string{"-n", "100", probe}})
	add(invocation{name: "past the bottom of the range", args: []string{"-n", "-100", probe}})
	add(invocation{name: "the top of the range exactly", args: []string{"-n", "19", probe}})
	add(invocation{name: "one past the top of the range", args: []string{"-n", "20", probe}})
	add(invocation{name: "the bottom of the range exactly", args: []string{"-n", "-20", probe}})
	add(invocation{name: "int max saturates", args: []string{"-n", "2147483647", probe}})
	add(invocation{name: "int min saturates", args: []string{"-n", "-2147483648", probe}})
	add(invocation{name: "past the width saturates", args: []string{"-n", "99999999999999999999", probe}})
	add(invocation{name: "past the width saturates negative", args: []string{"-n", "-99999999999999999999", probe}})

	// ---- lowering, which needs privilege ----
	add(invocation{name: "lowering the niceness", args: []string{"-n", "-3", probe}})

	// ---- the adjustment grammar's refusals ----
	add(invocation{name: "a word is not an adjustment", args: []string{"-n", "abc", "/bin/true"}})
	add(invocation{name: "an empty adjustment", args: []string{"-n", "", "/bin/true"}})
	add(invocation{name: "an empty long adjustment", args: []string{"--adjustment="}})
	add(invocation{name: "hexadecimal is not accepted", args: []string{"-n", "0x5", probe}})
	add(invocation{name: "trailing junk after the digits", args: []string{"-n", "5x", probe}})
	// strtol skips LEADING blanks and nothing consumes trailing ones.
	add(invocation{name: "a leading blank is allowed", args: []string{"-n", " 5", probe}})
	add(invocation{name: "a trailing blank is not", args: []string{"-n", "5 ", "/bin/true"}})
	add(invocation{name: "blanks around a negative", args: []string{"-n", "  -3  ", probe}})
	add(invocation{name: "the option letter repeated in the value", args: []string{"-nn5", probe}})
	add(invocation{name: "a letter as the value", args: []string{"-nx"}})
	// `--` is consumed as -n's value rather than ending the options.
	add(invocation{name: "dashdash as the adjustment", args: []string{"-n", "--", "5"}})

	// ---- an adjustment with no command ----
	add(invocation{name: "an adjustment with no command", args: []string{"-n", "5"}})
	add(invocation{name: "an adjustment then dashdash", args: []string{"-n", "5", "--"}})
	add(invocation{name: "a bare number then dashdash", args: []string{"-5", "--"}})
	add(invocation{name: "a long adjustment then dashdash", args: []string{"--adjustment=5", "--"}})

	// ---- option faults ----
	add(invocation{name: "an unknown option", args: []string{"-z"}})
	add(invocation{name: "an unknown option after a good one", args: []string{"-n", "5", "-x", probe}})
	add(invocation{name: "an unknown option before a good one", args: []string{"-x", "-n", "5", probe}})
	add(invocation{name: "the long option with no value", args: []string{"--adjustment"}})
	add(invocation{name: "the short option with no value", args: []string{"-n"}})
	add(invocation{name: "help with a value", args: []string{"--help=x"}})
	add(invocation{name: "version with a value", args: []string{"--version=1"}})

	// ---- the command, and the ways it fails to run ----
	add(invocation{name: "a command found on PATH", args: []string{"echo", "hi"}})
	add(invocation{name: "a command not found", args: []string{"nosuchcmd9090"}})
	add(invocation{name: "an empty command", args: []string{""}})
	add(invocation{name: "a bare dash is a command", args: []string{"-"}})
	add(invocation{name: "a number after dashdash is a command", args: []string{"--", "-5"}})
	add(invocation{name: "dashdash before the command", args: []string{"-n", "5", "--", probe}})
	add(invocation{name: "a data file is not executable", args: []string{"/etc/passwd"}})
	add(invocation{name: "a directory is not executable", args: []string{"/tmp"}})
	// Options after the command belong to the command.
	add(invocation{name: "options after the command are the command's", args: []string{"/bin/echo", "-n", "hi"}})
	add(invocation{name: "an adjustment then the command's own options", args: []string{"-n", "5", "/bin/echo", "-n", "hi"}})
	add(invocation{name: "a bare number after the command is the command's", args: []string{"/bin/echo", "-5"}})

	// ---- the write-failure paths ----
	//
	// gnulib's `exit_failure` is EXIT_CANCELED here rather than 1, so a
	// write that cannot land exits 125 — the same status a usage error
	// takes. Only the read path writes anything, so only it can reach
	// this.
	add(invocation{name: "stdout full", stdout: stdoutFull})
	add(invocation{name: "stdout closed", stdout: stdoutClosed})
	// A command to exec writes nothing itself, so a full stdout is the
	// COMMAND's problem and nice still execs.
	add(invocation{name: "stdout full with a command", args: []string{"/bin/true"}, stdout: stdoutFull})

	// ---- execvp's shell retry, shared with env (#9262) ----
	execTree := niceExecTree(t)
	add(invocation{name: "a script with no shebang runs under the shell", dir: execTree, args: []string{"./noshebang"}})
	add(invocation{name: "the shell sees the resolved path and the arguments", dir: execTree, args: []string{"-n", "5", "./showargs", "a", "b"}})
	add(invocation{name: "a script without the execute bit is refused", dir: execTree, args: []string{"./unreadable"}})

	return cases
}

// niceExecTree is the fixture for the shell-retry cases. Two files that
// differ only in how the kernel refuses to exec them, so the retry is
// visible without depending on anything outside the tree.
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

// The read at a NONZERO niceness, which no corpus case can reach on its own:
// two implementations agreeing at the 0 the suite inherits proves nothing,
// because that is exactly the value a broken read produces by accident.
// Linux's getpriority answers the nice value BIASED by 20, so a helper that
// forwards the syscall's answer prints 20 at an inherited 0 and 15 at 5, and
// one that applies the correction twice prints -20 and -5.
//
// The starting value comes from GNU `nice` raising it for a child. RAISING
// needs no privilege and the value dies with the child, so unlike moving the
// harness process's own niceness this neither races the other cases nor
// needs a restore that an unprivileged runner cannot perform.
func TestNiceReadsTheNicenessItWasStartedAt(t *testing.T) {
	ref := referenceBin(t, "nice")
	for _, want := range []int{5, 12, 19} {
		cmd := exec.Command(ref, "-n", strconv.Itoa(want), fernBin(t, "nice"))
		cmd.Env = baseEnv()
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("nice at niceness %d: %v", want, err)
		}
		if printed := strings.TrimRight(string(out), "\n"); printed != strconv.Itoa(want) {
			t.Errorf("nice printed %q at niceness %d, want %d", printed, want, want)
		}
	}
}

func TestNice(t *testing.T) {
	requireParity(t, "nice", niceCases(t))
}

// The two options whose TEXT is ours by design (docs/COREUTILS.md).
// Everything else about them still has to match, including that `--help`
// beats a later usage error and loses to an earlier one.
func TestNiceHelpVersion(t *testing.T) {
	requireHelp(t, "nice", []string{"--help"}, 0)
	requireHelp(t, "nice", []string{"--h"}, 0)
	requireHelp(t, "nice", []string{"--help", "extra"}, 0)
	requireHelp(t, "nice", []string{"-n", "5", "--help"}, 0)
	requireVersion(t, "nice", []string{"--version"}, 0)
	requireVersion(t, "nice", []string{"--v"}, 0)
	requireVersion(t, "nice", []string{"--version", "x"}, 0)
}
