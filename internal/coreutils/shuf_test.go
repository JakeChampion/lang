package coreutils

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func shufFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// shufSource writes a random-source fixture and returns the whole option
// that names it, so a case carries `count` rather than a path spelled out
// with its option again.
func shufSource(t *testing.T, dir, name string, bytes []byte) string {
	t.Helper()
	return "--random-source=" + shufFile(t, dir, name, bytes)
}

// shufLCG is a deterministic byte stream with no structure: a source of
// zeros drives every draw to its lowest value and a counting source
// climbs, so neither exercises a rejection the way arbitrary bytes do.
func shufLCG(n int) []byte {
	out := make([]byte, n)
	x := uint32(12345)
	for i := range out {
		x = x*1664525 + 1013904223
		out[i] = byte(x >> 24)
	}
	return out
}

func init() {
	registerCorpus("shuf", shufCases)
}

// shufCases is shuf(1)'s corpus.
//
// Everything here is ORACLE-gated — GNU and Fern run the same argv and
// the bytes are diffed — and that is only possible because
// `--random-source=FILE` makes the output a function of that file. Every
// case whose stdout depends on a draw names one. What each source is for:
//
//   - zeros: every draw takes its lowest value, so the permutation is the
//     identity and the byte COUNT is what is being compared.
//   - ff: every draw of a range that is not a power of two is rejected
//     and retried, so the source is consumed without end — which is how
//     the rejection loop itself is pinned, as an `end of file`.
//   - count and lcg: arbitrary bytes, where the answer is the whole
//     state machine — how many bytes a draw takes, what the leftover
//     entropy is, and which values are refused.
//   - the truncated prefixes: the exhaustion boundary. A run that needs
//     one more byte than the file holds must fail on BOTH sides at the
//     same draw, which is only true if the consumption matches exactly.
//
// The second thing the corpus has to pin is which of the two sampling
// paths runs, because they consume different draws: an input LARGER than
// 8 MiB with `-n` and without `-r` is sampled by a reservoir instead of
// being read whole and permuted. `bigEq` and `bigGt` sit either side of
// that threshold by one byte, and the pipe cases reach it from the other
// direction — a pipe has no size, so `-n` over stdin is always the
// reservoir while `-n` over a small regular file never is.
//
// What is NOT here is any case that shuffles without a named source:
// nothing can compare two runs of /dev/urandom. Those invariants —
// that the output is a permutation of the input, that -n bounds it, that
// -r may repeat — are gated by TestShufInvariants below, against the
// Fern binary alone.
func shufCases(t *testing.T) []invocation {
	dir := t.TempDir()
	zeros := shufSource(t, dir, "s.zeros", make([]byte, 256))
	ff := shufSource(t, dir, "s.ff", []byte(strings.Repeat("\xff", 256)))
	countBytes := make([]byte, 1024)
	for i := range countBytes {
		countBytes[i] = byte(i)
	}
	count := shufSource(t, dir, "s.count", countBytes)
	lcg := shufSource(t, dir, "s.lcg", shufLCG(4096))
	one := shufSource(t, dir, "s.one", []byte{0x78})
	empty := shufSource(t, dir, "s.empty", nil)
	trunc := make([]string, 0, 8)
	for _, n := range []int{0, 1, 2, 3, 4, 5, 8, 16} {
		trunc = append(trunc, shufSource(t, dir, "s.trunc"+string(rune('0'+len(trunc))), countBytes[:n]))
	}

	// Source names that are not plain: the two diagnostics quote
	// differently — an exhausted source always, a source that would not
	// open only when the name needs it.
	rawSrc := shufSource(t, dir, "na\xffme.src", nil)
	spacedSrc := shufSource(t, dir, "sp ace.src", nil)
	tabbedSrc := shufSource(t, dir, "ta\tb.src", nil)

	in5 := shufFile(t, dir, "in5", []byte("a\nb\nc\nd\ne\n"))
	in1 := shufFile(t, dir, "in1", []byte("only\n"))
	in0 := shufFile(t, dir, "in0", nil)
	nonl := shufFile(t, dir, "nonl", []byte("a\nb\nc"))
	blanks := shufFile(t, dir, "blanks", []byte("\n\n\n"))
	binary := shufFile(t, dir, "binary", []byte("a\xffb\nc\n\x80\n"))
	zsep := shufFile(t, dir, "zsep", []byte("a\x00b\x00c\x00"))
	zsepNo := shufFile(t, dir, "zsepnonl", []byte("a\x00b\x00c"))
	in100 := shufFile(t, dir, "in100", []byte(strings.Repeat("line\n", 100)))
	spaced := shufFile(t, dir, "a b", []byte("x\n"))
	raw := shufFile(t, dir, "na\xffme", []byte("x\n"))
	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")

	// Either side of the reservoir threshold, to the byte. The lines are
	// long so that the fork is exercised by nine draws rather than by a
	// million: what is being compared is which path ran, not its speed.
	bigHead := strings.Repeat(strings.Repeat("x", 1048575)+"\n", 7)
	bigEqBytes := []byte(bigHead + strings.Repeat("x", 1048575) + "\n")
	bigGtBytes := []byte(bigHead + strings.Repeat("x", 1048576) + "\n")
	if len(bigEqBytes) != 8388608 || len(bigGtBytes) != 8388609 {
		t.Fatalf("the threshold fixtures are %d and %d bytes, want 8388608 and 8388609", len(bigEqBytes), len(bigGtBytes))
	}
	bigEq := shufFile(t, dir, "big.eq", bigEqBytes)
	bigGt := shufFile(t, dir, "big.gt", bigGtBytes)

	seed := func(t *testing.T, wd string) {
		if err := os.WriteFile(filepath.Join(wd, "in"), []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, "pre"), []byte("PRE\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, "in0"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// -o follows a symlink rather than replacing it, and creates the
	// target of a dangling one.
	seedLinks := func(t *testing.T, wd string) {
		if err := os.WriteFile(filepath.Join(wd, "in"), []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, "real"), []byte("REAL\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("real", filepath.Join(wd, "good.lnk")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("target", filepath.Join(wd, "broken.lnk")); err != nil {
			t.Fatal(err)
		}
	}

	// An output path the process may not write. As root both sides
	// write it anyway, which is still the same answer on both.
	seedRO := func(t *testing.T, wd string) {
		if err := os.WriteFile(filepath.Join(wd, "in"), []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(wd, "ro"), 0o555); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, "rofile"), []byte("RO\n"), 0o444); err != nil {
			t.Fatal(err)
		}
	}

	cases := []invocation{
		// ---- the generator, against three shaped sources -------------
		{name: "range identity under zeros", args: []string{"-i", "1-10", zeros}},
		{name: "range under count", args: []string{"-i", "1-10", count}},
		{name: "range under lcg", args: []string{"-i", "1-10", lcg}},
		{name: "range of two", args: []string{"-i", "1-2", lcg}},
		{name: "range of three", args: []string{"-i", "1-3", lcg}},
		{name: "range of one draws nothing", args: []string{"-i", "7-7", empty}},
		{name: "range of 17", args: []string{"-i", "1-17", count}},
		{name: "range of 100", args: []string{"-i", "1-100", lcg}},
		{name: "range of 257", args: []string{"-i", "0-256", count}},
		{name: "range of 1000", args: []string{"-i", "1-1000", lcg}},
		{name: "ff rejects until the source ends", args: []string{"-i", "1-3", ff}},
		{name: "ff power of two never rejects", args: []string{"-i", "1-2", ff}},
		{name: "ff four", args: []string{"-i", "1-4", ff}},
		{name: "zeros one byte drives five", args: []string{"-i", "1-5", one}},
		{name: "one byte is not enough for a hundred", args: []string{"-i", "1-100", one}},

		// The wide ranges, where the state is 64 bits and the refill
		// wraps. Nothing below 2^57 can tell a wrapping shift from a
		// saturating one.
		{name: "range past 2^32", args: []string{"-i", "0-4294967296", "-n", "6", count}},
		{name: "range at 2^32", args: []string{"-i", "0-4294967295", "-n", "6", lcg}},
		{name: "range at 2^57", args: []string{"-i", "0-144115188075855872", "-n", "6", lcg}},
		{name: "range at 2^62", args: []string{"-i", "0-4611686018427387904", "-n", "6", lcg}},
		{name: "range at 10^18", args: []string{"-i", "0-1000000000000000000", "-n", "8", lcg}},
		{name: "range to intmax", args: []string{"-i", "0-9223372036854775806", "-n", "6", count}},
		{name: "range to intmax plus one", args: []string{"-i", "0-9223372036854775807", "-n", "6", count}},
		{name: "range to uintmax", args: []string{"-i", "1-18446744073709551615", "-n", "6", lcg}},
		{name: "range to uintmax less one", args: []string{"-i", "0-18446744073709551614", "-n", "6", lcg}},
		{name: "wide range repeats", args: []string{"-i", "0-1000000000000000000", "-r", "-n", "8", lcg}},
		{name: "wide range sparse and long", args: []string{"-i", "0-1000000000", "-n", "40", lcg}},
		{name: "wide range sparse past one table", args: []string{"-i", "0-1000000000", "-n", "300", lcg}},
		{name: "wide range sparse repeats", args: []string{"-i", "0-4294967296", "-r", "-n", "50", lcg}},

		// ---- lines ---------------------------------------------------
		{name: "file", args: []string{in5, count}},
		{name: "file under lcg", args: []string{in5, lcg}},
		{name: "stdin", args: []string{count}, stdin: "a\nb\nc\nd\ne\n"},
		{name: "lone dash is stdin", args: []string{"-", count}, stdin: "a\nb\nc\nd\ne\n"},
		{name: "one line", args: []string{in1, empty}},
		{name: "empty file", args: []string{in0, empty}},
		{name: "empty stdin", args: []string{empty}, stdin: ""},
		{name: "no trailing delimiter", args: []string{nonl, count}},
		{name: "blank lines", args: []string{blanks, count}},
		{name: "non utf8 content", args: []string{binary, count}},
		{name: "hundred lines", args: []string{in100, lcg}},
		{name: "stdin from a regular file", args: []string{count}, stdinPath: in5},
		{name: "quoted name", args: []string{spaced, count}},
		{name: "raw byte name", args: []string{raw, count}},

		// ---- -e ------------------------------------------------------
		{name: "echo", args: []string{"-e", "a", "b", "c", count}},
		{name: "echo long", args: []string{"--echo", "a", "b", "c", count}},
		{name: "echo one", args: []string{"-e", "solo", empty}},
		{name: "echo none", args: []string{"-e", empty}},
		{name: "echo empty operand", args: []string{"-e", "", "x", count}},
		{name: "echo non utf8 operand", args: []string{"-e", "\xff", "a\xffb", count}},
		{name: "echo after dashdash", args: []string{"-e", count, "--", "-n", "3"}},
		{name: "echo twice", args: []string{"-e", "-e", "a", "b", count}},
		{name: "echo ignores stdin", args: []string{"-e", "a", "b", count}, stdin: "IGNORED\n"},
		{name: "echo head count", args: []string{"-e", "-n", "2", "a", "b", "c", count}},

		// ---- -n ------------------------------------------------------
		{name: "head count", args: []string{"-i", "1-10", "-n", "3", count}},
		{name: "head count glued", args: []string{"-i", "1-10", "-n3", count}},
		{name: "head count long", args: []string{"-i", "1-10", "--head-count=3", count}},
		{name: "head count long split", args: []string{"-i", "1-10", "--head-count", "3", count}},
		{name: "head count prefix", args: []string{"-i", "1-10", "--hea", "3", count}},
		{name: "head count zero", args: []string{"-i", "1-10", "-n", "0"}},
		{name: "head count past the range", args: []string{"-i", "1-3", "-n", "10", count}},
		{name: "head count equals the range", args: []string{"-i", "1-3", "-n", "3", count}},
		{name: "head count takes the minimum", args: []string{"-n", "3", "-n", "2", in5, count}},
		{name: "head count minimum other order", args: []string{"-n", "2", "-n", "3", in5, count}},
		{name: "head count overflow is dropped", args: []string{"-n", "99999999999999999999999", "-i", "1-3", count}},
		{name: "head count at the cap", args: []string{"-n", "18446744073709551615", "-i", "1-3", count}},
		{name: "head count one below the cap", args: []string{"-n", "18446744073709551614", "-i", "1-3", count}},
		{name: "head count leading blank", args: []string{"-n", " 3", "-i", "1-9", count}},
		{name: "head count leading plus", args: []string{"-n", "+3", "-i", "1-9", count}},
		{name: "head count of a file", args: []string{"-n", "2", in5, count}},
		{name: "head count of stdin is a reservoir", args: []string{"-n", "2", count}, stdin: "a\nb\nc\nd\ne\n"},
		{name: "head count of stdin one", args: []string{"-n", "1", count}, stdin: "a\nb\nc\nd\ne\n"},
		{name: "head count of stdin exact", args: []string{"-n", "5", count}, stdin: "a\nb\nc\nd\ne\n"},
		{name: "head count of stdin past the end", args: []string{"-n", "6", count}, stdin: "a\nb\nc\nd\ne\n"},
		{name: "head count of empty stdin", args: []string{"-n", "3", count}, stdin: ""},
		{name: "head count of one line of stdin", args: []string{"-n", "3", count}, stdin: "a\n"},
		{name: "reservoir over a hundred lines", args: []string{"-n", "4", lcg}, stdin: strings.Repeat("line\n", 100)},
		{name: "reservoir keeps the last lines", args: []string{"-n", "2", zeros}, stdin: strings.Repeat("line\n", 50)},

		// ---- the reservoir threshold ---------------------------------
		{name: "eight mib exactly is read whole", args: []string{"-n", "3", lcg, bigEq}},
		{name: "one byte more is a reservoir", args: []string{"-n", "3", lcg, bigGt}},
		{name: "eight mib exactly on stdin", args: []string{"-n", "3", lcg}, stdinPath: bigEq},
		{name: "one byte more on stdin", args: []string{"-n", "3", lcg}, stdinPath: bigGt},
		{name: "eight mib without a head count", args: []string{lcg, bigEq}},
		{name: "one byte more without a head count", args: []string{lcg, bigGt}},
		{name: "repeat turns the reservoir off", args: []string{"-r", "-n", "3", lcg, bigGt}},

		// ---- -r ------------------------------------------------------
		{name: "repeat", args: []string{"-r", "-n", "6", in5, count}},
		{name: "repeat long", args: []string{"--repeat", "-n", "6", in5, count}},
		{name: "repeat a range", args: []string{"-r", "-n", "6", "-i", "10-14", count}},
		{name: "repeat one line", args: []string{"-r", "-n", "4", in1, empty}},
		{name: "repeat echo", args: []string{"-r", "-e", "-n", "4", "a", "b", count}},
		{name: "repeat zero", args: []string{"-r", "-n", "0", in5}},
		{name: "repeat of nothing", args: []string{"-r", in0, count}},
		{name: "repeat of empty stdin", args: []string{"-r", count}, stdin: ""},
		{name: "repeat of no operands", args: []string{"-r", "-e", count}},
		{name: "repeat of an empty range", args: []string{"-r", "-n", "2", "-i", "1-0", count}},
		{name: "repeat writes before the source runs out", args: []string{"-r", "-n", "20", "-i", "1-3", one}},
		{name: "repeat of lines until the source runs out", args: []string{"-r", "-n", "200", in5, one}},
		// The source has to be one that cannot run out: the case ends
		// when the harness closes the pipe, and a source exhausted first
		// would make which of the two happens a race.
		{name: "repeat forever", args: []string{"-r", in5, "--random-source=/dev/zero"}, limit: 64},
		{name: "repeat a range forever", args: []string{"-r", "-i", "1-9", "--random-source=/dev/zero"}, limit: 40},

		// ---- -z ------------------------------------------------------
		{name: "zero terminated", args: []string{"-z", zsep, count}},
		{name: "zero terminated long", args: []string{"--zero-terminated", zsep, count}},
		{name: "zero terminated no final nul", args: []string{"-z", zsepNo, count}},
		{name: "zero terminated newline file is one line", args: []string{"-z", in5, count}},
		{name: "zero terminated range", args: []string{"-z", "-i", "1-5", count}},
		{name: "zero terminated echo", args: []string{"-z", "-e", "a", "b", count}},
		{name: "zero terminated stdin", args: []string{"-z", count}, stdin: "a\x00b\x00c\x00"},
		{name: "zero terminated head count", args: []string{"-z", "-n", "2", count}, stdin: "a\x00b\x00c\x00"},
		{name: "zero terminated repeat", args: []string{"-z", "-r", "-n", "3", zsep, count}},
		{name: "zero terminated twice", args: []string{"-z", "-z", "-i", "1-2", count}},

		// ---- -o ------------------------------------------------------
		{name: "output file", args: []string{"-o", "out", count, "in"}, seedTree: seed},
		{name: "output long", args: []string{"--output=out", count, "in"}, seedTree: seed},
		{name: "output long split", args: []string{"--output", "out", count, "in"}, seedTree: seed},
		{name: "output is the input", args: []string{"-o", "in", count, "in"}, seedTree: seed},
		{name: "output truncates", args: []string{"-o", "pre", count, "in"}, seedTree: seed},
		{name: "output of a range", args: []string{"-o", "out", "-i", "1-5", count}, seedTree: seed},
		{name: "output of echo", args: []string{"-e", "-o", "out", "x", "y", count}, seedTree: seed},
		{name: "output dash is a file", args: []string{"-o", "-", count, "in"}, seedTree: seed},
		{name: "output twice with one name", args: []string{"-o", "out", "-o", "out", count, "in"}, seedTree: seed},
		{name: "output twice with two names", args: []string{"-o", "out", "-o", "other", count, "in"}, seedTree: seed},
		{name: "head count zero truncates the output", args: []string{"-n", "0", "-o", "pre", "in"}, seedTree: seed},
		{name: "repeat of nothing truncates the output", args: []string{"-r", "-o", "pre", "in0"}, seedTree: seed},
		{name: "an exhausted source leaves the output alone", args: []string{"-o", "pre", "-i", "1-100", one}, seedTree: seed},
		{name: "a bad range leaves the output alone", args: []string{"-o", "pre", "-i", "9-3"}, seedTree: seed},
		{name: "a missing input leaves the output alone", args: []string{"-o", "pre", "nosuch"}, seedTree: seed},
		{name: "output into a missing directory", args: []string{"-o", "nodir/out", count, "in"}, seedTree: seed},
		{name: "output is a directory", args: []string{"-o", ".", count, "in"}, seedTree: seed},
		{name: "output with no value", args: []string{"-o"}},
		{name: "output long with no value", args: []string{"--output"}},
		{name: "output is empty", args: []string{"-o", "", count}, stdin: "a\n"},
		{name: "output through a symlink", args: []string{"-o", "good.lnk", count, "in"}, seedTree: seedLinks},
		{name: "output through a dangling symlink", args: []string{"-o", "broken.lnk", count, "in"}, seedTree: seedLinks},
		{name: "a dangling symlink as input", args: []string{"broken.lnk", count}, seedTree: seedLinks},
		{name: "output into an unwritable directory", args: []string{"-o", "ro/out", count, "in"}, seedTree: seedRO},
		{name: "output over an unwritable file", args: []string{"-o", "rofile", count, "in"}, seedTree: seedRO},

		// ---- --random-source -----------------------------------------
		{name: "source with no value", args: []string{"--random-source"}},
		{name: "source is empty", args: []string{"--random-source=", "-i", "1-5"}},
		{name: "source is missing", args: []string{"--random-source=" + missing, "-i", "1-5"}},
		{name: "source is a directory", args: []string{"--random-source=" + d, "-i", "1-5"}},
		{name: "a directory source with nothing to draw", args: []string{"--random-source=" + d, "-i", "1-1"}},
		{name: "an empty source with nothing to draw", args: []string{empty, "-i", "1-1"}},
		{name: "an empty source with a draw", args: []string{empty, "-i", "1-2"}},
		{name: "source twice with one name", args: []string{count, count, in5}},
		{name: "source twice with two names", args: []string{count, lcg, in5}},
		{name: "source is not opened with nothing to draw", args: []string{"--random-source=" + missing}, stdin: ""},
		{name: "source is opened for a reservoir of nothing", args: []string{"-n", "1", "--random-source=" + missing}, stdin: ""},
		{name: "source is opened for a repeat of nothing", args: []string{"-r", "--random-source=" + missing}, stdin: ""},
		{name: "the cap keeps the reservoir off", args: []string{"-n", "18446744073709551615", "--random-source=" + missing}, stdin: ""},
		{name: "one below the cap turns it on", args: []string{"-n", "18446744073709551614", "--random-source=" + missing}, stdin: ""},
		{name: "a missing input beats a missing source", args: []string{"--random-source=" + missing, missing}},
		{name: "a missing source beats a bad output", args: []string{"--random-source=" + missing, "-o", "/nonexistent-dir/x"}, stdin: "a\nb\n"},
		{name: "source prefix", args: []string{"--ra=" + strings.TrimPrefix(count, "--random-source="), "-i", "1-5"}},
		{name: "source long split", args: []string{"--random-source", strings.TrimPrefix(count, "--random-source="), "-i", "1-5"}},
		{name: "exhausted source with a raw byte in its name", args: []string{rawSrc, "-i", "1-5"}},
		{name: "exhausted source with a space in its name", args: []string{spacedSrc, "-i", "1-5"}},
		{name: "exhausted source with a tab in its name", args: []string{tabbedSrc, "-i", "1-5"}},
		{name: "missing source with a raw byte in its name", args: []string{rawSrc + ".missing", "-i", "1-5"}},
		{name: "missing source with a space in its name", args: []string{spacedSrc + ".missing", "-i", "1-5"}},

		// ---- the exhaustion boundary ---------------------------------
		{name: "no bytes at all", args: []string{"-i", "1-5", trunc[0]}},
		{name: "one byte", args: []string{"-i", "1-5", trunc[1]}},
		{name: "two bytes", args: []string{"-i", "1-100", trunc[2]}},
		{name: "three bytes", args: []string{"-i", "1-100", trunc[3]}},
		{name: "four bytes", args: []string{"-i", "1-1000", trunc[4]}},
		{name: "five bytes", args: []string{"-i", "1-1000", trunc[5]}},
		{name: "eight bytes of a wide range", args: []string{"-i", "0-1000000000000000000", "-n", "2", trunc[6]}},
		{name: "sixteen bytes of a wide range", args: []string{"-i", "0-1000000000000000000", "-n", "4", trunc[7]}},
		{name: "a reservoir that outruns the source", args: []string{"-n", "3", trunc[3]}, stdin: strings.Repeat("line\n", 100)},
		{name: "a repeat that outruns the source", args: []string{"-r", "-n", "40", "-i", "1-7", trunc[2]}},

		// ---- -i's grammar --------------------------------------------
		{name: "range glued", args: []string{"-i1-5", count}},
		{name: "range long", args: []string{"--input-range=1-5", count}},
		{name: "range long split", args: []string{"--input-range", "1-5", count}},
		{name: "range prefix", args: []string{"--in", "1-5", count}},
		{name: "range from zero", args: []string{"-i", "0-3", count}},
		{name: "range of one number", args: []string{"-i", "5-5", count}},
		{name: "empty range", args: []string{"-i", "1-0", count}},
		{name: "empty range at the top", args: []string{"-i", "6-5", count}},
		{name: "inverted range", args: []string{"-i", "5-2"}},
		{name: "inverted range wider", args: []string{"-i", "6-4"}},
		{name: "inverted range from uintmax", args: []string{"-i", "18446744073709551615-0"}},
		{name: "range covering everything", args: []string{"-i", "0-18446744073709551615", "-n", "2"}},
		{name: "range at uintmax", args: []string{"-i", "18446744073709551615-18446744073709551615", count}},
		{name: "range leading blank", args: []string{"-i", " 1-5", count}},
		{name: "range leading plus", args: []string{"-i", "+1-5", count}},
		{name: "range plus on the top", args: []string{"-i", "1-+5", count}},
		{name: "range blank before the top", args: []string{"-i", "1- 5", count}},
		{name: "range leading zeros", args: []string{"-i", "01-05", count}},
		{name: "range trailing blank", args: []string{"-i", "1-5 "}},
		{name: "range with a suffix", args: []string{"-i", "1-5k"}},
		{name: "range in hex", args: []string{"-i", "0x1-0xa"}},
		{name: "range with a suffix on the bottom", args: []string{"-i", "1k-5"}},
		{name: "range with three parts", args: []string{"-i", "1-2-3"}},
		{name: "range with a trailing dash", args: []string{"-i", "1-5-"}},
		{name: "range negative", args: []string{"-i", "-1-5"}},
		{name: "range negative top", args: []string{"-i", "1--5"}},
		{name: "range negative zero", args: []string{"-i", "-0-0"}},
		{name: "range of letters", args: []string{"-i", "a-b"}},
		{name: "range with no dash", args: []string{"-i", "1"}},
		{name: "range with no top", args: []string{"-i", "1-"}},
		{name: "range is one dash", args: []string{"-i", "-"}},
		{name: "range is empty", args: []string{"-i", ""}},
		{name: "range overflows on the bottom", args: []string{"-i", "99999999999999999999999999-5"}},
		{name: "range overflows on the top", args: []string{"-i", "1-99999999999999999999999999"}},
		{name: "range overflows with a suffix on the bottom", args: []string{"-i", "99999999999999999999x-5"}},
		{name: "range overflows with a suffix on the top", args: []string{"-i", "1-99999999999999999999x"}},
		{name: "range with no value", args: []string{"-i"}},
		{name: "range long with no value", args: []string{"--input-range"}},
		{name: "range twice", args: []string{"-i", "1-3", "-i", "1-3"}},
		{name: "range twice with a bad second", args: []string{"-i", "1-3", "-i", "5-1"}},
		{name: "range twice with a bad first", args: []string{"-i", "5-1", "-i", "1-3"}},

		// ---- -n's grammar --------------------------------------------
		{name: "count is negative", args: []string{"-n", "-1", in5}},
		{name: "count is a word", args: []string{"-n", "x", in5}},
		{name: "count is empty", args: []string{"-n", "", in5}},
		{name: "count has a multiplier", args: []string{"-n", "1k", in5}},
		{name: "count is hex", args: []string{"-n", "0x10", in5}},
		{name: "count has a suffix", args: []string{"-n", "5x", in5}},
		{name: "count has a trailing blank", args: []string{"-n", "3\t", in5}},
		{name: "count overflows with a suffix", args: []string{"-n", "99999999999999999999k", in5}},
		{name: "a bad count after a good one", args: []string{"-n", "2", "-n", "abc", in5}},
		{name: "a bad count before a good one", args: []string{"-n", "abc", "-n", "2", in5}},
		{name: "count with no value", args: []string{"-n"}},
		{name: "count long with no value", args: []string{"--head-count"}},

		// ---- the short circuit ---------------------------------------
		{name: "zero count opens nothing", args: []string{"-n", "0", missing}},
		{name: "zero count opens no source", args: []string{"-n", "0", "--random-source=" + missing, missing}},
		{name: "zero count still checks the range", args: []string{"-n", "0", "-i", "5-1"}},
		{name: "zero count and a directory", args: []string{"-n", "0", d}},
		{name: "zero count and stdin", args: []string{"-n", "0"}, stdin: "a\nb\n"},
		{name: "zero count and an extra operand", args: []string{"-n", "0", in5, in5}},

		// ---- operands and the option scan ----------------------------
		{name: "two operands", args: []string{in5, in5, count}},
		{name: "three operands", args: []string{"a", "b", "c"}},
		{name: "an operand with a range", args: []string{"-i", "1-3", in5}},
		{name: "two operands with a range", args: []string{"-i", "1-3", "foo", "bar"}},
		{name: "a dash operand with a range", args: []string{"-i", "1-3", "-"}},
		{name: "two dashes", args: []string{"-", "-"}},
		{name: "echo and range together", args: []string{"-e", "-i", "1-3"}},
		{name: "range and echo together", args: []string{"-i", "1-3", "-e", "a"}},
		{name: "combine beats the extra operand", args: []string{"-e", "-i", "1-3", "extra"}},
		{name: "a bad range beats the combine", args: []string{"-e", "-i", "5-1"}},
		{name: "missing file", args: []string{missing}},
		{name: "directory operand", args: []string{d}},
		{name: "directory operand with a count", args: []string{"-n", "2", d, count}},
		{name: "invalid option", args: []string{"-x"}},
		{name: "unrecognized long option", args: []string{"--foo=bar"}},
		{name: "n is not a long option", args: []string{"--n", "2"}},
		{name: "h is ambiguous", args: []string{"--h"}},
		{name: "r is ambiguous", args: []string{"--r"}},
		{name: "echo refuses a value", args: []string{"--echo=x"}},
		{name: "repeat refuses a value", args: []string{"--repeat=x"}},
		{name: "zero terminated prefix refuses a value", args: []string{"--ze=x"}},
		{name: "help refuses a value", args: []string{"--help=x"}},
		{name: "cluster", args: []string{"-ez", "a", "b", count}},
		{name: "cluster with a glued count", args: []string{"-zen2", "a", "b", "c", count}},
		{name: "repeated flags", args: []string{"-zz", "-rr", "-n", "3", in5, count}},
		{name: "dashdash alone", args: []string{count, "--"}, stdin: "a\nb\n"},
		{name: "dashdash after a range", args: []string{"-i", "1-3", count, "--"}},
		{name: "dashdash makes an option an operand", args: []string{"--", count}},
		{name: "dashdash before the file", args: []string{count, "--", in5}},
		{name: "options after the operand are permuted", args: []string{in5, "-n", "2", count}},
		{name: "posixly correct stops at the operand", args: []string{in5, "-n", "2", count}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posixly correct with an empty value", args: []string{in5, "-n", "2", count}, env: []string{"POSIXLY_CORRECT="}},
		{name: "posixly correct with the options first", args: []string{"-n", "2", count, in5}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "version after a bad option", args: []string{"-x", "--version"}},

		// ---- write failures ------------------------------------------
		{name: "closed stdout", args: []string{"-i", "1-3", count}, stdout: stdoutClosed},
		{name: "closed stdout with nothing to write", args: []string{"-n", "0"}, stdout: stdoutClosed},
		{name: "full stdout", args: []string{"-i", "1-3", count}, stdout: stdoutFull},
		{name: "full stdout under repeat", args: []string{"-r", "-n", "3", "-i", "1-3", count}, stdout: stdoutFull},
		{name: "full stdout of lines", args: []string{in5, count}, stdout: stdoutFull},
	}
	return cases
}

func TestShufParity(t *testing.T) {
	requireParity(t, "shuf", shufCases(t))
}

func TestShufHelpVersion(t *testing.T) {
	requireHelp(t, "shuf", []string{"--help"}, 0)
	requireHelp(t, "shuf", []string{"--hel"}, 0)
	requireHelp(t, "shuf", []string{"--help", "ignored"}, 0)
	requireHelp(t, "shuf", []string{"-e", "-i", "1-3", "--help"}, 0)
	requireVersion(t, "shuf", []string{"--vers"}, 0)
	requireHelp(t, "shuf", []string{"extra", "--help"}, 0)
	requireVersion(t, "shuf", []string{"--version"}, 0)
	requireVersion(t, "shuf", []string{"-i", "1-3", "extra", "--version"}, 0)
}

// TestShufInvariants covers what the oracle cannot: a run with no
// `--random-source` draws from /dev/urandom, so two runs of the same
// argv disagree with each other and GNU's bytes are not a reference for
// ours. What IS fixed about such a run — that the output is a
// permutation of the input, that -n bounds it, that -r may repeat and
// stays inside the input, that -i covers its range — is checked here
// against the Fern binary alone. Their stderr and exit status ARE
// deterministic, so those stay in the oracle corpus above.
func TestShufInvariants(t *testing.T) {
	bin := fernBin(t, "shuf")
	run := func(inv invocation) outcome {
		t.Helper()
		return inv.run(t, bin, "shuf")
	}
	lines := func(b []byte) []string {
		s := string(b)
		if s == "" {
			return nil
		}
		s = strings.TrimSuffix(s, "\n")
		return strings.Split(s, "\n")
	}
	sorted := func(v []string) string {
		c := append([]string(nil), v...)
		for i := 1; i < len(c); i++ {
			for j := i; j > 0 && c[j] < c[j-1]; j-- {
				c[j], c[j-1] = c[j-1], c[j]
			}
		}
		return strings.Join(c, "\x01")
	}

	input := "alpha\nbravo\ncharlie\ndelta\necho\nfoxtrot\ngolf\nhotel\n"
	want := lines([]byte(input))

	// A plain shuffle is a permutation of the input: same multiset, and
	// with eight distinct lines, same set.
	for i := 0; i < 8; i++ {
		got := run(invocation{stdin: input})
		if got.exit != 0 || len(got.stderr) != 0 {
			t.Fatalf("shuffle: exit %d stderr %q", got.exit, got.stderr)
		}
		if sorted(lines(got.stdout)) != sorted(want) {
			t.Fatalf("shuffle is not a permutation of the input: %q", got.stdout)
		}
	}

	// -n bounds the count and still draws from the input, on both the
	// read-whole and the reservoir path (a pipe is always the latter).
	for _, n := range []int{1, 3, 8, 9} {
		got := run(invocation{args: []string{"-n", strconv.Itoa(n)}, stdin: input})
		if got.exit != 0 {
			t.Fatalf("-n %d: exit %d stderr %q", n, got.exit, got.stderr)
		}
		out := lines(got.stdout)
		if want := min(n, len(want)); len(out) != want {
			t.Fatalf("-n %d produced %d lines, want %d", n, len(out), want)
		}
		seen := map[string]bool{}
		for _, l := range out {
			if seen[l] {
				t.Fatalf("-n %d repeated %q without -r", n, l)
			}
			seen[l] = true
			if !strings.Contains(input, l+"\n") {
				t.Fatalf("-n %d invented the line %q", n, l)
			}
		}
	}

	// -r may repeat, and every line it writes is one of the input's.
	got := run(invocation{args: []string{"-r", "-n", "200"}, stdin: input})
	if got.exit != 0 {
		t.Fatalf("-r: exit %d stderr %q", got.exit, got.stderr)
	}
	out := lines(got.stdout)
	if len(out) != 200 {
		t.Fatalf("-r -n 200 wrote %d lines", len(out))
	}
	for _, l := range out {
		if !strings.Contains(input, l+"\n") {
			t.Fatalf("-r invented the line %q", l)
		}
	}
	if sorted(out[:8]) == sorted(want) && sorted(out[8:16]) == sorted(want) {
		t.Fatalf("-r looks like a permutation rather than a draw with replacement")
	}

	// -i covers exactly its range.
	got = run(invocation{args: []string{"-i", "1-50"}})
	if got.exit != 0 {
		t.Fatalf("-i: exit %d stderr %q", got.exit, got.stderr)
	}
	nums := lines(got.stdout)
	if len(nums) != 50 {
		t.Fatalf("-i 1-50 wrote %d lines", len(nums))
	}
	seen := map[string]bool{}
	for _, s := range nums {
		if seen[s] {
			t.Fatalf("-i 1-50 repeated %q", s)
		}
		seen[s] = true
	}
	for i := 1; i <= 50; i++ {
		if !seen[strconv.Itoa(i)] {
			t.Fatalf("-i 1-50 never wrote %d", i)
		}
	}

	// An unshuffled shape is still a shape: one line in, one line out.
	got = run(invocation{stdin: "only\n"})
	if got.exit != 0 || string(got.stdout) != "only\n" {
		t.Fatalf("one line: exit %d stdout %q", got.exit, got.stdout)
	}
}
