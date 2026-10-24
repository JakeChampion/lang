package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

func init() {
	registerCorpus("timeout", timeoutCases)
}

func timeoutIgnoresTerm() []string {
	return []string{"/bin/sh", "-c", `trap "" TERM; sleep 2`}
}

const trapDeadline = "0.5"

func timeoutExecTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "unreadable"), []byte("echo unreachable\n"), 0o644); err != nil {
		t.Fatalf("write unreadable: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatalf("mkdir adir: %v", err)
	}
	return dir
}

func timeoutCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }
	sleepBin := referenceBin(t, "sleep")
	trueBin := referenceBin(t, "true")
	execDir := timeoutExecTree(t)

	add(invocation{name: "no arguments"})
	add(invocation{name: "a duration and no command", args: []string{"1"}})
	add(invocation{name: "dashdash alone", args: []string{"--"}})
	add(invocation{name: "a command that exits 0", args: []string{"5", trueBin}})
	add(invocation{name: "a command that fails", args: []string{"5", referenceBin(t, "false")}})
	add(invocation{name: "the command's own status passes through", args: []string{"5", "/bin/sh", "-c", "exit 42"}})
	add(invocation{name: "the command's arguments reach it", args: []string{"5", referenceBin(t, "echo"), "a", "b"}})
	add(invocation{name: "a command named dashdash", args: []string{"1", "--"}})
	add(invocation{name: "an empty command", args: []string{"1", ""}})
	add(invocation{name: "the duration after dashdash", args: []string{"--", "5", trueBin}})

	for _, d := range []string{"1s", "1m", "1h", "1d", "1e2", ".5", "5.", "+1", " 1", "0x1", "inf", "007", "1.5"} {
		add(invocation{name: "duration " + d, args: []string{d, trueBin}})
	}
	for _, d := range []string{"x", "1x", "", "1 ", "1M", "1S", "1.5.5", "nan", "--", "1s2", "s"} {
		add(invocation{name: "bad duration " + d, args: []string{d, trueBin}})
	}
	add(invocation{name: "past the width saturates", args: []string{"99999999999999999999", trueBin}})
	add(invocation{name: "a negative duration after dashdash", args: []string{"--", "-1", trueBin}})
	add(invocation{name: "a negative duration is an option", args: []string{"-1", trueBin}})
	add(invocation{name: "negative zero is an interval", args: []string{"--", "-0", trueBin}})

	add(invocation{name: "zero waits the command out", args: []string{"0", sleepBin, "0.2"}})
	add(invocation{name: "zero with a kill-after", args: []string{"-k", "0.2", "0", "/bin/sh", "-c", `trap "" TERM; sleep 0.2`}})

	add(invocation{name: "the command times out", args: []string{"0.05", sleepBin, "30"}})
	add(invocation{name: "verbose names the signal", args: []string{"-v", "0.05", sleepBin, "30"}})
	add(invocation{name: "the long verbose spelling", args: []string{"--verbose", "0.05", sleepBin, "30"}})
	add(invocation{name: "preserve-status passes the death through", args: []string{"--preserve-status", "0.05", sleepBin, "30"}})
	add(invocation{name: "preserve-status without a timeout", args: []string{"--preserve-status", "5", "/bin/sh", "-c", "exit 7"}})
	add(invocation{name: "foreground times out too", args: []string{"--foreground", "0.05", sleepBin, "30"}})
	add(invocation{name: "foreground and verbose", args: []string{"--foreground", "-v", "0.05", sleepBin, "30"}})

	for _, s := range []string{"TERM", "term", "SIGTERM", "sigterm", "SigTerm", "15", "015"} {
		add(invocation{name: "signal " + s, args: []string{"-v", "-s", s, "0.05", sleepBin, "30"}})
	}
	add(invocation{name: "signal KILL", args: []string{"-v", "-s", "KILL", "0.05", sleepBin, "30"}})
	add(invocation{name: "signal 9", args: []string{"-v", "-s", "9", "0.05", sleepBin, "30"}})
	add(invocation{name: "signal KILL in the foreground", args: []string{"--foreground", "-v", "-s", "KILL", "0.05", sleepBin, "30"}})
	add(invocation{name: "signal HUP in the foreground", args: []string{"--foreground", "-v", "-s", "HUP", "0.05", sleepBin, "30"}})
	add(invocation{name: "signal zero delivers nothing", args: []string{"-v", "-s", "0", "0.05", sleepBin, "0.3"}})
	add(invocation{name: "signal CONT is survivable", args: []string{"-v", "-s", "CONT", "0.05", sleepBin, "0.3"}})
	add(invocation{name: "a stop signal is resumed", args: []string{"-v", "-s", "TSTP", "0.05", sleepBin, "0.3"}})
	add(invocation{name: "a realtime signal", args: []string{"-v", "-s", "RTMIN", "0.05", sleepBin, "0.3"}})
	add(invocation{name: "a realtime signal with an offset", args: []string{"-v", "-s", "RTMIN+1", "0.05", sleepBin, "0.3"}})
	add(invocation{name: "the top of the realtime range", args: []string{"-v", "-s", "RTMAX", "0.05", sleepBin, "0.3"}})
	add(invocation{name: "a number in the realtime range", args: []string{"-v", "-s", "64", "0.05", sleepBin, "0.3"}})
	add(invocation{name: "a wait status is a signal", args: []string{"-v", "-s", "137", "0.05", sleepBin, "30"}})
	add(invocation{name: "SIG and a number", args: []string{"-v", "-s", "SIG9", "0.05", sleepBin, "30"}})
	add(invocation{name: "EXIT is signal zero", args: []string{"-v", "-s", "EXIT", "0.05", sleepBin, "0.3"}})
	add(invocation{name: "a number masking to zero", args: []string{"-v", "-s", "256", "0.05", sleepBin, "0.3"}})
	add(invocation{name: "an unbounded number masking to zero", args: []string{"-v", "-s", "2000000000", "0.05", sleepBin, "0.3"}})
	add(invocation{name: "the last signal wins", args: []string{"-v", "-s", "KILL", "-s", "TERM", "0.05", sleepBin, "30"}})
	add(invocation{name: "the long signal spelling", args: []string{"-v", "--signal=TERM", "0.05", sleepBin, "30"}})
	add(invocation{name: "the signal glued to the letter", args: []string{"-v", "-sTERM", "0.05", sleepBin, "30"}})
	for _, s := range []string{"BOGUS", "99999", "32", "33", "65", "", " TERM", "TERM ", "0x0", "+0", "-1", "int ", "LOST", "UNUSED", "SIG137", "393", "2147483648"} {
		add(invocation{name: "bad signal " + s, args: []string{"-s", s, "1", trueBin}})
	}
	add(invocation{name: "zero is a signal", args: []string{"-s", "0", "5", trueBin}})
	add(invocation{name: "two zeros are still signal zero", args: []string{"-s", "00", "5", trueBin}})

	add(invocation{name: "the kill-after is not needed", args: []string{"-v", "-k", "0.3", "0.05", sleepBin, "30"}})
	add(invocation{name: "the kill-after expires", args: append([]string{"-v", "-k", "0.2", trapDeadline}, timeoutIgnoresTerm()...)})
	add(invocation{name: "the kill-after expires quietly", args: append([]string{"-k", "0.2", trapDeadline}, timeoutIgnoresTerm()...)})
	add(invocation{name: "the kill-after in the foreground", args: append([]string{"--foreground", "-v", "-k", "0.2", trapDeadline}, timeoutIgnoresTerm()...)})
	add(invocation{name: "the kill-after with preserve-status", args: append([]string{"--foreground", "--preserve-status", "-k", "0.2", trapDeadline}, timeoutIgnoresTerm()...)})
	add(invocation{name: "a zero kill-after disables it", args: []string{"-v", "-k", "0", "0.05", sleepBin, "0.3"}})
	add(invocation{name: "signal zero with a kill-after", args: []string{"--foreground", "-v", "-s", "0", "-k", "0.2", "0.05", sleepBin, "30"}})
	add(invocation{name: "the last kill-after wins", args: append([]string{"-v", "-k", "30", "-k", "0.2", trapDeadline}, timeoutIgnoresTerm()...)})
	add(invocation{name: "the long kill-after spelling", args: []string{"-v", "--kill-after=0.3", "0.05", sleepBin, "30"}})
	add(invocation{name: "the kill-after glued to the letter", args: []string{"-v", "-k0.3", "0.05", sleepBin, "30"}})
	for _, k := range []string{"x", "", "-1", "1M"} {
		add(invocation{name: "bad kill-after " + k, args: []string{"-k", k, "1", trueBin}})
	}
	add(invocation{name: "a bad kill-after beats a bad duration", args: []string{"-k", "x", "y", trueBin}})

	add(invocation{name: "a command that is not there", args: []string{"5", "no-such-command-here"}})
	add(invocation{name: "a path that is not there", args: []string{"5", "./no-such-command-here"}})
	add(invocation{name: "a file without the execute bit", args: []string{"5", filepath.Join(execDir, "unreadable")}})
	add(invocation{name: "a directory", args: []string{"5", filepath.Join(execDir, "adir")}})
	add(invocation{name: "a directory named as a dot", args: []string{"5", "."}})

	add(invocation{name: "an unknown short option", args: []string{"-z", "1", trueBin}})
	add(invocation{name: "an unknown long option", args: []string{"--bogus", "1", trueBin}})
	add(invocation{name: "an ambiguous long prefix", args: []string{"--v", "1", trueBin}})
	add(invocation{name: "a longer ambiguous prefix", args: []string{"--ver", "1", trueBin}})
	add(invocation{name: "the prefix that resolves", args: []string{"--verb", "0.05", sleepBin, "30"}})
	add(invocation{name: "the signal prefix takes the next word", args: []string{"--s", "1", trueBin}})
	add(invocation{name: "the preserve prefix", args: []string{"--p", "5", trueBin}})
	add(invocation{name: "the foreground prefix", args: []string{"--f", "5", trueBin}})
	add(invocation{name: "the kill-after prefix with a value", args: []string{"--k=1", "5", trueBin}})
	add(invocation{name: "options stop at the first operand", args: []string{"5", "-v", trueBin}})
	add(invocation{name: "a cluster", args: []string{"-vs", "KILL", "0.05", sleepBin, "30"}})
	add(invocation{name: "a cluster with a glued value", args: []string{"-vk0.2", "0.05", sleepBin, "30"}})
	add(invocation{name: "an option needing a value at the end", args: []string{"-s"}})
	add(invocation{name: "a kill-after needing a value at the end", args: []string{"-k"}})
	add(invocation{name: "verbose with nothing to run", args: []string{"-v"}})
	add(invocation{name: "foreground with nothing to run", args: []string{"--foreground"}})
	add(invocation{name: "preserve-status with nothing to run", args: []string{"--preserve-status"}})
	add(invocation{name: "a kill-after with only a duration", args: []string{"-k", "1", "1"}})

	return cases
}

func TestTimeout(t *testing.T) {
	requireParity(t, "timeout", timeoutCases(t))
}

func TestTimeoutHelpVersion(t *testing.T) {
	requireHelp(t, "timeout", []string{"--help"}, 0)
	requireHelp(t, "timeout", []string{"--h"}, 0)
	requireHelp(t, "timeout", []string{"--help", "extra"}, 0)
	requireHelp(t, "timeout", []string{"-v", "--help"}, 0)
	requireVersion(t, "timeout", []string{"--version"}, 0)
	requireVersion(t, "timeout", []string{"--version", "x"}, 0)
	requireVersion(t, "timeout", []string{"--preserve-status", "--version"}, 0)
}
