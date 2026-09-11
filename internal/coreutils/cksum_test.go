package coreutils

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// cksum(1) — eleven algorithms behind one `-a`, and the corpus is its
// own rather than `sumCases`, because cksum is not the program the
// seven `*sum` utilities are:
//
//   - its default output is TAGGED and `--untagged` is the reversed
//     form, which is the opposite way round from the seven;
//   - `crc`, `sysv` and `bsd` print a checksum and a count, cannot be
//     checked, and DO NOT ESCAPE the file name;
//   - a digest may be written and read in base64 as well as hex;
//   - in check mode without `-a`, each line's TAG chooses the algorithm
//     to verify it with, and a line without one is improperly formatted;
//   - the check-line grammar is looser here in three measured ways: the
//     minimum line is three bytes rather than the digest plus two, every
//     algorithm takes the `-NNN` length and the tab-before-paren that
//     only BLAKE2b takes in b2sum, and `MD5-64 (f)` verifies the first
//     eight bytes of an MD5.
//
// `--debug` is the one output not compared here. It names which CRC
// implementation GNU chose — `using pclmul hardware support` on a CPU
// that has the instruction — which is a runtime dispatch a static Fern
// binary does not have and cannot honestly claim; docs/COREUTILS.md
// records it beside `--help` and `--version`. Everything else about the
// option IS compared: that it is accepted, that it refuses a value,
// that it appears in the ambiguity list, and that it prints nothing for
// an algorithm that is not the CRC.

// ckAlgos are `-a`'s names in declaration order, which is the order the
// `Valid arguments are:` list prints them in.
func ckAlgos() []string {
	return []string{"bsd", "sysv", "crc", "md5", "sha1", "sha224", "sha256", "sha384", "sha512", "blake2b", "sm3"}
}

// ckDigests are the eight that are digests: the ones `-c`, `--tag`,
// `--base64` and the name escaping apply to.
func ckDigests() []string {
	return []string{"md5", "sha1", "sha224", "sha256", "sha384", "sha512", "blake2b", "sm3"}
}

// ckTags are the words those eight write in a BSD tag and in
// `improperly formatted %s checksum line`.
func ckTags() map[string]string {
	return map[string]string{
		"md5": "MD5", "sha1": "SHA1", "sha224": "SHA224", "sha256": "SHA256",
		"sha384": "SHA384", "sha512": "SHA512", "blake2b": "BLAKE2b", "sm3": "SM3",
	}
}

// ckTree is the files cksum's corpus is asked about.
type ckTree struct {
	dir                                                 string
	a, empty, big, backslash, newline, carriage, spaced string
	raw, missing, subdir                                string
	// blocks are the sizes at which sysv's 512-byte and bsd's 1 KiB
	// block counts round up.
	b512, b513, b1024, b1025 string
}

func newCkTree(t *testing.T) ckTree {
	t.Helper()
	dir := t.TempDir()
	tr := ckTree{
		dir:   dir,
		a:     sumFile(t, dir, "a", "hello\n"),
		empty: sumFile(t, dir, "e0", ""),
		// Past one read block and not a multiple of any block size, so
		// the tail padding of every digest is exercised.
		big:       sumFile(t, dir, "big", strings.Repeat("0123456789abcdef", 12345)+"tail"),
		backslash: sumFile(t, dir, `back\slash`, "x\n"),
		newline:   sumFile(t, dir, "new\nline", "x\n"),
		carriage:  sumFile(t, dir, "car\rriage", "x\n"),
		spaced:    sumFile(t, dir, "sp ace", "x\n"),
		raw:       sumFile(t, dir, "na\xffme", "x\n"),
		missing:   filepath.Join(dir, "nosuch"),
		subdir:    filepath.Join(dir, "d"),
		b512:      sumFile(t, dir, "b512", strings.Repeat("z", 512)),
		b513:      sumFile(t, dir, "b513", strings.Repeat("z", 513)),
		b1024:     sumFile(t, dir, "b1024", strings.Repeat("z", 1024)),
		b1025:     sumFile(t, dir, "b1025", strings.Repeat("z", 1025)),
	}
	if err := os.Mkdir(tr.subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	return tr
}

// ckLines writes one-off checksum files; each call names a fresh path.
func ckLines(t *testing.T, dir, prefix string) func(string) string {
	t.Helper()
	n := 0
	return func(content string) string {
		n++
		p := filepath.Join(dir, prefix+strconv.Itoa(n))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
}

func init() {
	registerCorpus("cksum", cksumCases)
}

func cksumCases(t *testing.T) []invocation {
	t.Helper()
	tr := newCkTree(t)
	cases := ckAlgorithmCases(t, tr)
	cases = append(cases, ckOptionCases(t, tr)...)
	cases = append(cases, ckLengthCases(t, tr)...)
	cases = append(cases, ckCheckCases(t, tr)...)
	cases = append(cases, ckDetectCases(t, tr)...)
	cases = append(cases, ckGrammarCases(t, tr)...)
	return cases
}

// Every algorithm over every input shape and every output form.
func ckAlgorithmCases(t *testing.T, tr ckTree) []invocation {
	t.Helper()
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args})
	}

	// The default algorithm, which is the POSIX CRC and not crc32b.
	add("default one file", tr.a)
	add("default empty file", tr.empty)
	add("default a file spanning read blocks", tr.big)
	add("default two files", tr.a, tr.empty)
	add("default the same file twice", tr.a, tr.a)
	add("default missing file", tr.missing)
	add("default directory", tr.subdir)
	add("default missing then present", tr.missing, tr.a)
	add("default empty operand", "")
	add("default dash", "-")
	add("default dashdash", "--")
	add("default dashdash then a name", "--", tr.a)
	cases = append(cases,
		invocation{name: "default stdin", stdin: "hello\n"},
		invocation{name: "default empty stdin"},
		invocation{name: "default stdin as an operand", args: []string{"-"}, stdin: "hello\n"},
		invocation{name: "default stdin spanning read blocks", stdin: strings.Repeat("z", 200000)},
	)

	for _, a := range ckAlgos() {
		add(a+" one file", "-a", a, tr.a)
		add(a+" empty file", "-a", a, tr.empty)
		add(a+" spanning read blocks", "-a", a, tr.big)
		add(a+" two files", "-a", a, tr.a, tr.empty)
		add(a+" missing file", "-a", a, tr.missing)
		add(a+" directory", "-a", a, tr.subdir)
		add(a+" missing then present", "-a", a, tr.missing, tr.a)
		add(a+" empty operand", "-a", a, "")
		add(a+" dash", "-a", a, "-")
		add(a+" tag", "-a", a, "--tag", tr.a)
		add(a+" untagged", "-a", a, "--untagged", tr.a)
		add(a+" tag then untagged", "-a", a, "--tag", "--untagged", tr.a)
		add(a+" untagged then tag", "-a", a, "--untagged", "--tag", tr.a)
		add(a+" binary untagged", "-a", a, "-b", "--untagged", tr.a)
		add(a+" text untagged", "-a", a, "-t", "--untagged", tr.a)
		add(a+" binary tagged", "-a", a, "-b", tr.a)
		add(a+" zero", "-a", a, "-z", tr.a)
		add(a+" zero untagged", "-a", a, "-z", "--untagged", tr.a)
		add(a+" base64", "-a", a, "--base64", tr.a)
		add(a+" base64 untagged", "-a", a, "--base64", "--untagged", tr.a)
		add(a+" raw", "-a", a, "--raw", tr.a)
		add(a+" raw empty file", "-a", a, "--raw", tr.empty)
		add(a+" raw zero", "-a", a, "--raw", "-z", tr.a)
		add(a+" raw two files", "-a", a, "--raw", tr.a, tr.empty)
		add(a+" raw missing", "-a", a, "--raw", tr.missing)
		if a != "crc" {
			// The CRC is the one algorithm GNU has more than one
			// implementation of, so it is the one `--debug` names; see
			// the note at the top of this file.
			add(a+" debug", "-a", a, "--debug", tr.a)
		}
		// Names: escaped for the eight digests, verbatim for the three.
		add(a+" backslash name", "-a", a, tr.backslash)
		add(a+" newline name", "-a", a, tr.newline)
		add(a+" carriage return name", "-a", a, tr.carriage)
		add(a+" space in the name", "-a", a, tr.spaced)
		add(a+" name that is not valid UTF-8", "-a", a, tr.raw)
		add(a+" untagged backslash name", "-a", a, "--untagged", tr.backslash)
		add(a+" untagged newline name", "-a", a, "--untagged", tr.newline)
		add(a+" zero newline name", "-a", a, "-z", tr.newline)
		add(a+" escaped and plain names", "-a", a, tr.a, tr.backslash, tr.newline)
		cases = append(cases,
			invocation{name: a + " stdin", args: []string{"-a", a}, stdin: "hello\n"},
			invocation{name: a + " empty stdin", args: []string{"-a", a}},
			invocation{name: a + " stdin as an operand", args: []string{"-a", a, "-"}, stdin: "hello\n"},
			invocation{name: a + " raw from stdin", args: []string{"-a", a, "--raw"}, stdin: "hello\n"},
			invocation{name: a + " stdout closed", args: []string{"-a", a, tr.a}, stdout: stdoutClosed},
			invocation{name: a + " stdout full", args: []string{"-a", a, tr.a}, stdout: stdoutFull},
			invocation{name: a + " raw with stdout closed", args: []string{"-a", a, "--raw", tr.a}, stdout: stdoutClosed},
			invocation{name: a + " stdout closed with a missing file", args: []string{"-a", a, tr.missing}, stdout: stdoutClosed},
		)
	}

	// The block counts sysv and bsd print round up at different sizes.
	for _, a := range []string{"sysv", "bsd", "crc"} {
		add(a+" 512 bytes", "-a", a, tr.b512)
		add(a+" 513 bytes", "-a", a, tr.b513)
		add(a+" 1024 bytes", "-a", a, tr.b1024)
		add(a+" 1025 bytes", "-a", a, tr.b1025)
		add(a+" all four block sizes", "-a", a, tr.b512, tr.b513, tr.b1024, tr.b1025)
	}
	return cases
}

// The option surface: `-a`'s argument matching, getopt, and every
// refusal in the order the reference tests them.
func ckOptionCases(t *testing.T, tr ckTree) []invocation {
	t.Helper()
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args})
	}

	// -a takes an exact name: no abbreviations, no case folding.
	for _, v := range []string{"", "m", "md", "MD5", "crc32b", "CRC", "s", "sh", "sha2", "sha25", "b", "bl", "blake", "sm", "c", "nonsense", "crc "} {
		add("algorithm "+strconv.Quote(v), "-a", v, tr.a)
	}
	add("algorithm glued", "-amd5", tr.a)
	add("algorithm long", "--algorithm=md5", tr.a)
	add("algorithm long separate", "--algorithm", "md5", tr.a)
	add("algorithm long prefix", "--algo=md5", tr.a)
	add("algorithm twice", "-a", "md5", "-a", "sha1", tr.a)
	add("algorithm needs an argument", "-a")
	add("algorithm long needs an argument", "--algorithm")
	add("algorithm prefix needs an argument", "--a")
	add("algorithm after an operand", tr.a, "-a", "md5")

	// getopt: the messages, and the ambiguity list in declaration order.
	add("invalid short option", "-x", tr.a)
	add("invalid short option in a cluster", "-bx", tr.a)
	add("unrecognized long option", "--foo", tr.a)
	add("unrecognized long option with a value", "--foo=bar", tr.a)
	add("empty long name", "--=x", tr.a)
	add("ambiguous t", "--t", tr.a)
	add("ambiguous s", "--s", tr.a)
	add("ambiguous st", "--st", tr.a)
	add("unique prefix check", "--c", tr.a)
	add("unique prefix tag", "--ta", tr.a)
	add("unique prefix text", "--te", tr.a)
	add("unique prefix untagged", "--u", tr.a)
	add("unique prefix zero", "--z", tr.a)
	add("unique prefix raw", "--r", tr.a)
	add("unique prefix base64", "--ba", tr.a)
	add("unique prefix binary", "--bi", tr.a)
	add("ambiguous b", "--b", tr.a)
	// `--d` resolves to the one long option starting with it; the
	// algorithm is not the CRC so its output is compared too.
	add("unique prefix debug", "--d", "-a", "md5", tr.a)
	add("unique prefix ignore-missing", "--i", tr.a)
	add("unique prefix quiet", "--q", tr.a)
	add("unique prefix warn", "--w", tr.a)
	add("unique prefix length", "--l", "256", "-a", "blake2b", tr.a)
	add("flag rejects a glued value", "--raw=1", tr.a)
	add("debug rejects a glued value", "--debug=x", tr.a)
	add("untagged rejects a glued value", "--untagged=1", tr.a)
	add("help rejects a glued value", "--help=x")
	add("version rejects a glued value", "--version=x")
	add("operand then option", tr.a, "--untagged")
	cases = append(cases,
		invocation{name: "posix operand then option", args: []string{tr.a, "--untagged"}, env: []string{"POSIXLY_CORRECT=1"}},
		invocation{name: "posix option before the operand", args: []string{"--untagged", tr.a}, env: []string{"POSIXLY_CORRECT=1"}},
	)

	// Bare -t / -b, which are not in the help text and are options all
	// the same, and the refusal -t alone reaches.
	add("text alone", "-t", tr.a)
	add("long text alone", "--text", tr.a)
	add("binary alone", "-b", tr.a)
	add("long binary alone", "--binary", tr.a)
	add("binary cluster", "-bb", tr.a)
	add("text after untagged", "--untagged", "-t", tr.a)
	add("untagged after text", "-t", "--untagged", tr.a)
	add("tag after untagged after text", "-t", "--untagged", "--tag", tr.a)

	// The refusals, in the order the reference tests them: --raw with
	// --base64 before --text, --text before --zero, --zero before the
	// binary/text one, and the five check-only options last.
	add("base64 and raw", "--base64", "--raw", "-a", "md5", tr.a)
	add("raw and base64", "--raw", "--base64", "-a", "md5", tr.a)
	add("base64 and raw with text", "--raw", "--base64", "-t", tr.a)
	add("base64 and raw with a check-only option", "--raw", "--base64", "--ignore-missing", tr.a)
	add("base64 and raw with two files", "--base64", "--raw", tr.a, tr.empty)
	add("text before zero", "-z", "-t", "-c", tr.a)
	add("zero with check", "-t", "-z", "--untagged", "-c", "-a", "md5", tr.a)
	add("tag with check", "--tag", "-c", "-a", "md5", tr.a)
	add("zero before the binary refusal", "-z", "--tag", "-c", "-a", "md5", tr.a)
	add("binary with check", "-b", "-c", "-a", "md5", tr.a)
	add("text untagged with check", "-t", "--untagged", "-c", "-a", "md5", tr.a)
	add("raw with two files", "--raw", tr.a, tr.empty)
	add("raw with three files", "--raw", tr.a, tr.empty, tr.a)
	add("raw with a missing second file", "--raw", tr.a, tr.missing)
	add("raw with check and two files", "--raw", "-c", "-a", "md5", tr.a, tr.empty)
	add("raw with a check-only option and two files", "--raw", "--quiet", tr.a, tr.empty)
	add("raw with text and two files", "--raw", "-t", tr.a, tr.empty)

	// The five that are meaningful only when verifying, and the
	// three-valued setting behind --status / --warn / --quiet.
	add("ignore-missing without check", "--ignore-missing", tr.a)
	add("quiet without check", "--quiet", tr.a)
	add("status without check", "--status", tr.a)
	add("strict without check", "--strict", tr.a)
	add("warn without check", "-w", tr.a)
	add("long warn without check", "--warn", tr.a)
	add("quiet then warn", "--quiet", "-w", tr.a)
	add("warn then quiet", "-w", "--quiet", tr.a)
	add("status then quiet", "--status", "--quiet", tr.a)
	add("status warn quiet", "--status", "-w", "--quiet", tr.a)
	add("ignore-missing then status", "--ignore-missing", "--status", tr.a)
	add("status then ignore-missing", "--status", "--ignore-missing", tr.a)
	add("zero then ignore-missing", "-z", "--ignore-missing", tr.a)
	add("strict then quiet", "--strict", "--quiet", tr.a)

	// --check with an algorithm that has no checksum line.
	add("check crc", "-c", "-a", "crc", tr.a)
	add("check sysv", "-c", "-a", "sysv", tr.a)
	add("check bsd", "-c", "-a", "bsd", tr.a)
	add("check crc with zero", "-c", "-a", "crc", "-z", tr.a)
	add("zero then check crc", "-z", "-c", "-a", "crc", tr.a)
	add("check crc with tag", "--tag", "-a", "sysv", "-c", tr.a)
	add("check crc with base64 and raw", "--raw", "--base64", "-c", "-a", "crc", tr.a)
	return cases
}

// -l: the value rules at option time, and the maximum, which waits
// until `-a` is known.
func ckLengthCases(t *testing.T, tr ckTree) []invocation {
	t.Helper()
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args})
	}
	add("length 8", "-l", "8", "-a", "blake2b", tr.a)
	add("length 128", "-l", "128", "-a", "blake2b", tr.a)
	add("length 256", "-l", "256", "-a", "blake2b", tr.a)
	add("length 512", "-l", "512", "-a", "blake2b", tr.a)
	add("length 504", "-l", "504", "-a", "blake2b", tr.a)
	add("length 0 is the default", "-l", "0", "-a", "blake2b", tr.a)
	add("length 0 on the crc", "-l", "0", tr.a)
	add("length 0 on md5", "-l", "0", "-a", "md5", tr.a)
	add("length glued", "-l8", "-a", "blake2b", tr.a)
	add("length long", "--length=8", "-a", "blake2b", tr.a)
	add("length long separate", "--length", "8", "-a", "blake2b", tr.a)
	add("length needs an argument", "-l")
	add("length long needs an argument", "--length")
	add("length untagged", "-l", "8", "--untagged", "-a", "blake2b", tr.a)
	add("length base64", "-l", "8", "--base64", "-a", "blake2b", tr.a)
	add("length raw", "-l", "128", "--raw", "-a", "blake2b", tr.a)
	add("length zero terminator", "-l", "8", "-z", "-a", "blake2b", tr.a)
	add("length twice", "-l", "8", "-l", "256", "-a", "blake2b", tr.a)
	add("length before the algorithm", "-l", "256", "-a", "blake2b", tr.a)
	add("length after the algorithm", "-a", "blake2b", "-l", "256", tr.a)

	// Only blake2b takes a length, and the message has no `Try …` line.
	add("length on the crc", "-l", "256", tr.a)
	add("length on md5", "-l", "256", "-a", "md5", tr.a)
	add("length on sysv", "-l", "256", "-a", "sysv", tr.a)
	add("length with no operand", "-l", "256")
	add("length before text", "-l", "256", "-t", tr.a)
	add("length before zero and check", "-l", "256", "-z", "-c", tr.a)
	add("length before the check refusal", "-l", "256", "-c", "-a", "crc", tr.a)
	add("length before the mutual exclusion", "--raw", "--base64", "-l", "256", "-a", "md5", tr.a)

	// The value rules run where the option is parsed, so they win over
	// every refusal above.
	add("length not a multiple of eight", "-l", "4", "-a", "blake2b", tr.a)
	add("length not a multiple of eight on md5", "-l", "7", "-a", "md5", tr.a)
	add("length not a multiple of eight on the crc", "-l", "7", tr.a)
	add("length not a multiple with raw and base64", "--raw", "--base64", "-l", "7", "-a", "md5", tr.a)
	add("length too large", "-l", "520", "-a", "blake2b", tr.a)
	add("length far too large", "-l", "1024", "-a", "blake2b", tr.a)
	add("length too large and not a multiple", "-l", "516", "-a", "blake2b", tr.a)
	add("length too large on md5", "-l", "1024", "-a", "md5", tr.a)
	add("length too large with raw and base64", "-l", "1024", "-a", "blake2b", "--raw", "--base64", tr.a)
	add("length too large with text", "-l", "1024", "-a", "blake2b", "-t", tr.a)
	add("length too large with check", "-l", "1024", "-a", "blake2b", "-c", tr.a)
	add("length negative", "-l", "-8", "-a", "blake2b", tr.a)
	add("length empty", "-l", "", "-a", "blake2b", tr.a)
	add("length not a number", "-l", "abc", "-a", "blake2b", tr.a)
	add("length with trailing junk", "-l", "8x", "-a", "blake2b", tr.a)
	add("length hex", "-l", "0x10", "-a", "blake2b", tr.a)
	add("length leading zeros", "-l", "08", "-a", "blake2b", tr.a)
	add("length leading blank", "-l", " 8", "-a", "blake2b", tr.a)
	add("length plus sign", "-l", "+8", "-a", "blake2b", tr.a)
	add("length trailing blank", "-l", "8 ", "-a", "blake2b", tr.a)
	add("length overflows uintmax", "-l", "99999999999999999999", "-a", "blake2b", tr.a)
	add("length at uintmax", "-l", "18446744073709551615", "-a", "blake2b", tr.a)
	add("length past uintmax", "-l", "18446744073709551616", "-a", "blake2b", tr.a)
	add("length past uintmax on md5", "-l", "18446744073709551616", "-a", "md5", tr.a)
	add("length before an invalid option", "-l", "4", "-x", tr.a)
	add("invalid option before a length", "-x", "-l", "4", tr.a)
	return cases
}

// Check mode with `-a`: the reports, the summaries and their statuses,
// over each of the eight digests and both output forms.
func ckCheckCases(t *testing.T, tr ckTree) []invocation {
	t.Helper()
	line := ckLines(t, tr.dir, "c")
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args})
	}

	for _, a := range ckDigests() {
		tagged := line(refOutput(t, "cksum", "-a", a, tr.a, tr.empty))
		untagged := line(refOutput(t, "cksum", "--untagged", "-a", a, tr.a, tr.empty))
		b64 := line(refOutput(t, "cksum", "--base64", "-a", a, tr.a))
		b64Untagged := line(refOutput(t, "cksum", "--base64", "--untagged", "-a", a, tr.a))
		esc := line(refOutput(t, "cksum", "-a", a, tr.backslash, tr.newline, tr.carriage))
		escUntagged := line(refOutput(t, "cksum", "--untagged", "-a", a, tr.backslash, tr.newline))
		rawName := line(refOutput(t, "cksum", "-a", a, tr.raw))

		add(a+" check tagged", "-c", "-a", a, tagged)
		add(a+" check untagged", "-c", "-a", a, untagged)
		add(a+" check base64", "-c", "-a", a, b64)
		add(a+" check base64 untagged", "-c", "-a", a, b64Untagged)
		add(a+" check escaped names", "-c", "-a", a, esc)
		add(a+" check escaped untagged names", "-c", "-a", a, escUntagged)
		add(a+" check a name that is not valid UTF-8", "-c", "-a", a, rawName)
		add(a+" check tagged without an algorithm", "-c", tagged)
		add(a+" check untagged without an algorithm", "-c", untagged)
		add(a+" check base64 without an algorithm", "-c", b64)
		add(a+" check escaped names without an algorithm", "-c", esc)
		add(a+" check with the wrong algorithm", "-c", "-a", "sha1", tagged)
		add(a+" check with the wrong algorithm and warn", "-c", "-w", "-a", "sha1", tagged)
		add(a+" check quiet", "-c", "--quiet", "-a", a, tagged)
		add(a+" check status", "-c", "--status", "-a", a, tagged)
	}

	// One algorithm carries the shapes that do not vary with the digest.
	good := refOutput(t, "cksum", "-a", "md5", tr.a)
	goodUntagged := refOutput(t, "cksum", "--untagged", "-a", "md5", tr.a)
	zeros := strings.Repeat("0", 32)
	ok := line(good)
	fail := line("MD5 (" + tr.a + ") = " + zeros + "\n")
	miss := line("MD5 (" + tr.missing + ") = " + zeros + "\n")
	isdir := line("MD5 (" + tr.subdir + ") = " + zeros + "\n")
	mal := line("garbage\n")
	empty := line("")
	blank := line("\n\n")
	mix := line(good + "MD5 (" + tr.a + ") = " + zeros + "\ngarbage\n")
	all3 := line("garbage\nMD5 (" + tr.a + ") = " + zeros + "\nMD5 (" + tr.missing + ") = " + zeros + "\n" + good)
	two3 := line(strings.Repeat("garbage\n", 2) +
		strings.Repeat("MD5 ("+tr.a+") = "+zeros+"\n", 2) +
		"MD5 (" + tr.missing + ") = " + zeros + "\nMD5 (" + tr.missing + "2) = " + zeros + "\n" + good)
	missOk := line("MD5 (" + tr.missing + ") = " + zeros + "\n" + good)
	missFail := line("MD5 (" + tr.missing + ") = " + zeros + "\nMD5 (" + tr.a + ") = " + zeros + "\n")
	dirOk := line("MD5 (" + tr.subdir + ") = " + zeros + "\n" + good)
	comment := line("# a comment\n" + good)
	numbering := line("# a comment\n\ngarbage\n" + good)
	notComment := line("  # a comment\n" + good)
	blanks := line("\n" + good + "\n")
	crlf := line(strings.ReplaceAll(good, "\n", "\r\n"))
	noNewline := line(strings.TrimRight(good, "\n"))
	digestOfA := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(good), "MD5 ("+tr.a+") = "), "\n")
	dash := line("MD5 (-) = " + digestOfA + "\n")

	add("check ok", "-c", ok)
	add("check long", "--check", ok)
	add("check two checksum files", "-c", ok, mal)
	add("check three checksum files", "-c", two3, ok, all3)
	add("check mismatch", "-c", fail)
	add("check missing", "-c", miss)
	add("check a directory", "-c", isdir)
	add("check malformed", "-c", mal)
	add("check malformed with warn", "-c", "-w", mal)
	add("check empty file", "-c", empty)
	add("check blank lines only", "-c", blank)
	add("check mixed", "-c", mix)
	add("check mixed with warn", "-c", "-w", mix)
	add("check all three faults", "-c", all3)
	add("check all three faults twice", "-c", two3)
	add("check all three faults with warn", "-c", "-w", two3)
	add("check comment line", "-c", comment)
	add("check line numbers count skipped lines", "-c", "-w", numbering)
	add("check indented comment is not one", "-c", "-w", notComment)
	add("check blank lines are skipped", "-c", "-w", blanks)
	add("check CRLF line endings", "-c", crlf)
	add("check without a trailing newline", "-c", noNewline)
	add("check a missing checksum file", "-c", tr.missing)
	add("check a checksum file that is a directory", "-c", tr.subdir)
	add("check an empty operand", "-c", "")
	add("check with dashdash", "-c", "--", ok)
	cases = append(cases,
		invocation{name: "check a line naming stdin", args: []string{"-c", dash}, stdin: "hello\n"},
		invocation{name: "check from stdin", args: []string{"-c"}, stdin: good},
		invocation{name: "check from a dash operand", args: []string{"-c", "-"}, stdin: good},
		invocation{name: "check untagged from stdin with an algorithm", args: []string{"-c", "-a", "md5"}, stdin: goodUntagged},
		invocation{name: "check with stdout closed", args: []string{"-c", ok}, stdout: stdoutClosed},
		invocation{name: "check with stdout full", args: []string{"-c", ok}, stdout: stdoutFull},
		invocation{name: "check status with stdout closed", args: []string{"-c", "--status", ok}, stdout: stdoutClosed},
		invocation{name: "check mismatch with stdout closed", args: []string{"-c", fail}, stdout: stdoutClosed},
	)

	add("check quiet ok", "-c", "--quiet", ok)
	add("check quiet mixed", "-c", "--quiet", mix)
	add("check quiet missing", "-c", "--quiet", miss)
	add("check status ok", "-c", "--status", ok)
	add("check status mixed", "-c", "--status", mix)
	add("check status malformed", "-c", "--status", mal)
	add("check status missing", "-c", "--status", miss)
	add("check status with warn after", "-c", "--status", "-w", mix)
	add("check warn then status", "-c", "-w", "--status", mix)
	add("check quiet then status", "-c", "--quiet", "--status", ok)
	add("check strict ok", "-c", "--strict", ok)
	add("check strict malformed", "-c", "--strict", mix)
	add("check strict malformed with warn", "-c", "--strict", "-w", mix)
	add("check strict only malformed", "-c", "--strict", mal)
	add("check ignore-missing all missing", "-c", "--ignore-missing", miss)
	add("check ignore-missing with one ok", "-c", "--ignore-missing", missOk)
	add("check ignore-missing with one failure", "-c", "--ignore-missing", missFail)
	add("check ignore-missing does not skip a directory", "-c", "--ignore-missing", isdir)
	add("check ignore-missing a directory and an ok", "-c", "--ignore-missing", dirOk)
	add("check ignore-missing quiet", "-c", "--ignore-missing", "--quiet", missOk)
	add("check ignore-missing status", "-c", "--ignore-missing", "--status", miss)
	add("check every check-only option at once", "-c", "--ignore-missing", "--quiet", "--status", "-w", "--strict", ok)

	// --raw and --base64 are ignored when verifying, but --raw's
	// operand count is not.
	add("check with raw", "--raw", "-c", ok)
	add("check with base64", "--base64", "-c", ok)
	add("check with raw and one file", "--raw", "-c", "-a", "md5", ok)
	return cases
}

// Check mode WITHOUT `-a`: the tag chooses the algorithm, an untagged
// line has none, and the last tag recognised is what the next
// improperly-formatted line is reported as.
func ckDetectCases(t *testing.T, tr ckTree) []invocation {
	t.Helper()
	line := ckLines(t, tr.dir, "d")
	tags := ckTags()
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args})
	}

	var mixed string
	for _, a := range ckDigests() {
		mixed += refOutput(t, "cksum", "-a", a, tr.a)
	}
	mixedFile := line(mixed)
	add("detect every algorithm in one file", "-c", mixedFile)
	add("detect every algorithm with warn", "-c", "-w", mixedFile)
	add("detect every algorithm under md5", "-c", "-a", "md5", mixedFile)
	add("detect every algorithm under md5 with warn", "-c", "-w", "-a", "md5", mixedFile)
	add("detect every algorithm under blake2b with warn", "-c", "-w", "-a", "blake2b", mixedFile)

	// An untagged line has no algorithm to be read with, before or
	// after a tagged one.
	untagged := refOutput(t, "cksum", "--untagged", "-a", "md5", tr.a)
	tagged := refOutput(t, "cksum", "-a", "md5", tr.a)
	b2Untagged := refOutput(t, "cksum", "--untagged", "-a", "blake2b", tr.a)
	b2Tagged := refOutput(t, "cksum", "-a", "blake2b", tr.a)
	add("detect untagged alone", "-c", "-w", line(untagged))
	add("detect untagged then tagged", "-c", "-w", line(untagged+tagged))
	add("detect tagged then untagged", "-c", "-w", line(tagged+untagged))
	add("detect blake2b untagged then tagged", "-c", "-w", line(b2Untagged+b2Tagged))
	add("detect blake2b tagged then untagged", "-c", "-w", line(b2Tagged+b2Untagged))
	add("detect untagged with an algorithm", "-c", "-w", "-a", "md5", line(untagged+tagged))

	// The name in the diagnostic is the last tag RECOGNISED, which a
	// line may recognise and then fail to parse.
	for _, a := range ckDigests() {
		tag := tags[a]
		add("detect "+a+" tag with a bad digest", "-c", "-w", line(tag+" ("+tr.a+") = nope\ngarbage\n"))
		add("detect "+a+" tag then garbage", "-c", "-w", line(refOutput(t, "cksum", "-a", a, tr.a)+"garbage\n"))
		add("detect "+a+" tag lowercased", "-c", "-w", line(strings.ToLower(tag)+" ("+tr.a+") = nope\n"))
		add("detect "+a+" tag uppercased", "-c", "-w", line(strings.ToUpper(tag)+" ("+tr.a+") = nope\n"))
	}
	// Tags that are not check algorithms leave the name where it was.
	add("detect a CRC tag", "-c", "-w", line("CRC ("+tr.a+") = 1234\n"))
	add("detect a SYSV tag", "-c", "-w", line("SYSV ("+tr.a+") = 1234\n"))
	add("detect a BSD tag", "-c", "-w", line("BSD ("+tr.a+") = 1234\n"))
	add("detect garbage only", "-c", "-w", line("garbage\n"))
	add("detect garbage then ok", "-c", "-w", line("garbage\n"+tagged))
	add("detect ok then garbage", "-c", "-w", line(tagged+"garbage\n"))
	add("detect garbage then ok strict", "-c", "-w", "--strict", line("garbage\n"+tagged))
	add("detect ok then garbage strict", "-c", "--strict", line(tagged+"garbage\n"))
	add("detect blank lines then garbage", "-c", "-w", line("\n\n  \n"+tagged))

	// A tag whose name matches and whose next byte does not: the
	// algorithm is chosen off the name alone, so this is an improperly
	// formatted MD5 line and not a CRC one — but a backslash after the
	// name matches no algorithm at all.
	add("detect a tag with three spaces", "-c", "-w", line("MD5   ("+tr.a+") = nope\n"))
	add("detect a tag with a tab", "-c", "-w", line("MD5\t("+tr.a+") = nope\n"))
	add("detect a tag with a tab then a space", "-c", "-w", line("MD5\t ("+tr.a+") = nope\n"))
	add("detect a tag with a space then a tab", "-c", "-w", line("MD5 \t("+tr.a+") = nope\n"))
	add("detect a tag followed by a backslash", "-c", "-w", line(`MD5\t(`+tr.a+") = nope\n"))
	add("detect a tag followed by a letter", "-c", "-w", line("MD5x ("+tr.a+") = nope\n"))
	return cases
}

// The check-line grammar: the two shapes, the separator and length
// rules, base64, and the `-NNN` length every algorithm takes here.
func ckGrammarCases(t *testing.T, tr ckTree) []invocation {
	t.Helper()
	line := ckLines(t, tr.dir, "g")
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args})
	}
	// The true digests of `a`, so a grammar case can MATCH rather than
	// only be a well-formed miss.
	md5 := strings.Fields(refOutput(t, "cksum", "--untagged", "-a", "md5", tr.a))[0]
	b2 := strings.Fields(refOutput(t, "cksum", "--untagged", "-a", "blake2b", tr.a))[0]
	md5b64 := strings.Fields(refOutput(t, "cksum", "--base64", "--untagged", "-a", "md5", tr.a))[0]
	zeros := strings.Repeat("0", 32)
	name := tr.a

	// The untagged shape, which needs `-a` to be read at all.
	u := func(n, content string) {
		add("grammar "+n, "-c", "-w", "-a", "md5", line(content))
	}
	u("one blank", md5+" "+name+"\n")
	u("two blanks", md5+"  "+name+"\n")
	u("three blanks", md5+"   "+name+"\n")
	u("star", md5+" *"+name+"\n")
	u("two stars", md5+" **"+name+"\n")
	u("blank star", md5+"  *"+name+"\n")
	u("tab", md5+"\t"+name+"\n")
	u("tab blank", md5+"\t "+name+"\n")
	u("blank tab", md5+" \t"+name+"\n")
	u("no separator", md5+name+"\n")
	u("digest only", md5+"\n")
	u("digest and one blank", md5+" \n")
	u("digest and two blanks", md5+"  \n")
	u("digest and three blanks", md5+"   \n")
	u("digest blank star", md5+" *\n")
	u("digest two tabs", md5+"\t\t\n")
	u("name with a trailing blank", md5+"  "+name+" \n")
	u("name with an inner blank", md5+"  "+tr.spaced+"\n")
	u("leading blank", " "+md5+"  "+name+"\n")
	u("leading tab", "\t"+md5+"  "+name+"\n")
	u("leading backslash", "\\"+md5+"  "+name+"\n")
	u("leading blanks then backslash", "  \\"+md5+"  "+name+"\n")
	u("backslash escape", "\\"+zeros+`  na\\me`+"\n")
	u("newline escape", "\\"+zeros+`  na\nme`+"\n")
	u("carriage return escape", "\\"+zeros+`  na\rme`+"\n")
	u("tab escape is invalid", "\\"+zeros+`  na\tme`+"\n")
	u("zero escape is invalid", "\\"+zeros+`  na\0me`+"\n")
	u("trailing lone backslash", "\\"+zeros+`  name\`+"\n")
	u("escaped with no backslash in the name", "\\"+zeros+"  plain\n")
	u("unescaped backslash in the name", zeros+`  na\me`+"\n")
	u("uppercase digest", strings.ToUpper(md5)+"  "+name+"\n")
	u("mixed case digest", strings.ToUpper(md5[:4])+md5[4:]+"  "+name+"\n")
	u("one digit too long", zeros+"0  "+name+"\n")
	u("one digit too short", zeros[1:]+"  "+name+"\n")
	u("a non-hex digit", "z"+zeros[1:]+"  "+name+"\n")
	u("a non-hex digit at the end", zeros[1:]+"z  "+name+"\n")
	u("base64 digest", md5b64+"  "+name+"\n")
	u("base64 digest with a star", md5b64+" *"+name+"\n")
	u("base64 digest with a tab", md5b64+"\t"+name+"\n")
	u("base64 digest only", md5b64+"\n")
	u("base64 digest and one blank", md5b64+" \n")
	u("base64 digest one pad short", md5b64[:len(md5b64)-1]+"  "+name+"\n")
	u("base64 digest one pad long", md5b64+"=  "+name+"\n")
	u("base64 digest unpadded", strings.TrimRight(md5b64, "=")+"  "+name+"\n")
	u("base64 digest with an invalid byte", "!"+md5b64[1:]+"  "+name+"\n")
	u("base64 digest with a slash", strings.Replace(md5b64, md5b64[:1], "/", 1)+"  "+name+"\n")
	u("NUL first", "\x00"+zeros+"  "+name+"\n")
	u("NUL after the name", zeros+"  a\x00b\n")
	u("NUL where the name would be", zeros+"  \x00\n")
	u("NUL past a marker", zeros+"  \x00x\n")

	// A blake2b untagged line takes its length from the hex run, and a
	// run whose length is also a base64 length is read as base64 — so
	// four and eight hex digits are improperly formatted here.
	b := func(n, content string) {
		add("grammar blake2b "+n, "-c", "-w", "-a", "blake2b", line(content))
	}
	b("full hex run", b2+"  "+name+"\n")
	b("two-digit hex run", strings.Fields(refOutput(t, "cksum", "--untagged", "-a", "blake2b", "-l", "8", tr.a))[0]+"  "+name+"\n")
	b("one-digit hex run", "a  "+name+"\n")
	b("three-digit hex run", "aaa  "+name+"\n")
	b("four-digit hex run", "aaaa  "+name+"\n")
	b("six-digit hex run", "aaaaaa  "+name+"\n")
	b("eight-digit hex run", "aaaaaaaa  "+name+"\n")
	b("twelve-digit hex run", "aaaaaaaaaaaa  "+name+"\n")
	b("hex run past the maximum", strings.Repeat("a", 130)+"  "+name+"\n")
	b("hex run and one blank", "aa \n")
	b("hex run followed by junk", "aax  "+name+"\n")
	b("base64 digest is not a hex run", strings.Fields(refOutput(t, "cksum", "--base64", "--untagged", "-a", "blake2b", tr.a))[0]+"  "+name+"\n")

	// The tagged shape, which needs no `-a`.
	g := func(n, content string) {
		add("grammar tagged "+n, "-c", "-w", line(content))
	}
	g("plain", "MD5 ("+name+") = "+md5+"\n")
	g("no space", "MD5("+name+") = "+md5+"\n")
	g("two spaces", "MD5  ("+name+") = "+md5+"\n")
	g("three spaces", "MD5   ("+name+") = "+md5+"\n")
	g("tab", "MD5\t("+name+") = "+md5+"\n")
	g("tab then space", "MD5\t ("+name+") = "+md5+"\n")
	g("leading blanks", "   MD5 ("+name+") = "+md5+"\n")
	g("no space around equals", "MD5 ("+name+")="+md5+"\n")
	g("tabs around equals", "MD5 ("+name+")\t=\t"+md5+"\n")
	g("no equals", "MD5 ("+name+") "+md5+"\n")
	g("no closing paren", "MD5 ("+name+" = "+md5+"\n")
	g("empty name", "MD5 () = "+md5+"\n")
	g("name with a paren", "MD5 (a)b) = "+md5+"\n")
	g("trailing junk", "MD5 ("+name+") = "+md5+"x\n")
	g("trailing blank", "MD5 ("+name+") = "+md5+" \n")
	g("short digest", "MD5 ("+name+") = "+md5[1:]+"\n")
	g("escaped", "\\MD5 ("+`na\\me`+") = "+zeros+"\n")
	g("word then digest", "MD5"+md5+"  "+name+"\n")
	g("base64 digest", "MD5 ("+name+") = "+md5b64+"\n")
	g("base64 digest one pad short", "MD5 ("+name+") = "+md5b64[:len(md5b64)-1]+"\n")
	g("base64 digest one pad long", "MD5 ("+name+") = "+md5b64+"=\n")
	g("base64 digest with an invalid byte", "MD5 ("+name+") = !"+md5b64[1:]+"\n")

	// `-NNN` is not b2sum's alone here: every algorithm takes one, and
	// a shorter one names a PREFIX of the digest.
	g("md5 length 128", "MD5-128 ("+name+") = "+md5+"\n")
	g("md5 length 128 no space", "MD5-128("+name+") = "+md5+"\n")
	g("md5 length 64 with a full digest", "MD5-64 ("+name+") = "+md5+"\n")
	g("md5 length 64 with a prefix", "MD5-64 ("+name+") = "+md5[:16]+"\n")
	g("md5 length 64 base64", "MD5-64 ("+name+") = "+strings.Fields(refOutput(t, "cksum", "--base64", "--untagged", "-a", "md5", tr.a))[0]+"\n")
	g("md5 length 256", "MD5-256 ("+name+") = "+md5+"\n")
	g("md5 length 0", "MD5-0 ("+name+") = "+md5+"\n")
	g("md5 length not a multiple of eight", "MD5-4 ("+name+") = a\n")
	g("sha256 length 128 with an md5 digest", "SHA256-128 ("+name+") = "+md5+"\n")
	g("sm3 length 128 with an md5 digest", "SM3-128 ("+name+") = "+md5+"\n")
	g("length with no digits", "MD5- ("+name+") = "+md5+"\n")
	g("negative length", "MD5--8 ("+name+") = aa\n")
	g("octal length", "MD5-020 ("+name+") = "+md5[:4]+"\n")
	g("hex length", "MD5-0x10 ("+name+") = "+md5[:4]+"\n")
	g("length with leading zeros", "MD5-08 ("+name+") = "+md5[:2]+"\n")
	g("length with a plus", "MD5-+8 ("+name+") = "+md5[:2]+"\n")

	// BLAKE2b's own lengths, which the tag declares and `-l` does not
	// override. Eight, sixteen and thirty-two bits are the three whose
	// base64 form cannot verify — a digest that small is smaller than
	// the room its own base64 needs — and the reference reports the
	// mismatch rather than a malformed line.
	for _, bits := range []int{8, 16, 24, 32, 40, 48, 56, 64, 128, 256, 512} {
		s := strconv.Itoa(bits)
		hexD := strings.Fields(refOutput(t, "cksum", "--untagged", "-a", "blake2b", "-l", s, tr.a))[0]
		b64Line := strings.TrimSuffix(refOutput(t, "cksum", "--base64", "-a", "blake2b", "-l", s, tr.a), "\n")
		b64D := b64Line[strings.LastIndex(b64Line, " = ")+3:]
		g("blake2b "+s+" hex", "BLAKE2b-"+s+" ("+name+") = "+hexD+"\n")
		g("blake2b "+s+" base64", "BLAKE2b-"+s+" ("+name+") = "+b64D+"\n")
		add("grammar blake2b "+s+" hex with a length", "-c", "-w", "-l", "512", "-a", "blake2b", line("BLAKE2b-"+s+" ("+name+") = "+hexD+"\n"))
	}
	g("blake2b length too large", "BLAKE2b-1000 ("+name+") = "+b2+"\n")
	g("blake2b length that does not match", "BLAKE2b-256 ("+name+") = "+b2+"\n")
	g("blake2b default tag", "BLAKE2b ("+name+") = "+b2+"\n")
	g("blake2b tag with a tab", "BLAKE2b\t("+name+") = "+b2+"\n")
	g("blake2b tag with two spaces", "BLAKE2b  ("+name+") = "+b2+"\n")
	g("blake2b tag with three spaces", "BLAKE2b   ("+name+") = "+b2+"\n")
	g("blake2b 512 tag with two spaces", "BLAKE2b-512  ("+name+") = "+b2+"\n")
	return cases
}

func TestCksumParity(t *testing.T) {
	requireParity(t, "cksum", cksumCases(t))
}

func TestCksumHelpVersion(t *testing.T) {
	requireHelp(t, "cksum", []string{"--help"}, 0)
	requireHelp(t, "cksum", []string{"--he"}, 0)
	requireVersion(t, "cksum", []string{"--version"}, 0)
	requireVersion(t, "cksum", []string{"--vers"}, 0)
	// getopt permutes, so an operand does not end the option scan.
	requireHelp(t, "cksum", []string{"x", "--help"}, 0)
	requireVersion(t, "cksum", []string{"x", "--version"}, 0)
}
