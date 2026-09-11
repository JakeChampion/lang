package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// splitSeed fills a case's own working directory with the inputs the
// corpus names. Every case runs in a fresh copy of this, per side, and
// the TREE each side leaves behind is compared as well as the streams —
// split writes almost nothing to stdout, so the files are the output.
func splitSeed(t *testing.T, dir string) {
	t.Helper()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Ten two-byte records, 20 bytes: every `-n` division of it lands on
	// a different mix of whole and split records.
	write("in", "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n")
	// Records of 5, 3, 2, 14 and 2 bytes — the 14 is longer than the
	// `-C` budgets below, which is the only way a `-C` piece ends
	// mid-record.
	write("uneven", "aaaa\nbb\nc\nddddddddddddd\ne\n")
	// No trailing separator: the last record is unterminated, which is
	// what stops `-n l/N` running away when N exceeds the byte count.
	write("nonl", "a\nb\nc")
	write("empty", "")
	// NUL-separated, for `-t '\0'`.
	write("nul", "a\x00b\x00c\x00")
	// 800 bytes: `-b 1` over this crosses the two-letter suffix space
	// (650 names) into the widened four-letter one.
	write("big", strings.Repeat("q", 800))
	write("f name", "x\ny\n")
	write("f'n", "x\ny\n")
	// A name that is not valid UTF-8: it reaches the diagnostics as
	// bytes, so nothing may re-encode it.
	write("na\xffme", "x\ny\n")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// splitCase is one invocation in split's own working directory.
func splitCase(name string, args ...string) invocation {
	return invocation{name: name, args: args, seedTree: splitSeed}
}

// splitStdin is splitCase with something on stdin — a PIPE, so the
// input is not seekable and `-n` has to learn its size another way.
func splitStdin(name, stdin string, args ...string) invocation {
	return invocation{name: name, args: args, stdin: stdin, seedTree: splitSeed}
}

func init() {
	registerCorpus("split", splitCases)
}

// splitCases is split(1)'s corpus.
//
// Three areas carry most of it. The suffix counter widens rather than
// wrapping, and the widening reserves the alphabet's last character, so
// `xyz` is followed by `xzaaa` and only 650 two-letter names exist. `-n`
// divides by byte count, by record (`l/`) or round robin (`r/`), each
// with a `K/N` form that writes one share to stdout instead of files.
// And the diagnostics are glibc's getopt plus split's own, including the
// two that read oddly and are GNU's wording rather than a slip here:
// `-C` reports "invalid number of lines" for a byte count, and a
// negative `-a` is `Numerical result out of range` while a huge one is
// `Value too large for defined data type`.
func splitCases(*testing.T) []invocation {
	return []invocation{
		// ---- defaults and the four ways to cut ----
		splitCase("default 1000 lines", "in"),
		splitCase("lines", "-l", "3", "in"),
		splitCase("lines glued", "-l3", "in"),
		splitCase("lines long", "--lines=3", "in"),
		splitCase("lines one", "-l", "1", "in"),
		splitCase("lines more than the file", "-l", "100", "in"),
		splitCase("bytes", "-b", "4", "in"),
		splitCase("bytes one", "-b", "1", "in"),
		splitCase("bytes long", "--bytes=4", "in"),
		splitCase("bytes with a suffix", "-b", "1K", "in"),
		splitCase("bytes with a binary suffix", "-b", "1KiB", "in"),
		splitCase("bytes more than the file", "-b", "100", "in"),
		splitCase("line bytes", "-C", "4", "in"),
		splitCase("line bytes long", "--line-bytes=4", "in"),
		splitCase("line bytes one", "-C", "1", "in"),
		splitCase("line bytes uneven", "-C", "5", "uneven"),
		splitCase("line bytes uneven small", "-C", "3", "uneven"),
		splitCase("line bytes uneven large", "-C", "10", "uneven"),
		splitCase("line bytes more than the file", "-C", "100", "in"),

		// ---- -n, every form ----
		splitCase("chunks one", "-n", "1", "in"),
		splitCase("chunks three", "-n", "3", "in"),
		splitCase("chunks seven", "-n", "7", "in"),
		splitCase("chunks equal to the size", "-n", "20", "in"),
		splitCase("chunks past the size", "-n", "30", "in"),
		splitCase("chunks long", "--number=3", "in"),
		splitCase("chunks uneven", "-n", "4", "uneven"),
		splitCase("chunks of an empty file", "-n", "3", "empty"),
		splitCase("chunks of an unterminated file", "-n", "3", "nonl"),
		splitCase("chunk to stdout", "-n", "2/3", "in"),
		splitCase("first chunk to stdout", "-n", "1/3", "in"),
		splitCase("last chunk to stdout", "-n", "3/3", "in"),
		splitCase("line chunks", "-n", "l/3", "in"),
		splitCase("line chunks one", "-n", "l/1", "in"),
		splitCase("line chunks four", "-n", "l/4", "in"),
		splitCase("line chunks six", "-n", "l/6", "in"),
		splitCase("line chunks equal to the size", "-n", "l/20", "in"),
		splitCase("line chunks uneven", "-n", "l/5", "uneven"),
		splitCase("line chunks of an empty file", "-n", "l/4", "empty"),
		splitCase("line chunks of an unterminated file", "-n", "l/10", "nonl"),
		// N past the byte count with a terminated last record is GNU's
		// violated `assert (n <= file_size)`: pieces until the suffixes
		// run out. `-a 1` bounds it to 26 names.
		splitCase("line chunks past the size", "-a", "1", "-n", "l/11", "in"),
		splitCase("line chunk to stdout", "-n", "l/2/3", "in"),
		splitCase("line chunk to stdout past the size", "-n", "l/2/30", "in"),
		splitCase("round robin", "-n", "r/3", "in"),
		splitCase("round robin one", "-n", "r/1", "in"),
		splitCase("round robin more files than records", "-n", "r/20", "in"),
		splitCase("round robin unterminated", "-n", "r/2", "nonl"),
		splitCase("round robin empty", "-n", "r/3", "empty"),
		splitCase("round robin to stdout", "-n", "r/2/3", "in"),
		splitCase("round robin to stdout unterminated", "-n", "r/1/2", "nonl"),

		// ---- -e ----
		splitCase("elide", "-e", "-n", "20", "in"),
		splitCase("elide round robin", "-e", "-n", "r/20", "in"),
		splitCase("elide reuses the names it skipped", "-e", "-n", "l/6", "in"),
		splitCase("elide with nothing to elide", "-e", "-n", "3", "in"),

		// ---- suffixes ----
		splitCase("suffix length one", "-a", "1", "-l", "3", "in"),
		splitCase("suffix length three", "-a", "3", "-l", "3", "in"),
		splitCase("suffix length long", "--suffix-length=3", "-l", "3", "in"),
		splitCase("suffix length zero is the default", "-a", "0", "-l", "3", "in"),
		splitCase("suffix length exhausted", "-a", "1", "-b", "1", "big"),
		splitCase("suffix widens past two letters", "-b", "1", "big"),
		splitCase("suffix widens with three declared", "-a", "3", "-b", "1", "big"),
		splitCase("numeric suffixes", "-d", "-l", "3", "in"),
		splitCase("numeric suffixes widen", "-d", "-b", "1", "big"),
		splitCase("numeric suffixes long", "--numeric-suffixes", "-l", "3", "in"),
		splitCase("numeric suffixes from", "--numeric-suffixes=5", "-l", "3", "in"),
		splitCase("numeric suffixes from does not widen", "--numeric-suffixes=5", "-b", "1", "big"),
		splitCase("numeric suffixes from with leading zeros", "--numeric-suffixes=007", "-a", "2", "-l", "3", "in"),
		splitCase("numeric suffixes from empty", "--numeric-suffixes=", "-l", "3", "in"),
		splitCase("hex suffixes", "-x", "-l", "3", "in"),
		splitCase("hex suffixes widen", "-x", "-b", "1", "big"),
		splitCase("hex suffixes long", "--hex-suffixes", "-l", "3", "in"),
		// A FROM holding a hex LETTER is not here: GNU 9.4 seeds its
		// counter with `FROM[i] - '0'` and then indexes its alphabet out
		// of bounds, so the names it produces are whatever follows that
		// string in the binary. docs/COREUTILS.md records it.
		splitCase("hex suffixes from digits", "--hex-suffixes=10", "-l", "3", "in"),
		splitCase("last suffix kind wins", "-d", "-x", "-l", "3", "in"),
		splitCase("additional suffix", "--additional-suffix=.txt", "-l", "3", "in"),
		splitCase("additional suffix empty", "--additional-suffix=", "-l", "3", "in"),
		splitCase("additional suffix with widening", "--additional-suffix=.q", "-b", "1", "big"),
		splitCase("suffix sized for the chunk count", "-n", "700", "big"),
		splitCase("suffix exactly sized for the chunk count", "-n", "676", "big"),
		splitCase("suffix one too small for the chunk count", "-n", "677", "big"),
		splitCase("declared suffix large enough for the chunks", "-a", "5", "-n", "700", "big"),

		// ---- separators ----
		splitCase("separator", "-t", "b", "-l", "2", "in"),
		splitCase("separator long", "--separator=b", "-l", "2", "in"),
		splitCase("separator nul", "-t", `\0`, "-l", "2", "nul"),
		splitCase("separator absent from the input", "-t", "Z", "-l", "2", "in"),
		splitCase("separator repeated identically", "-t", "b", "-t", "b", "-l", "2", "in"),
		splitCase("separator with line chunks", "-t", "b", "-n", "l/3", "in"),
		splitCase("separator with round robin", "-t", "b", "-n", "r/3", "in"),

		// ---- --verbose, -u, the prefix ----
		splitCase("verbose", "--verbose", "-l", "3", "in"),
		splitCase("verbose chunks", "--verbose", "-n", "4", "in"),
		splitCase("verbose elided chunks", "--verbose", "-e", "-n", "20", "in"),
		splitCase("verbose round robin", "--verbose", "-n", "r/4", "in"),
		splitCase("verbose with a chunk on stdout", "--verbose", "-n", "2/3", "in"),
		splitCase("unbuffered", "-u", "-l", "3", "in"),
		splitCase("unbuffered round robin", "-u", "-n", "r/2/3", "in"),
		splitCase("prefix", "-l", "3", "in", "piece-"),
		splitCase("prefix empty", "-l", "3", "in", ""),
		splitCase("prefix into a subdirectory", "-l", "3", "in", "d/p"),

		// ---- operands ----
		splitStdin("stdin by default", "1\n2\n3\n4\n", "-l", "2"),
		splitStdin("stdin named", "1\n2\n3\n4\n", "-l", "2", "-"),
		splitStdin("stdin bytes", "12345", "-b", "2"),
		splitStdin("stdin line bytes", "1\n22\n333\n", "-C", "4"),
		splitStdin("stdin chunks", "1\n2\n3\n4\n", "-n", "3"),
		splitStdin("stdin line chunks", "1\n2\n3\n4\n", "-n", "l/3"),
		splitStdin("stdin round robin", "1\n2\n3\n4\n", "-n", "r/3"),
		splitStdin("stdin chunk to stdout", "1\n2\n3\n4\n", "-n", "2/3"),
		splitStdin("stdin empty", "", "-l", "2"),
		splitStdin("stdin empty chunks", "", "-n", "3"),
		splitStdin("stdin unterminated", "a\nb", "-l", "1"),
		// Past one read block, so `-n` cannot answer from what it has
		// already read and spools the input to a temporary file.
		splitStdin("stdin chunks past one block", strings.Repeat("0123456789\n", 20000), "-n", "3"),
		splitStdin("stdin line chunks past one block", strings.Repeat("0123456789\n", 20000), "-n", "l/3"),
		splitStdin("stdin chunk to stdout past one block", strings.Repeat("0123456789\n", 20000), "-n", "2/3"),
		splitStdin("stdin round robin past one block", strings.Repeat("0123456789\n", 20000), "-n", "r/3"),
		splitStdin("stdin lines past one block", strings.Repeat("0123456789\n", 20000), "-l", "5000"),
		splitCase("double dash", "-l", "3", "--", "in"),
		splitCase("double dash alone", "-l", "3", "--"),
		splitCase("operand with a space", "-l", "1", "f name"),
		splitCase("operand with a quote", "-l", "1", "f'n"),
		splitCase("operand that is not utf-8", "-l", "1", "na\xffme"),
		splitCase("operand empty", "-l", "1", ""),
		splitCase("operand missing", "-l", "2", "nosuch"),
		splitCase("operand is a directory", "-l", "2", "d"),
		splitCase("extra operand", "in", "p", "q"),
		splitCase("prefix cannot be created", "-l", "2", "in", "nodir/p"),

		// ---- number faults ----
		splitCase("lines zero", "-l", "0", "in"),
		splitCase("lines not a number", "-l", "x", "in"),
		splitCase("lines empty", "-l", "", "in"),
		splitCase("lines negative", "-l", "-5", "in"),
		splitCase("lines take no suffix", "-l", "1K", "in"),
		splitCase("lines past uintmax saturate", "-l", "99999999999999999999999", "in"),
		splitCase("bytes zero", "-b", "0", "in"),
		splitCase("bytes not a number", "-b", "1x", "in"),
		splitCase("bytes negative", "-b", "-1", "in"),
		splitCase("bytes plus", "-b", "+5", "in"),
		splitCase("bytes leading blank", "-b", " 5", "in"),
		splitCase("bytes past uintmax saturate", "-b", "99999999999999999999999", "in"),
		splitCase("line bytes zero says lines", "-C", "0", "in"),
		splitCase("line bytes not a number says lines", "-C", "x", "in"),
		splitCase("line bytes empty says lines", "-C", "", "in"),
		splitCase("chunks zero", "-n", "0", "in"),
		splitCase("chunks not a number", "-n", "x", "in"),
		splitCase("chunks empty", "-n", "", "in"),
		splitCase("chunks take no suffix", "-n", "1K", "in"),
		splitCase("chunk number too large", "-n", "6/3", "in"),
		splitCase("chunk number zero", "-n", "0/3", "in"),
		splitCase("chunk number with a style", "-n", "l/4/3", "in"),
		splitCase("chunk number round robin zero", "-n", "r/0/3", "in"),
		splitCase("chunks with a bare style", "-n", "l", "in"),
		splitCase("chunks with an empty style body", "-n", "l/", "in"),
		splitCase("chunks with a leading slash", "-n", "/3", "in"),
		splitCase("chunks with a trailing slash", "-n", "3/", "in"),
		splitCase("chunks with two slashes", "-n", "2/3/4", "in"),
		splitCase("chunks with a style and two slashes", "-n", "l/1/2/3", "in"),
		splitCase("chunks with a style inside", "-n", "r/l/3", "in"),
		splitCase("suffix length not a number", "-a", "x", "in"),
		splitCase("suffix length empty", "-a", "", "in"),
		splitCase("suffix length negative", "-a", "-1", "in"),
		splitCase("suffix length negative after a blank", "-a", " -1", "in"),
		splitCase("suffix length negative and not a number", "-a", "-x", "in"),
		splitCase("suffix length with a trailing letter", "-a", "1x", "in"),
		splitCase("suffix length past uintmax", "-a", "99999999999999999999999", "in"),
		splitCase("suffix length too small for the chunks", "-a", "2", "-n", "700", "in"),
		splitCase("numeric start not a number", "--numeric-suffixes=abc", "in"),
		splitCase("numeric start negative", "--numeric-suffixes=-1", "in"),
		splitCase("numeric start too large", "--numeric-suffixes=995", "in"),
		splitCase("hex start not hexadecimal", "--hex-suffixes=G", "in"),
		splitCase("hex start uppercase", "--hex-suffixes=A", "in"),
		splitCase("hex start too large", "--hex-suffixes=111", "-a", "2", "in"),

		// ---- other faults ----
		splitCase("two ways", "-b", "5", "-l", "5", "in"),
		splitCase("two ways reversed", "-l", "5", "-b", "5", "in"),
		splitCase("two ways with chunks", "-b", "5", "-n", "3", "in"),
		splitCase("two ways with line bytes", "-n", "3", "-C", "5", "in"),
		splitCase("the same way twice", "-l", "5", "-l", "6", "in"),
		splitCase("separator empty", "-t", "", "-l", "2", "in"),
		splitCase("separator multi character", "-t", "bb", "-l", "2", "in"),
		splitCase("separator escaped newline is two characters", "-t", `\n`, "-l", "2", "in"),
		splitCase("separator contradicted", "-t", "a", "-t", "b", "-l", "2", "in"),
		splitCase("additional suffix with a slash", "--additional-suffix=a/b", "in"),

		// ---- getopt ----
		splitCase("ambiguous s", "--s", "in"),
		splitCase("ambiguous n", "--n", "in"),
		splitCase("ambiguous h", "--h", "in"),
		splitCase("ambiguous v", "--v", "in"),
		splitCase("ambiguous nu", "--nu", "in"),
		splitCase("unique prefix e", "--e", "-l", "3", "in"),
		splitCase("unique prefix hex", "--hex", "-l", "3", "in"),
		splitCase("unique prefix su takes the next token", "--su", "in"),
		splitCase("long lines wants a value", "--lines", "in"),
		splitCase("short a wants a value", "-a"),
		splitCase("long filter wants a value", "--filter"),
		splitCase("verbose allows no value", "--verbose=x", "in"),
		splitCase("help allows no value", "--help=x", "in"),
		splitCase("invalid short option", "-Q", "in"),
		splitCase("unrecognized long option", "--foo=bar", "in"),
		splitCase("cluster", "-el", "3", "in"),
		splitCase("options after the operand are permuted", "in", "-l", "3"),
		{name: "posixly correct stops at the operand", args: []string{"-l", "2", "-", "-e"}, stdin: "1\n2\n", env: []string{"POSIXLY_CORRECT=1"}, seedTree: splitSeed},
	}
}

func TestSplitParity(t *testing.T) {
	requireParity(t, "split", splitCases(t))
}

func TestSplitHelpVersion(t *testing.T) {
	requireHelp(t, "split", []string{"--help"}, 0)
	requireHelp(t, "split", []string{"--hel"}, 0)
	requireHelp(t, "split", []string{"--help", "ignored"}, 0)
	requireVersion(t, "split", []string{"--version"}, 0)
	requireVersion(t, "split", []string{"--vers"}, 0)
}
