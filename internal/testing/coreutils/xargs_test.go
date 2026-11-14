package coreutils

import (
	"fmt"
	"strings"
	"testing"
)

// The corpus of the xargs port. GNU findutils 4.9.0 is the oracle. The
// cases come from the documented behaviour of GNU xargs and from runs of
// the GNU binary. No case is copied from the GNU test suite.

func init() {
	registerCorpus("xargs", xargsCases)
}

func xargsCases(t *testing.T) []invocation {
	var cases []invocation
	cases = append(cases, xargsInputCases()...)
	cases = append(cases, xargsOptionCases()...)
	cases = append(cases, xargsExitCases()...)
	cases = append(cases, xargsSizeCases()...)
	cases = append(cases, xargsParallelCases()...)
	cases = append(cases, xargsFileCases()...)
	return cases
}

func xc(stdin, line string) invocation {
	return invocation{name: fmt.Sprintf("xargs %s <<< %q", line, stdin), args: shellWords(line), stdin: stdin}
}

// xcDarwin is xc for a case whose Darwin reference is wrong, for the reason
// given.
func xcDarwin(stdin, line, reason string) invocation {
	inv := xc(stdin, line)
	inv.darwinLibcDefect = reason
	return inv
}

// appleAssertForm is why the Darwin reference is wrong when GNU xargs fails
// an assertion.
const appleAssertForm = "Apple's assert words a failed assertion in its own form, not in the form of glibc"

// xargsInputCases: how the input splits into arguments.
func xargsInputCases() []invocation {
	return []invocation{
		xc("a b c", ``),
		xc("a b c\n", `echo`),
		xc("a b c", `-n 1`),
		xc("a b c", `-n 2 echo X`),
		xc("a 'b c' \"d e\" f\\ g", `echo`),
		xc("a 'b", `echo`),
		xc("a \"b", `echo`),
		xc("a\\", `echo`),
		xc("a\nb\n\nc", `-L 1 echo`),
		xc("a \nb\nc d", `-L 1 echo`),
		xc("a\t\nb\nc d\n", `-L 1 echo`),
		xc("a\nb", `-l echo`),
		xc("a\nb\nc", `-l2 echo`),
		xc("a b c\nd\ne f\n", `-L 2 echo`),
		xc("a b", `-i echo [{}]`),
		xc("a b\nc d", `-I XX echo [XX] XX`),
		xc("a\nb", `-I {} -n 1 echo {}`),
		xc("  a  b  \n  c\n", `-I X echo [X]`),
		xc("a\\\nb\n", `-I X echo [X]`),
		xc("\"a b\"  c\n", `-I X echo [X]`),
		xc("a\n\nb\n", `-I X echo [X]`),
		xc("a\n", `-I X echo X X XX`),
		xc("a b", `-0 echo`),
		xc("a\x00b c\x00", `-0 echo`),
		xc("a\x00b c\x00", `-0 -n 1 echo`),
		xc("a\x00\x00b\x00", `-0 -n 1 echo`),
		xc("a\x00b", `-0 -L 1 echo`),
		xc("a\x00b\x00", `-0 -I X echo [X]`),
		xc("a:b:c", `-d : echo`),
		xc("a:b:c", `-d '\n' echo`),
		xc("a\nb\n", `-d '\n' -n 1 echo`),
		xc("a\tb", `-d '\t' echo`),
		xc("aXb", `-d '\x58' echo`),
		xc("aXb", `-d '\130' echo`),
		xc("aXb", `-d '\x' echo`),
		xc("aXb", `-d '\q' echo`),
		xc("aXb", `-d '\' echo`),
		xc("aXb", `-d '' echo`),
		xc("aXb", `-d ab echo`),
		xc("a\\b", `-d '\\' echo`),
		xc("a\x7fb", `-d '\177' echo`),
		xc("a,b,,c,", `-d , -n 1 echo`),
		xc("a 'b c' d", `-d ' ' echo`),
		xc("a b STOP c", `-E STOP echo`),
		xc("a b STOP c", `-e STOP echo`),
		xc("a b STOP c", `-eSTOP echo`),
		xc("a b STOP c", `--eof=STOP echo`),
		xc("a b STOP c", `--eof echo`),
		xc("a b\n", `-E b echo`),
		xc("a b", `-E b echo`),
		xc("a b \n", `-E b echo`),
		xc("a\nb\nc\n", `-E b echo`),
		xc("a \"b\" c\n", `-E b echo`),
		xc("a b\n", `-E '' echo`),
		xc("a b\n", `-e echo`),
		xc("a b\n", `-E b -0 echo`),
		xc("a b\n", `-E b -d , echo`),
		xc("a\nSTOP\nb\n", `-I X -E STOP echo X`),
		xc("a\x00b c\n", `echo`),
		xc("a\x00b\x00c\n", `-n 1 echo`),
		xc("a\\ b c\n", `echo`),
		xc("a\\\nb\n", `echo`),
		xc("a\\\\b\n", `echo`),
		xc("\"a\\\"b\"\n", `echo`),
		xc("'a\\'b\n", `echo`),
		xc("\"a\nb\"\n", `echo`),
		xc("'a\nb'\n", `echo`),
		xc("x 'a\nb'\n", `echo`),
		xc("a\tb\vc\fd\re\n", `echo`),
		xc("'' \"\" x\n", `echo`),
		xc("''\n", `-n 1 echo [`),
		xc("", `echo empty`),
		xc("", `-r echo empty`),
		xc("\n", `-r echo nothing`),
		xc("   \n\n", `echo blank`),
		xc("   \n\n", `-r echo blank`),
		xc("", `-I X echo X`),
		xc("", `-L 1 echo`),
		xc("", `-n 1 echo`),
		xc("", `-0 echo`),
		xc("a\xffb\n", `echo`),
		xc(strings.Repeat("word ", 3000), `-n 7 echo`),
		xc(strings.Repeat("line one\n", 500), `-L 3 echo`),
	}
}

// xargsOptionCases: the options and the errors in them.
func xargsOptionCases() []invocation {
	return []invocation{
		xc("a b\n", `-t echo`),
		xc("a b\n", `-t -n 1 echo`),
		xc("a b\n", `--verbose echo "x y" "'q'"`),
		xc("a b\n", `-t sh -c 'echo $1' sh`),
		xc("a\n", `-t echo 'a"b' 'c$d' "e\\f" ''`),
		xc("a b\n", `-L 1 -n 1 echo`),
		xc("a b\n", `-n 1 -L 1 echo`),
		xc("a b\n", `-n 1 -I X echo X`),
		xc("a b\n", `-I X -n 1 echo X`),
		xc("a b\n", `-L 1 -I X echo X`),
		xc("a b\n", `-I X -L 1 echo X`),
		xc("a b\n", `-n1 -l echo`),
		xc("a b\n", `-l -n1 echo`),
		xc("a b\n", `-i -l echo`),
		xc("a b\n", `-l -i echo {}`),
		xc("a b\n", `-L 0 echo`),
		xc("a b\n", `-l0 echo`),
		xc("a b\n", `-L -1 echo`),
		xc("a b", `-n 0 echo`),
		xc("a b", `-P x echo`),
		xc("x", `-q`),
		xc("x", `--foo`),
		xc("x", `-n`),
		xc("x", `-L x`),
		xc("x", `-n x`),
		xc("x", `-n -1`),
		xc("x", `-P -1`),
		xc("a\n", `-h`),
		xc("a\n", `-v`),
		xc("a\n", `--m 5 echo`),
		xc("a\n", `--n echo`),
		xc("a\n", `--e echo`),
		xc("a\n", `--ver`),
		xc("a\n", `--max-procs=x echo`),
		xc("a\n", `-P 99999999999 echo`),
		xc("a\n", `-n 99999999999 echo`),
		xc("a\n", `-L 99999999999 echo`),
		xc("a\n", `-n ' 5' echo`),
		xc("a\n", `-n 5x echo`),
		xc("a\n", `-n 0x10 echo`),
		xc("a\n", `-n 010 echo`),
		xc("a\n", `-n +5 echo`),
		xc("a\n", `--max-args=1 echo`),
		xc("a\n", `--max-ar 1 echo`),
		xc("a\n", `--max-lines echo`),
		xc("a\n", `--max-lines=1 echo`),
		xc("a\n", `--max-lines= echo`),
		xc("a\n", `--max-args`),
		xc("a\n", `-i echo {} x`),
		xc("a\n", `-iX echo X`),
		xc("a\n", `--replace echo {}`),
		xc("a\n", `--replace=X echo X`),
		xc("a\n", `--replace= echo x`),
		xc("a\n", `-I '' echo x`),
		xc("a\n", `-- -n 1 echo`),
		xc("a\n", `echo -n`),
		xc("a\n", `-x echo`),
		xc("a\n", `--null=x echo`),
		xc("a\n", `--verbose=x echo`),
		xc("a\n", `--help=x`),
		xc("a\n", `--show-limits=1`),
		xc("a\n", `--process-slot-var= echo`),
		xc("a\n", `--process-slot-var=A=B echo`),
		xc("a\n", `--process-slot-var=FOO sh -c 'echo ${FOO-unset}'`),
		xc("a\n", `sh -c 'echo ${FOO-unset}'`),
		xc("a\n", `-p true`),
		xc("a\n", `--interactive echo`),
		xc("a\n", `-p -t echo`),
		xcDarwin("a b\n", `-o echo`, appleAssertForm),
		xcDarwin("a b\n", `--open-tty echo`, appleAssertForm),
		xc("a\n", `--show-limits -r true`),
		xc("a\n", `--show-limits echo`),
		xc("a\n", `-s 2000 --show-limits true`),
		xc("a\n", `-s 99999999999 --show-limits true`),
		xc("a\n", `--show-limit true`),
		xc("a\n", `-a nosuch echo`),
		xc("a\n", `-a /dev/null echo`),
		xc("a\n", `--arg-file=/dev/null echo`),
		xc("a b", `--delimiter=b echo`),
		xc("a b", `--null echo`),
		xc("a b", `--no-run-if-empty echo`),
		xc("a b", `--exit echo`),
		xc("a b", `--max-procs=1 echo`),
		xc("a b", `--max-chars=20 echo`),
	}
}

// xargsExitCases: the exit statuses of the commands and of xargs.
func xargsExitCases() []invocation {
	return []invocation{
		xc("a b", `false`),
		xc("a b", `true`),
		xc("a b", `sh -c 'exit 255'`),
		xc("a b", `sh -c 'kill -9 $$'`),
		xc("a\n", `-n 1 sh -c 'kill -15 $$'`),
		xc("a b", `nosuchcmd`),
		xc("a b", `/etc`),
		xc("a\n", `./nosuch`),
		xc("a\n", `''`),
		xc("a b", `-n 1 sh -c 'exit 3'`),
		xc("a b\n", `-n 1 sh -c 'exit $1' sh`),
		xc("1 255 2\n", `-n 1 sh -c 'exit $0'`),
		xc("255 1\n", `-n 1 sh -c 'exit $0'`),
		xc("0 255 3\n", `-n 1 sh -c 'exit $0'`),
		xc("1 0\n", `-n 1 sh -c 'exit $0'`),
		xc("126 0\n", `-n 1 sh -c 'exit $0'`),
		xc("127 0\n", `-n 1 sh -c 'exit $0'`),
		xc("125 0\n", `-n 1 sh -c 'exit $0'`),
		xc("a b c\n", `-n 1 sh -c 'echo $0; exit 255'`),
		xc("a b c\n", `-n 1 sh -c 'echo $0; kill -9 $$'`),
		xc("a 'b\n", `-n 1 false`),
	}
}

// xargsSizeCases: the size of the command line.
func xargsSizeCases() []invocation {
	return []invocation{
		xc("a b c d", `-s 10 echo`),
		xc("aaaaaaaaaaaaaaaa", `-s 10 echo`),
		xc("a b c d", `-x -s 10 echo`),
		xc("a b\n", `-s 5 echo`),
		xc("a b\n", `-s 6 echo`),
		xc("a b\n", `-s 7 echo`),
		xc("a b\n", `-s 8 echo`),
		xc("a b\n", `-x -s 7 echo`),
		xc("a b c\n", `-x -n 2 -s 9 echo`),
		xc("a b c\n", `-x -n 2 -s 8 echo`),
		xc("a b c\n", `-n 2 -s 9 echo`),
		xc("a b c\nd\n", `-x -L 1 -s 9 echo`),
		xc("aaa\n", `-I X -s 10 echo X`),
		xc("aaa\n", `-I X -s 20 echo X X X X`),
		xc("a b", `-s 0 echo`),
		xc("a\n", `-s 1 echo`),
		xc("a\n", `-s abc echo`),
		xc("a\n", `-s 10k echo`),
		xc("a\n", `-s 99999999999 echo`),
		xc(strings.Repeat("abcdefghij ", 30000), `echo`),
		xc(strings.Repeat("abcdefghij ", 30000), `-s 4096 echo`),
		xc(strings.Repeat("abcdefghij\n", 2000), `-I X -s 4096 echo X`),
	}
}

// xargsParallelCases: -P runs commands at once, so their output is
// compared as a set of lines.
func xargsParallelCases() []invocation {
	unordered := func(inv invocation) invocation {
		inv.unordered = true
		return inv
	}
	return []invocation{
		unordered(xc("a b\n", `-P 2 -n 1 echo`)),
		unordered(xc("a\n", `-n 1 -P 0 echo`)),
		unordered(xc("a b c\n", `-n 1 -P 3 sh -c 'echo $0'`)),
		unordered(xc("a b c\n", `-n 1 -P 2 --process-slot-var=S sh -c 'echo $0 $S; if [ $0 = a ]; then sleep 1; fi'`)),
		unordered(xc("a b c d e f\n", `-n 1 -P 2 sh -c 'exit 1'`)),
		unordered(xc("1 2 3 4 5 6 7 8 9 10\n", `-n 2 -P 4 echo`)),
		xc("a b c\n", `-n 1 -P 2 sh -c 'exit 255'`),
		xc("a b c\n", `-n 1 -P 2 nosuchcmd`),
		unordered(xc("x\n", `-P 2 --process-slot-var=SLOT sh -c 'echo $SLOT $0'`)),
	}
}

// xargsFileCases read the input from a file with -a, so the command keeps
// the standard input of xargs.
func xargsFileCases() []invocation {
	seed := func(t *testing.T, dir string) {
		t.Helper()
		writeFile(t, dir, "args", "one two\nthree\n")
		writeFile(t, dir, "nul", "a\x00b c\x00")
	}
	return []invocation{
		{name: "xargs -a args", args: []string{"-a", "args", "echo"}, seedTree: seed},
		{name: "xargs -a args cat", args: []string{"-a", "args", "-n", "1", "sh", "-c", "echo $0; cat"}, stdin: "from stdin\n", seedTree: seed},
		{name: "xargs stdin of the command is null", args: []string{"sh", "-c", "cat; echo $0"}, stdin: "x\n", seedTree: seed},
		{name: "xargs -0 -a nul", args: []string{"-0", "-a", "nul", "-n", "1", "echo"}, seedTree: seed},
		{name: "xargs -a a directory", args: []string{"-a", ".", "echo"}, seedTree: seed},
		{name: "xargs -a two files", args: []string{"-a", "nul", "-a", "args", "echo"}, seedTree: seed},
		{name: "xargs to a full stdout", args: []string{"echo"}, stdin: "a b\n", stdout: stdoutFull},
		{name: "xargs merged output", args: []string{"-t", "-n", "1", "echo"}, stdin: "a b c\n", merged: true},
		{name: "xargs -p on a terminal", args: []string{"-p", "echo"}, stdin: "a\n", ttyIn: true},
	}
}

func TestXargsParity(t *testing.T) {
	requireParity(t, "xargs", xargsCases(t))
}

func TestXargsHelpVersion(t *testing.T) {
	requireHelp(t, "xargs", []string{"--help"}, 0)
	requireHelp(t, "xargs", []string{"--he"}, 0)
	requireVersion(t, "xargs", []string{"--version"}, 0)
	requireVersion(t, "xargs", []string{"--vers"}, 0)
}
