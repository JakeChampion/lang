package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The corpus md5sum, sha1sum, sha224sum, sha256sum, sha384sum,
// sha512sum and b2sum share, because the seven are one program
// (`coreutils/lib/digest.fern`) parameterised by its digest.
//
// Running the same cases against all seven is not redundancy: each one
// carries its own word in the BSD tag and in `improperly formatted %s
// checksum line`, its own digest length — which is what the check-line
// grammar measures a line against — and, for b2sum, a length that the
// line itself declares. A case that passes for md5sum and fails for
// sha512sum is exactly the class of bug this shape catches.
//
// Checksum files that are supposed to be VALID are produced by running
// the reference binary, never by writing a digest down: the oracle
// makes them the same way it makes everything else here.

// sumSpec is what the seven do not share.
type sumSpec struct {
	util string
	// tag is the BSD-style prefix, and the word in the
	// improperly-formatted diagnostic.
	tag string
	// bits is the digest length nothing overrides. For b2sum it is also
	// the maximum `-l` accepts.
	bits int
	// variable marks b2sum: `-l BITS` resizes the digest, a checksum
	// line's own length is read off the hex run or off the `-NNN` of
	// `BLAKE2b-NNN`, and the tag names the length whenever it is not
	// the default.
	variable bool
}

func sumSpecs() map[string]sumSpec {
	return map[string]sumSpec{
		"md5sum":    {util: "md5sum", tag: "MD5", bits: 128},
		"sha1sum":   {util: "sha1sum", tag: "SHA1", bits: 160},
		"sha224sum": {util: "sha224sum", tag: "SHA224", bits: 224},
		"sha256sum": {util: "sha256sum", tag: "SHA256", bits: 256},
		"sha384sum": {util: "sha384sum", tag: "SHA384", bits: 384},
		"sha512sum": {util: "sha512sum", tag: "SHA512", bits: 512},
		"b2sum":     {util: "b2sum", tag: "BLAKE2b", bits: 512, variable: true},
	}
}

// refOutput runs the GNU binary for `util` and returns its stdout. It is
// how a checksum file that must be well formed is built: whatever the
// reference writes is by definition what it accepts back.
func refOutput(t *testing.T, util string, args ...string) string {
	t.Helper()
	bin := referenceBin(t, util)
	argv := append(crossPrefix(), bin)
	argv = append(argv, args...)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = baseEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %s: %v", util, strings.Join(args, " "), err)
	}
	return string(out)
}

// sumTree is the files one utility's corpus is asked about, plus the
// checksum files built over them.
type sumTree struct {
	dir string
	// The inputs.
	a, empty, big, backslash, newline, carriage, spaced, quoted, raw string
	missing, subdir                                                  string
	// hex is a digest-length run of '0': a well-formed line that cannot
	// match, for the FAILED paths.
	hex string
}

func sumFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newSumTree(t *testing.T, spec sumSpec) sumTree {
	t.Helper()
	dir := t.TempDir()
	tr := sumTree{
		dir: dir,
		a:   sumFile(t, dir, "a", "hello\n"),
		// Empty is its own digest, and the padding path for every one of
		// these algorithms differs at zero bytes.
		empty: sumFile(t, dir, "e0", ""),
		// Longer than one read block and not a multiple of any of the
		// block sizes, so the tail padding is exercised too.
		big:       sumFile(t, dir, "big", strings.Repeat("0123456789abcdef", 12345)+"tail"),
		backslash: sumFile(t, dir, `back\slash`, "x\n"),
		newline:   sumFile(t, dir, "new\nline", "x\n"),
		carriage:  sumFile(t, dir, "car\rriage", "x\n"),
		spaced:    sumFile(t, dir, "sp ace", "x\n"),
		quoted:    sumFile(t, dir, "q'uote", "x\n"),
		raw:       sumFile(t, dir, "na\xffme", "x\n"),
		missing:   filepath.Join(dir, "nosuch"),
		subdir:    filepath.Join(dir, "d"),
		hex:       strings.Repeat("0", spec.bits/4),
	}
	if err := os.Mkdir(tr.subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	return tr
}

// sumCases is the corpus of one checksum utility.
func sumCases(t *testing.T, util string) []invocation {
	t.Helper()
	spec := sumSpecs()[util]
	tr := newSumTree(t, spec)
	cases := computeCases(t, spec, tr)
	cases = append(cases, checkCases(t, spec, tr)...)
	cases = append(cases, grammarCases(t, spec, tr)...)
	if spec.variable {
		cases = append(cases, lengthCases(t, spec, tr)...)
	}
	return cases
}

// The compute half: digests, the two modes, --tag, -z, escaping, and
// the operand diagnostics.
func computeCases(t *testing.T, spec sumSpec, tr sumTree) []invocation {
	t.Helper()
	return []invocation{
		{name: "one file", args: []string{tr.a}},
		{name: "empty file", args: []string{tr.empty}},
		{name: "a file spanning read blocks", args: []string{tr.big}},
		{name: "two files", args: []string{tr.a, tr.empty}},
		{name: "the same file twice", args: []string{tr.a, tr.a}},
		{name: "stdin", stdin: "hello\n"},
		{name: "empty stdin"},
		{name: "stdin as an operand", args: []string{"-"}, stdin: "hello\n"},
		{name: "stdin twice", args: []string{"-", "-"}, stdin: "hello\n"},
		{name: "stdin and a file", args: []string{"-", tr.a}, stdin: "hello\n"},
		{name: "stdin spanning read blocks", stdin: strings.Repeat("z", 200000)},

		// Binary and text mode, and the marker between them.
		{name: "binary", args: []string{"-b", tr.a}},
		{name: "text", args: []string{"-t", tr.a}},
		{name: "long binary", args: []string{"--binary", tr.a}},
		{name: "long text", args: []string{"--text", tr.a}},
		{name: "binary then text", args: []string{"-b", "-t", tr.a}},
		{name: "text then binary", args: []string{"-t", "-b", tr.a}},
		{name: "binary from stdin", args: []string{"-b"}, stdin: "hello\n"},
		{name: "binary cluster", args: []string{"-bb", tr.a}},

		// --tag, and its two refusals.
		{name: "tag", args: []string{"--tag", tr.a}},
		{name: "tag binary", args: []string{"--tag", "-b", tr.a}},
		{name: "tag from stdin", args: []string{"--tag"}, stdin: "hello\n"},
		{name: "tag after text", args: []string{"-t", "--tag", tr.a}},
		{name: "tag before text", args: []string{"--tag", "-t", tr.a}},
		{name: "tag binary then text", args: []string{"--tag", "-b", "-t", tr.a}},
		{name: "tag text then binary", args: []string{"--tag", "-t", "-b", tr.a}},
		{name: "tag with check", args: []string{"--tag", "-c", tr.a}},
		{name: "tag with check and quiet", args: []string{"--tag", "-c", "--quiet", tr.a}},
		{name: "tag text with check", args: []string{"--tag", "-t", "-c", tr.a}},

		// -z: NUL terminators, and no escaping at all.
		{name: "zero", args: []string{"-z", tr.a}},
		{name: "zero two files", args: []string{"-z", tr.a, tr.empty}},
		{name: "zero tag", args: []string{"-z", "--tag", tr.a}},
		{name: "zero binary", args: []string{"-z", "-b", tr.a}},
		{name: "zero long", args: []string{"--zero", tr.a}},
		{name: "zero with check", args: []string{"-z", "-c", tr.a}},
		{name: "zero text with check", args: []string{"-z", "-t", "-c", tr.a}},
		{name: "zero tag text with check", args: []string{"-z", "--tag", "-t", "-c", tr.a}},
		{name: "zero with a backslash name", args: []string{"-z", tr.backslash}},
		{name: "zero with a newline name", args: []string{"-z", tr.newline}},

		// Escaped names, which is the detail most implementations get
		// wrong: the `\` leads the LINE, ahead of the tag.
		{name: "name with a backslash", args: []string{tr.backslash}},
		{name: "name with a newline", args: []string{tr.newline}},
		{name: "name with a carriage return", args: []string{tr.carriage}},
		{name: "name with a tab is not escaped", args: []string{sumFile(t, tr.dir, "ta\tb", "x\n")}},
		{name: "name with a space is not escaped", args: []string{tr.spaced}},
		{name: "name with a quote is not escaped", args: []string{tr.quoted}},
		{name: "name that is not valid UTF-8", args: []string{tr.raw}},
		{name: "escaped names and plain ones", args: []string{tr.a, tr.backslash, tr.newline}},
		{name: "tag with a backslash name", args: []string{"--tag", tr.backslash}},
		{name: "tag with a newline name", args: []string{"--tag", tr.newline}},
		{name: "tag with a carriage return name", args: []string{"--tag", tr.carriage}},
		{name: "binary with a backslash name", args: []string{"-b", tr.backslash}},

		// Operands and their diagnostics.
		{name: "missing file", args: []string{tr.missing}},
		{name: "missing then present", args: []string{tr.missing, tr.a}},
		{name: "present then missing", args: []string{tr.a, tr.missing}},
		{name: "directory", args: []string{tr.subdir}},
		{name: "directory with tag", args: []string{"--tag", tr.subdir}},
		{name: "directory and a file", args: []string{tr.subdir, tr.a}},
		{name: "empty operand", args: []string{""}},
		{name: "empty operand before a file", args: []string{"", tr.a}},
		{name: "missing name with a space", args: []string{filepath.Join(tr.dir, "no such")}},
		{name: "missing name that is not valid UTF-8", args: []string{filepath.Join(tr.dir, "no\xffsuch")}},
		{name: "dashdash", args: []string{"--"}, stdin: "hello\n"},
		{name: "dashdash then a name", args: []string{"--", tr.a}},
		{name: "dashdash then a dash", args: []string{"--", "-"}, stdin: "hello\n"},

		// The five check-only options outside check mode.
		{name: "ignore-missing without check", args: []string{"--ignore-missing", tr.a}},
		{name: "quiet without check", args: []string{"--quiet", tr.a}},
		{name: "status without check", args: []string{"--status", tr.a}},
		{name: "strict without check", args: []string{"--strict", tr.a}},
		{name: "warn without check", args: []string{"-w", tr.a}},
		{name: "long warn without check", args: []string{"--warn", tr.a}},
		// --status, --warn and --quiet are one setting with three
		// values: each clears the other two, so which one is reported
		// depends on the order they were given in.
		{name: "quiet then warn", args: []string{"--quiet", "-w", tr.a}},
		{name: "warn then quiet", args: []string{"-w", "--quiet", tr.a}},
		{name: "status then quiet", args: []string{"--status", "--quiet", tr.a}},
		{name: "status then warn", args: []string{"--status", "-w", tr.a}},
		{name: "quiet then strict", args: []string{"--quiet", "--strict", tr.a}},
		{name: "strict then quiet", args: []string{"--strict", "--quiet", tr.a}},
		{name: "warn then strict", args: []string{"-w", "--strict", tr.a}},
		{name: "ignore-missing then status", args: []string{"--ignore-missing", "--status", tr.a}},
		{name: "warn then ignore-missing", args: []string{"-w", "--ignore-missing", tr.a}},

		// getopt.
		{name: "invalid short option", args: []string{"-x", tr.a}},
		{name: "invalid short option in a cluster", args: []string{"-bx", tr.a}},
		{name: "unrecognized long option", args: []string{"--foo", tr.a}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar", tr.a}},
		{name: "empty long name", args: []string{"--=x", tr.a}},
		{name: "ambiguous t", args: []string{"--t", tr.a}},
		{name: "ambiguous s", args: []string{"--s", tr.a}},
		{name: "ambiguous st", args: []string{"--st", tr.a}},
		{name: "unique prefix binary", args: []string{"--b", tr.a}},
		{name: "unique prefix check", args: []string{"--c", tr.a}},
		{name: "unique prefix tag", args: []string{"--ta", tr.a}},
		{name: "unique prefix text", args: []string{"--te", tr.a}},
		{name: "unique prefix zero", args: []string{"--z", tr.a}},
		{name: "unique prefix ignore-missing", args: []string{"--i", tr.a}},
		{name: "unique prefix quiet", args: []string{"--q", tr.a}},
		{name: "unique prefix warn", args: []string{"--w", tr.a}},
		{name: "flag rejects a glued value", args: []string{"--binary=1", tr.a}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "operand then option", args: []string{tr.a, "-b"}},
		{name: "posix operand then option", args: []string{tr.a, "-b"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the operand", args: []string{"-b", tr.a}, env: []string{"POSIXLY_CORRECT=1"}},

		// Write failures.
		{name: "stdout closed", args: []string{tr.a}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{tr.a}, stdout: stdoutFull},
		{name: "stdout closed with a missing file", args: []string{tr.missing}, stdout: stdoutClosed},
		{name: "stdout full with many files", args: []string{tr.a, tr.a, tr.a}, stdout: stdoutFull},
	}
}

// The check half: the report, its summaries, and their exit statuses.
func checkCases(t *testing.T, spec sumSpec, tr sumTree) []invocation {
	t.Helper()
	dir := tr.dir
	mk := func(name, content string) string { return sumFile(t, dir, name, content) }

	ok := mk("c.ok", refOutput(t, spec.util, tr.a))
	okBinary := mk("c.okb", refOutput(t, spec.util, "-b", tr.a))
	okTag := mk("c.oktag", refOutput(t, spec.util, "--tag", tr.a))
	okTwo := mk("c.two", refOutput(t, spec.util, tr.a, tr.empty))
	esc := mk("c.esc", refOutput(t, spec.util, tr.backslash, tr.newline, tr.carriage))
	escTag := mk("c.esctag", refOutput(t, spec.util, "--tag", tr.backslash, tr.newline, tr.carriage))
	rawName := mk("c.rawname", refOutput(t, spec.util, tr.raw))
	spacedName := mk("c.spaced", refOutput(t, spec.util, tr.spaced))

	fail := mk("c.fail", tr.hex+"  "+tr.a+"\n")
	miss := mk("c.miss", tr.hex+"  "+tr.missing+"\n")
	isdir := mk("c.dir", tr.hex+"  "+tr.subdir+"\n")
	mal := mk("c.mal", "garbage\n")
	empty := mk("c.empty", "")
	blank := mk("c.blank", "\n\n")
	mix := mk("c.mix", refOutput(t, spec.util, tr.a)+tr.hex+"  "+tr.a+"\ngarbage\n")
	all3 := mk("c.all3", "garbage\n"+tr.hex+"  "+tr.a+"\n"+tr.hex+"  "+tr.missing+"\n"+refOutput(t, spec.util, tr.a))
	two3 := mk("c.two3", strings.Repeat("garbage\n", 2)+
		strings.Repeat(tr.hex+"  "+tr.a+"\n", 2)+
		tr.hex+"  "+tr.missing+"\n"+tr.hex+"  "+tr.missing+"2\n"+
		refOutput(t, spec.util, tr.a))
	missOk := mk("c.missok", tr.hex+"  "+tr.missing+"\n"+refOutput(t, spec.util, tr.a))
	missFail := mk("c.missfail", tr.hex+"  "+tr.missing+"\n"+tr.hex+"  "+tr.a+"\n")
	dirOk := mk("c.dirok", tr.hex+"  "+tr.subdir+"\n"+refOutput(t, spec.util, tr.a))
	comment := mk("c.comment", "# a comment\n"+refOutput(t, spec.util, tr.a))
	notComment := mk("c.notcomment", "  # a comment\n"+refOutput(t, spec.util, tr.a))
	blanks := mk("c.blanks", "\n"+refOutput(t, spec.util, tr.a)+"\n")
	crlf := mk("c.crlf", strings.ReplaceAll(refOutput(t, spec.util, tr.a), "\n", "\r\n"))
	noNewline := mk("c.nonl", strings.TrimRight(refOutput(t, spec.util, tr.a), "\n"))
	// `a` holds exactly what the stdin cases feed, so its digest is the
	// one a line naming `-` has to match.
	digestOfA := strings.Fields(refOutput(t, spec.util, tr.a))[0]
	dash := mk("c.dash", digestOfA+"  -\n")
	stdinName := refOutput(t, spec.util, tr.a)

	return []invocation{
		{name: "check ok", args: []string{"-c", ok}},
		{name: "check long", args: []string{"--check", ok}},
		{name: "check binary line", args: []string{"-c", okBinary}},
		{name: "check tagged line", args: []string{"-c", okTag}},
		{name: "check two files", args: []string{"-c", okTwo}},
		{name: "check escaped names", args: []string{"-c", esc}},
		{name: "check escaped tagged names", args: []string{"-c", escTag}},
		{name: "check a name that is not valid UTF-8", args: []string{"-c", rawName}},
		{name: "check a name with a space", args: []string{"-c", spacedName}},
		{name: "check mismatch", args: []string{"-c", fail}},
		{name: "check missing", args: []string{"-c", miss}},
		{name: "check a directory", args: []string{"-c", isdir}},
		{name: "check malformed", args: []string{"-c", mal}},
		{name: "check malformed with warn", args: []string{"-c", "-w", mal}},
		{name: "check empty file", args: []string{"-c", empty}},
		{name: "check blank lines only", args: []string{"-c", blank}},
		{name: "check mixed", args: []string{"-c", mix}},
		{name: "check mixed with warn", args: []string{"-c", "-w", mix}},
		{name: "check all three faults", args: []string{"-c", all3}},
		{name: "check all three faults twice", args: []string{"-c", two3}},
		{name: "check all three faults with warn", args: []string{"-c", "-w", two3}},
		{name: "check comment line", args: []string{"-c", comment}},
		{name: "check indented comment is not one", args: []string{"-c", "-w", notComment}},
		{name: "check blank lines are skipped", args: []string{"-c", "-w", blanks}},
		{name: "check CRLF line endings", args: []string{"-c", crlf}},
		{name: "check without a trailing newline", args: []string{"-c", noNewline}},
		{name: "check a line naming stdin", args: []string{"-c", dash}, stdin: "hello\n"},
		{name: "check from stdin", args: []string{"-c"}, stdin: stdinName},
		{name: "check from a dash operand", args: []string{"-c", "-"}, stdin: stdinName},
		{name: "check two checksum files", args: []string{"-c", ok, mal}},
		{name: "check three checksum files", args: []string{"-c", two3, ok, all3}},
		{name: "check a missing checksum file", args: []string{"-c", tr.missing}},
		{name: "check a checksum file that is a directory", args: []string{"-c", tr.subdir}},
		{name: "check an empty operand", args: []string{"-c", ""}},
		{name: "check with dashdash", args: []string{"-c", "--", ok}},

		// --quiet, --status, --warn and --strict over the same reports.
		{name: "check quiet ok", args: []string{"-c", "--quiet", ok}},
		{name: "check quiet mixed", args: []string{"-c", "--quiet", mix}},
		{name: "check quiet missing", args: []string{"-c", "--quiet", miss}},
		{name: "check status ok", args: []string{"-c", "--status", ok}},
		{name: "check status mixed", args: []string{"-c", "--status", mix}},
		{name: "check status malformed", args: []string{"-c", "--status", mal}},
		{name: "check status missing", args: []string{"-c", "--status", miss}},
		{name: "check status with warn after", args: []string{"-c", "--status", "-w", mix}},
		{name: "check warn then status", args: []string{"-c", "-w", "--status", mix}},
		{name: "check quiet then status", args: []string{"-c", "--quiet", "--status", ok}},
		{name: "check strict ok", args: []string{"-c", "--strict", ok}},
		{name: "check strict malformed", args: []string{"-c", "--strict", mix}},
		{name: "check strict malformed with warn", args: []string{"-c", "--strict", "-w", mix}},
		{name: "check strict only malformed", args: []string{"-c", "--strict", mal}},

		// --ignore-missing.
		{name: "check ignore-missing all missing", args: []string{"-c", "--ignore-missing", miss}},
		{name: "check ignore-missing with one ok", args: []string{"-c", "--ignore-missing", missOk}},
		{name: "check ignore-missing with one failure", args: []string{"-c", "--ignore-missing", missFail}},
		{name: "check ignore-missing does not skip a directory", args: []string{"-c", "--ignore-missing", isdir}},
		{name: "check ignore-missing a directory and an ok", args: []string{"-c", "--ignore-missing", dirOk}},
		{name: "check ignore-missing quiet", args: []string{"-c", "--ignore-missing", "--quiet", missOk}},
		{name: "check ignore-missing status", args: []string{"-c", "--ignore-missing", "--status", miss}},

		// The combinations check mode refuses.
		{name: "check with binary", args: []string{"-b", "-c", ok}},
		{name: "check with text", args: []string{"-t", "-c", ok}},
		{name: "check with binary and quiet", args: []string{"-b", "-c", "--quiet", ok}},
		{name: "check with zero", args: []string{"-z", "-c", ok}},
		{name: "check with zero and tag", args: []string{"-z", "--tag", "-c", ok}},
		{name: "check with zero and binary", args: []string{"-z", "-b", "-c", ok}},
		{name: "check then zero", args: []string{"-c", "-z", ok}},

		// Write failures in check mode.
		{name: "check with stdout closed", args: []string{"-c", ok}, stdout: stdoutClosed},
		{name: "check with stdout full", args: []string{"-c", ok}, stdout: stdoutFull},
		{name: "check status with stdout closed", args: []string{"-c", "--status", ok}, stdout: stdoutClosed},
		{name: "check mismatch with stdout closed", args: []string{"-c", fail}, stdout: stdoutClosed},
	}
}

// The check-line grammar, which is looser than what the utility writes:
// the separator rules, the length rules, the two shapes, and the three
// escapes a name may carry.
func grammarCases(t *testing.T, spec sumSpec, tr sumTree) []invocation {
	t.Helper()
	dir := tr.dir
	h := tr.hex
	n := 0
	line := func(content string) string {
		n++
		p := filepath.Join(dir, "g"+strconv.Itoa(n))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// The true digest of `a`, so a grammar case can be a MATCH rather
	// than only a well-formed miss.
	good := strings.Fields(refOutput(t, spec.util, tr.a))[0]
	name := tr.a

	cases := []invocation{
		// The separator between the digest and the name.
		{name: "grammar one blank", args: []string{"-c", "-w", line(h + " " + name + "\n")}},
		{name: "grammar two blanks", args: []string{"-c", "-w", line(h + "  " + name + "\n")}},
		{name: "grammar three blanks", args: []string{"-c", "-w", line(h + "   " + name + "\n")}},
		{name: "grammar star", args: []string{"-c", "-w", line(h + " *" + name + "\n")}},
		{name: "grammar two stars", args: []string{"-c", "-w", line(h + " **" + name + "\n")}},
		{name: "grammar blank star", args: []string{"-c", "-w", line(h + "  *" + name + "\n")}},
		{name: "grammar tab", args: []string{"-c", "-w", line(h + "\t" + name + "\n")}},
		{name: "grammar tab blank", args: []string{"-c", "-w", line(h + "\t " + name + "\n")}},
		{name: "grammar blank tab", args: []string{"-c", "-w", line(h + " \t" + name + "\n")}},
		{name: "grammar no separator", args: []string{"-c", "-w", line(h + name + "\n")}},
		{name: "grammar digest only", args: []string{"-c", "-w", line(h + "\n")}},
		{name: "grammar digest and one blank", args: []string{"-c", "-w", line(h + " \n")}},
		{name: "grammar digest and two blanks", args: []string{"-c", "-w", line(h + "  \n")}},
		{name: "grammar digest and three blanks", args: []string{"-c", "-w", line(h + "   \n")}},
		{name: "grammar digest and four blanks", args: []string{"-c", "-w", line(h + "    \n")}},
		{name: "grammar digest blank star", args: []string{"-c", "-w", line(h + " *\n")}},
		{name: "grammar digest two tabs", args: []string{"-c", "-w", line(h + "\t\t\n")}},
		{name: "grammar name with a trailing blank", args: []string{"-c", "-w", line(h + "  " + name + " \n")}},
		{name: "grammar name with an inner blank", args: []string{"-c", "-w", line(h + "  " + tr.spaced + "\n")}},

		// Leading blanks, and the `\` that marks an escaped name.
		{name: "grammar leading blank", args: []string{"-c", "-w", line(" " + h + "  " + name + "\n")}},
		{name: "grammar leading tab", args: []string{"-c", "-w", line("\t" + h + "  " + name + "\n")}},
		{name: "grammar leading blanks too short", args: []string{"-c", "-w", line(" " + h + " \n")}},
		{name: "grammar leading blanks just long enough", args: []string{"-c", "-w", line(" " + h + "  \n")}},
		{name: "grammar leading backslash", args: []string{"-c", "-w", line("\\" + h + "  " + name + "\n")}},
		{name: "grammar leading blanks then backslash", args: []string{"-c", "-w", line("  \\" + h + "  " + name + "\n")}},
		{name: "grammar backslash escape", args: []string{"-c", "-w", line("\\" + h + "  " + `na\\me` + "\n")}},
		{name: "grammar newline escape", args: []string{"-c", "-w", line("\\" + h + "  " + `na\nme` + "\n")}},
		{name: "grammar carriage return escape", args: []string{"-c", "-w", line("\\" + h + "  " + `na\rme` + "\n")}},
		{name: "grammar tab escape is invalid", args: []string{"-c", "-w", line("\\" + h + "  " + `na\tme` + "\n")}},
		{name: "grammar zero escape is invalid", args: []string{"-c", "-w", line("\\" + h + "  " + `na\0me` + "\n")}},
		{name: "grammar trailing lone backslash", args: []string{"-c", "-w", line("\\" + h + "  " + `name\` + "\n")}},
		{name: "grammar escaped with no backslash in the name", args: []string{"-c", "-w", line("\\" + h + "  plain\n")}},
		{name: "grammar unescaped backslash in the name", args: []string{"-c", "-w", line(h + "  " + `na\me` + "\n")}},

		// The digest itself.
		{name: "grammar uppercase digest", args: []string{"-c", "-w", line(strings.ToUpper(good) + "  " + name + "\n")}},
		{name: "grammar mixed case digest", args: []string{"-c", "-w", line(strings.ToUpper(good[:4]) + good[4:] + "  " + name + "\n")}},
		{name: "grammar one digit too long", args: []string{"-c", "-w", line(h + "0  " + name + "\n")}},
		{name: "grammar one digit too short", args: []string{"-c", "-w", line(h[1:] + "  " + name + "\n")}},
		{name: "grammar a non-hex digit", args: []string{"-c", "-w", line("z" + h[1:] + "  " + name + "\n")}},
		{name: "grammar a non-hex digit at the end", args: []string{"-c", "-w", line(h[1:] + "z  " + name + "\n")}},

		// The BSD-style shape.
		{name: "grammar tagged", args: []string{"-c", "-w", line(spec.tag + " (" + name + ") = " + good + "\n")}},
		{name: "grammar tagged no space", args: []string{"-c", "-w", line(spec.tag + "(" + name + ") = " + good + "\n")}},
		{name: "grammar tagged two spaces", args: []string{"-c", "-w", line(spec.tag + "  (" + name + ") = " + good + "\n")}},
		{name: "grammar tagged tab", args: []string{"-c", "-w", line(spec.tag + "\t(" + name + ") = " + good + "\n")}},
		{name: "grammar tagged no space around equals", args: []string{"-c", "-w", line(spec.tag + " (" + name + ")=" + good + "\n")}},
		{name: "grammar tagged tabs around equals", args: []string{"-c", "-w", line(spec.tag + " (" + name + ")\t=\t" + good + "\n")}},
		{name: "grammar tagged no equals", args: []string{"-c", "-w", line(spec.tag + " (" + name + ") " + good + "\n")}},
		{name: "grammar tagged no closing paren", args: []string{"-c", "-w", line(spec.tag + " (" + name + " = " + good + "\n")}},
		{name: "grammar tagged empty name", args: []string{"-c", "-w", line(spec.tag + " () = " + good + "\n")}},
		{name: "grammar tagged name with a paren", args: []string{"-c", "-w", line(spec.tag + " (a)b) = " + good + "\n")}},
		{name: "grammar tagged trailing junk", args: []string{"-c", "-w", line(spec.tag + " (" + name + ") = " + good + "x\n")}},
		{name: "grammar tagged trailing blank", args: []string{"-c", "-w", line(spec.tag + " (" + name + ") = " + good + " \n")}},
		{name: "grammar tagged short digest", args: []string{"-c", "-w", line(spec.tag + " (" + name + ") = " + good[1:] + "\n")}},
		{name: "grammar tagged lowercase word", args: []string{"-c", "-w", line(strings.ToLower(spec.tag) + " (" + name + ") = " + good + "\n")}},
		{name: "grammar tagged escaped", args: []string{"-c", "-w", line("\\" + spec.tag + " (" + `na\\me` + ") = " + good + "\n")}},
		{name: "grammar tagged word then digest", args: []string{"-c", "-w", line(spec.tag + good + "  " + name + "\n")}},

		// A NUL ends the line as a C string, but the too-short rule
		// still counts the bytes after it.
		{name: "grammar NUL first", args: []string{"-c", "-w", line("\x00" + h + "  " + name + "\n")}},
		{name: "grammar NUL after the name", args: []string{"-c", "-w", line(h + "  a\x00b\n")}},
		{name: "grammar NUL where the name would be", args: []string{"-c", "-w", line(h + "  \x00\n")}},
		{name: "grammar NUL past a marker", args: []string{"-c", "-w", line(h + "  \x00x\n")}},
	}
	return cases
}

// b2sum alone: -l, and the digest length a checksum line declares.
func lengthCases(t *testing.T, spec sumSpec, tr sumTree) []invocation {
	t.Helper()
	dir := tr.dir
	n := 0
	line := func(content string) string {
		n++
		p := filepath.Join(dir, "l"+strconv.Itoa(n))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sum := func(args ...string) string {
		return strings.Fields(refOutput(t, spec.util, args...))[0]
	}
	full := sum(tr.a)
	short := sum("-l", "8", tr.a)
	quarter := sum("-l", "128", tr.a)
	name := tr.a

	return []invocation{
		{name: "length 8", args: []string{"-l", "8", tr.a}},
		{name: "length 128", args: []string{"-l", "128", tr.a}},
		{name: "length 512", args: []string{"-l", "512", tr.a}},
		{name: "length 0 is the default", args: []string{"-l", "0", tr.a}},
		{name: "length 504", args: []string{"-l", "504", tr.a}},
		{name: "length glued", args: []string{"-l8", tr.a}},
		{name: "length long", args: []string{"--length=8", tr.a}},
		{name: "length long separate", args: []string{"--length", "8", tr.a}},
		{name: "length needs an argument", args: []string{"-l"}},
		{name: "length long needs an argument", args: []string{"--length"}},
		{name: "length with tag", args: []string{"-l", "256", "--tag", tr.a}},
		{name: "length 512 with tag", args: []string{"-l", "512", "--tag", tr.a}},
		{name: "length 0 with tag", args: []string{"-l", "0", "--tag", tr.a}},
		{name: "length with zero", args: []string{"-l", "8", "-z", tr.a}},
		{name: "length twice", args: []string{"-l", "8", "-l", "256", tr.a}},

		{name: "length not a multiple of eight", args: []string{"-l", "4", tr.a}},
		{name: "length too large", args: []string{"-l", "520", tr.a}},
		{name: "length too large and not a multiple", args: []string{"-l", "516", tr.a}},
		{name: "length far too large", args: []string{"-l", "1000", tr.a}},
		{name: "length negative", args: []string{"-l", "-8", tr.a}},
		{name: "length empty", args: []string{"-l", "", tr.a}},
		{name: "length not a number", args: []string{"-l", "abc", tr.a}},
		{name: "length with trailing junk", args: []string{"-l", "8x", tr.a}},
		{name: "length hex", args: []string{"-l", "0x10", tr.a}},
		{name: "length leading zeros", args: []string{"-l", "08", tr.a}},
		{name: "length leading blank", args: []string{"-l", " 8", tr.a}},
		{name: "length plus sign", args: []string{"-l", "+8", tr.a}},
		{name: "length trailing blank", args: []string{"-l", "8 ", tr.a}},
		{name: "length overflows uintmax", args: []string{"-l", "99999999999999999999", tr.a}},
		{name: "length at uintmax", args: []string{"-l", "18446744073709551615", tr.a}},
		{name: "length before an invalid option", args: []string{"-l", "4", "-x", tr.a}},
		{name: "invalid option before a length", args: []string{"-x", "-l", "4", tr.a}},
		{name: "length before a tag refusal", args: []string{"-l", "4", "--tag", "-t", tr.a}},
		{name: "length after an operand", args: []string{tr.a, "-l", "4"}},

		// A checksum line carries its own length, and -l does not
		// override it.
		{name: "check a short digest", args: []string{"-c", line(short + "  " + name + "\n")}},
		{name: "check a short digest with a length", args: []string{"-c", "-l", "512", line(short + "  " + name + "\n")}},
		{name: "check a tagged short digest", args: []string{"-c", line("BLAKE2b-8 (" + name + ") = " + short + "\n")}},
		{name: "check a tagged quarter digest", args: []string{"-c", line("BLAKE2b-128 (" + name + ") = " + quarter + "\n")}},
		{name: "check a tagged full digest", args: []string{"-c", line("BLAKE2b (" + name + ") = " + full + "\n")}},
		{name: "check a tagged full digest with a suffix", args: []string{"-c", line("BLAKE2b-512 (" + name + ") = " + full + "\n")}},
		{name: "check a tagged length that does not match", args: []string{"-c", "-w", line("BLAKE2b-256 (" + name + ") = " + full + "\n")}},
		{name: "check an untagged length that does not match", args: []string{"-c", "-w", line("BLAKE2b (" + name + ") = " + quarter + "\n")}},
		{name: "check a tagged length of zero", args: []string{"-c", "-w", line("BLAKE2b-0 (" + name + ") = \n")}},
		{name: "check a tagged length not a multiple of eight", args: []string{"-c", "-w", line("BLAKE2b-4 (" + name + ") = a\n")}},
		{name: "check a tagged length too large", args: []string{"-c", "-w", line("BLAKE2b-1000 (" + name + ") = " + full + "\n")}},
		{name: "check a tagged octal length", args: []string{"-c", "-w", line("BLAKE2b-020 (" + name + ") = aaaa\n")}},
		{name: "check a tagged hex length", args: []string{"-c", "-w", line("BLAKE2b-0x10 (" + name + ") = aaaa\n")}},
		{name: "check a tagged length with leading zeros", args: []string{"-c", "-w", line("BLAKE2b-08 (" + name + ") = aa\n")}},
		{name: "check a tagged length with a plus", args: []string{"-c", "-w", line("BLAKE2b-+8 (" + name + ") = aa\n")}},
		{name: "check a tagged length with no digits", args: []string{"-c", "-w", line("BLAKE2b- (" + name + ") = " + full + "\n")}},
		{name: "check a tagged negative length", args: []string{"-c", "-w", line("BLAKE2b--8 (" + name + ") = aa\n")}},
		{name: "check a two-digit hex run", args: []string{"-c", "-w", line("aa  " + name + "\n")}},
		{name: "check a one-digit hex run", args: []string{"-c", "-w", line("a  " + name + "\n")}},
		{name: "check a three-digit hex run", args: []string{"-c", "-w", line("aaa  " + name + "\n")}},
		{name: "check a full hex run", args: []string{"-c", "-w", line(strings.Repeat("a", 128) + "  " + name + "\n")}},
		{name: "check a hex run past the maximum", args: []string{"-c", "-w", line(strings.Repeat("a", 130) + "  " + name + "\n")}},
		{name: "check a hex run and one blank", args: []string{"-c", "-w", line("aa \n")}},
		{name: "check a hex run and two blanks", args: []string{"-c", "-w", line("aa  \n")}},
		{name: "check a hex run followed by junk", args: []string{"-c", "-w", line("aax  " + name + "\n")}},
	}
}
