package coreutils

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func init() {
	registerCorpus("dd", ddCases)
}

// ddSeed is the tree every case works over: a ten-byte input whose bytes
// are all distinct (so a skip or a seek that lands one byte off is
// visible), a five-thousand-byte one for the block arithmetic, a file
// already holding ten bytes for the cases that write INTO something, and
// a directory for the open failures.
func ddSeed(t *testing.T, dir string) {
	t.Helper()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("in10", "abcdefghij")
	write("in5000", strings.Repeat("Z", 5000))
	write("pre", "0123456789")
	write("lines", "a\nbcdef\nghi\n")
	write("recs", "ab  cd  ef  ")
	// Bytes that are not ASCII letters and one that is not UTF-8 at all,
	// for the case conversions: in the C locale neither is a letter, so
	// both have to reach the output untouched.
	write("mixed", "caf\xc3\xa9 A\xffz Z\n")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// ddCases is dd(1)'s corpus.
//
// Every case names `status=noxfer` or `status=none`, and that is not a
// preference: dd's third report line carries the elapsed TIME and a rate
// derived from it, which two correct implementations cannot agree on. A
// mask cannot help — the harness applies one to stdout and to the tree,
// never to stderr, and the whole report is on stderr — so the third line
// is pinned by shape instead, in TestDDTransferLine below.
//
// What is left is fully comparable, and it is most of dd: the record
// counts, every byte of the output file, the operand grammar with its
// five separate diagnostics, and what each `conv=` does to the bytes.
func ddCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, seedTree: ddSeed})
	}

	// --- the report's two comparable lines ---------------------------------
	//
	// `N+M records in` counts reads: full when one returned `ibs` bytes
	// and partial when it returned fewer, which is why ten bytes through
	// the default 512-byte block is `0+1`. Records OUT are the writes,
	// and `ibs=3 obs=4` is what separates the two counts.
	add("plain", "if=in10", "of=out", "status=noxfer")
	add("bs-4", "if=in10", "of=out", "bs=4", "status=noxfer")
	add("bs-4096-short-last", "if=in5000", "of=out", "bs=4096", "status=noxfer")
	add("ibs-obs-differ", "if=in10", "of=out", "ibs=3", "obs=4", "status=noxfer")
	add("ibs-obs-differ-quiet", "if=in10", "of=out", "ibs=3", "obs=4", "status=none")
	add("count-blocks", "if=in5000", "of=out", "bs=1024", "count=2", "status=noxfer")
	add("count-zero", "if=in10", "of=out", "count=0", "status=noxfer")
	add("count-bytes", "if=in5000", "of=out", "bs=1024", "count=100", "iflag=count_bytes", "status=noxfer")
	add("count-multiplied", "if=in10", "of=out", "bs=1", "count=2x3", "status=noxfer")
	add("bs-multiplied", "if=in10", "of=out", "bs=1kx2", "count=1", "status=noxfer")

	// --- skip and seek -----------------------------------------------------
	add("skip-and-seek", "if=in10", "of=out", "bs=1", "skip=3", "seek=2", "status=noxfer")
	add("skip-bytes", "if=in10", "of=out", "bs=4", "skip=3", "iflag=skip_bytes", "status=noxfer")
	add("seek-bytes", "if=in10", "of=out", "bs=4", "seek=3", "oflag=seek_bytes", "status=noxfer")
	add("skip-to-eof", "if=in10", "of=out", "bs=1", "skip=10", "status=noxfer")
	add("skip-past-eof", "if=in10", "of=out", "bs=1", "skip=11", "status=noxfer")
	add("skip-far-past-eof", "if=in10", "of=out", "bs=1", "skip=50", "status=noxfer")
	// A seek without conv=notrunc cuts the output at the seek offset, so
	// the bytes before it survive and everything after is gone.
	add("seek-truncates", "if=in10", "of=pre", "bs=1", "seek=2", "count=3", "status=noxfer")
	add("seek-notrunc", "if=in10", "of=pre", "bs=1", "seek=2", "count=3", "conv=notrunc", "status=noxfer")
	add("notrunc-shorter", "if=in10", "of=pre", "bs=1", "count=3", "conv=notrunc", "status=noxfer")
	add("append", "if=in10", "of=pre", "oflag=append", "conv=notrunc", "status=noxfer")

	// --- iseek and oseek ---------------------------------------------------
	//
	// Aliases, so the pair is one operand for the last-wins rule: naming
	// both takes the second of them.
	add("iseek", "if=in10", "of=out", "bs=1", "iseek=3", "status=noxfer")
	add("oseek", "if=in10", "of=out", "bs=2", "oseek=1", "status=noxfer")
	add("skip-then-iseek", "if=in10", "of=out", "bs=1", "skip=1", "iseek=3", "status=noxfer")
	add("iseek-then-skip", "if=in10", "of=out", "bs=1", "iseek=3", "skip=1", "status=noxfer")
	add("oseek-notrunc", "if=in10", "of=pre", "bs=1", "oseek=2", "count=3", "conv=notrunc", "status=noxfer")
	add("iseek-invalid-number", "if=in10", "iseek=q")

	// --- the number grammar ------------------------------------------------
	//
	// `c` is 1 and `w` is 2, neither takes the `iB` form or a second
	// multiplier, and a `B` on the END of any piece makes the operand a
	// BYTE count — which rides the multiplier grammar's own endings, so
	// `1kB` is 1000 bytes where `1k` is 1024 blocks.
	add("suffix-c", "if=in10", "of=out", "bs=1", "count=1cx7", "status=noxfer")
	add("suffix-c-bare", "if=in10", "of=out", "bs=1", "count=c", "status=noxfer")
	add("suffix-w", "if=in10", "of=out", "bs=1", "count=3w", "status=noxfer")
	add("suffix-w-bare", "if=in10", "of=out", "bs=1", "count=w", "status=noxfer")
	add("suffix-b", "if=in10", "of=out", "bs=1", "count=b", "status=noxfer")
	add("suffix-c-on-bs", "if=in10", "of=out", "bs=3c", "status=noxfer")
	add("suffix-w-on-bs", "if=in10", "of=out", "bs=2w", "status=noxfer")
	add("count-bytes-suffix", "if=in5000", "of=out", "bs=100", "count=2B", "status=noxfer")
	add("count-bytes-suffix-kb", "if=in5000", "of=out", "bs=100", "count=1kB", "status=noxfer")
	add("count-bytes-suffix-kib", "if=in5000", "of=out", "bs=100", "count=1KiB", "status=noxfer")
	add("count-blocks-no-suffix", "if=in5000", "of=out", "bs=100", "count=1k", "status=noxfer")
	add("count-bytes-suffix-product", "if=in5000", "of=out", "bs=100", "count=1cx7Bx2", "status=noxfer")
	add("count-bytes-suffix-after-b", "if=in5000", "of=out", "bs=100", "count=1bB", "status=noxfer")
	add("skip-bytes-suffix", "if=in10", "of=out", "bs=4", "skip=3B", "status=noxfer")
	add("seek-bytes-suffix", "if=in10", "of=out", "bs=4", "seek=3B", "status=noxfer")
	add("bytes-suffix-on-bs", "if=in10", "of=out", "bs=3B", "status=noxfer")
	add("invalid-number-uppercase-c", "if=in10", "count=1C")
	add("invalid-number-uppercase-w", "if=in10", "count=1W")
	add("invalid-number-c-then-ib", "if=in10", "count=1ciB")
	add("invalid-number-c-then-digit", "if=in10", "count=1c0")
	add("invalid-number-k-then-c", "if=in10", "count=1kc")
	add("invalid-number-lowercase-b-after-k", "if=in10", "count=1kb")
	add("invalid-number-double-b", "if=in10", "count=1BB")
	add("invalid-number-bare-b", "if=in10", "count=B")
	// A value past INTMAX_MAX is a DIFFERENT line from a value that is
	// not a number: the errno text comes after the quoted operand.
	add("number-overflow", "if=in10", "count=9223372036854775808")
	add("number-overflow-unsigned", "if=in10", "count=18446744073709551616")
	add("number-overflow-product", "if=in10", "count=9223372036854775807x9223372036854775807")
	add("number-at-intmax", "if=in10", "of=out", "count=9223372036854775807", "status=noxfer")
	// `0x` is a warning rather than a refusal, since a reader may have
	// meant hexadecimal — one per piece, and the zero still wins.
	add("zero-multiplier", "if=in10", "of=out", "count=0x3", "status=noxfer")
	add("zero-multiplier-twice", "if=in10", "of=out", "count=0x0x3", "status=noxfer")
	add("zero-multiplier-spelled-out", "if=in10", "of=out", "count=00x3", "status=noxfer")
	add("zero-multiplier-on-bs", "if=in10", "bs=0x3")
	add("zero-multiplier-trailing", "if=in10", "of=out", "count=3x0", "status=noxfer")

	// --- the byte conversions ----------------------------------------------
	add("conv-ucase", "if=in10", "of=out", "conv=ucase", "status=noxfer")
	add("conv-lcase", "if=in10", "of=out", "conv=lcase", "bs=4", "status=noxfer")
	add("conv-swab", "if=in10", "of=out", "conv=swab", "status=noxfer")
	add("conv-swab-odd", "if=in10", "of=out", "bs=3", "conv=swab", "status=noxfer")
	add("conv-sync-pads", "if=in10", "of=out", "ibs=4", "conv=sync", "status=noxfer")
	add("conv-two", "if=in10", "of=out", "bs=4", "conv=ucase,swab", "status=noxfer")
	// The case conversions are C-locale tolower/toupper: the 26 ASCII
	// letters and nothing else, so a byte over 0x7F and a byte that is
	// not UTF-8 both pass through as they stand.
	add("conv-ucase-non-ascii", "if=mixed", "of=out", "conv=ucase", "status=noxfer")
	add("conv-lcase-non-ascii", "if=mixed", "of=out", "conv=lcase", "status=noxfer")
	add("conv-ucase-small-blocks", "if=mixed", "of=out", "bs=3", "conv=ucase", "status=noxfer")
	add("conv-lcase-and-swab", "if=mixed", "of=out", "bs=3", "conv=lcase,swab", "status=noxfer")

	// --- the record conversions --------------------------------------------
	//
	// `conv=block` turns lines into cbs-sized records and counts the ones
	// it had to cut; `conv=unblock` turns them back. Neither is on without
	// a `cbs`, which is why the bare flag copies the bytes through.
	add("conv-block", "if=lines", "of=out", "conv=block", "cbs=4", "status=noxfer")
	add("conv-block-truncates", "if=lines", "of=out", "conv=block", "cbs=2", "status=noxfer")
	add("conv-block-no-cbs", "if=lines", "of=out", "conv=block", "status=noxfer")
	add("conv-unblock", "if=recs", "of=out", "conv=unblock", "cbs=4", "status=noxfer")
	add("conv-unblock-partial", "if=recs", "of=out", "conv=unblock", "cbs=5", "status=noxfer")
	add("conv-block-and-unblock", "if=lines", "of=out", "conv=block,unblock", "cbs=4", "status=noxfer")

	// --- the opens ---------------------------------------------------------
	add("no-such-input", "if=nosuch", "of=out", "status=noxfer")
	add("input-is-a-directory", "if=d", "of=out", "status=noxfer")
	add("output-is-a-directory", "if=in10", "of=d", "status=noxfer")
	// `conv=excl` on a name that is TAKEN is here; the case that
	// CREATES one is not, and that is a gap rather than a preference:
	// Fern's exclusive open fixes the mode at 0600 where GNU's is 0666
	// through the umask, so the file dd leaves behind differs by its
	// mode alone. #9237 is the flags-word bit that closes it.
	add("conv-excl-exists", "if=in10", "of=pre", "conv=excl", "status=noxfer")
	add("conv-nocreat-missing", "if=in10", "of=nosuch2", "conv=nocreat", "status=noxfer")
	add("conv-nocreat-exists", "if=in10", "of=pre", "conv=nocreat", "status=noxfer")
	add("conv-fsync", "if=in10", "of=out", "conv=fsync", "status=noxfer")
	add("conv-fdatasync", "if=in10", "of=out", "conv=fdatasync", "status=noxfer")
	// A descriptor with no data-only sync answers EINVAL, which is not a
	// failure to report: the fall-back is a full fsync, so what a run
	// over /dev/null reports is `fsync failed`.
	add("conv-fdatasync-no-data-sync", "if=in10", "of=/dev/null", "conv=fdatasync", "status=noxfer")
	add("conv-fsync-no-sync", "if=in10", "of=/dev/null", "conv=fsync", "status=noxfer")
	add("conv-both-syncs-no-sync", "if=in10", "of=/dev/null", "conv=fdatasync,fsync", "status=noxfer")

	// --- the grammar's five diagnostics ------------------------------------
	add("unrecognized-operand", "bogus=1")
	add("operand-without-equals", "in10")
	add("bare-dash", "-")
	add("unrecognized-option", "--bogus")
	add("invalid-number", "if=in10", "bs=nope")
	add("invalid-number-empty", "if=in10", "bs=")
	add("invalid-number-negative", "if=in10", "count=-1")
	add("invalid-number-zero-bs", "if=in10", "bs=0")
	add("invalid-number-trailing-x", "if=in10", "bs=1x")
	add("invalid-conversion", "if=in10", "conv=bogus")
	add("invalid-conversion-empty", "if=in10", "conv=")
	add("invalid-input-flag", "if=in10", "iflag=bogus")
	add("invalid-output-flag", "if=in10", "oflag=bogus")
	add("invalid-status", "if=in10", "status=bogus")
	// The last operand of a repeated name wins.
	add("last-name-wins", "if=pre", "if=in10", "of=out", "status=noxfer")
	// `if=-` is a file called `-`, not standard input.
	add("dash-is-a-name", "if=-", "of=out", "status=noxfer")

	// --- standard input and output -----------------------------------------
	cases = append(cases,
		invocation{name: "stdin-to-stdout", args: []string{"status=noxfer"}, stdin: "hello dd", seedTree: ddSeed},
		invocation{name: "stdin-skip", args: []string{"bs=1", "skip=3", "status=noxfer"}, stdin: "abcdefghij", seedTree: ddSeed},
		invocation{name: "stdin-skip-past-eof", args: []string{"bs=1", "skip=11", "status=noxfer"}, stdin: "abcdefghij", seedTree: ddSeed},
		invocation{name: "stdin-fullblock", args: []string{"bs=4", "iflag=fullblock", "status=noxfer"}, stdin: "abcdefghij", seedTree: ddSeed},
		invocation{name: "stdin-to-file", args: []string{"of=out", "bs=3", "status=noxfer"}, stdin: "abcdefghij", seedTree: ddSeed},
	)

	return cases
}

func TestDD(t *testing.T) {
	requireParity(t, "dd", ddCases(t))
}

// `status=progress` writes one `\r`-prefixed transfer line per second,
// then the newline that separates the last of them from the report. It
// cannot be a corpus case, because the run it describes always ends in
// the third report line and that line carries a duration — so this
// compares the two implementations' stderr with the DURATIONS masked
// and everything else, the progress lines' own whole-second counts
// included, byte for byte.
//
// The seconds on a progress line are ROUNDED: GNU reads an elapsed
// 1.7 s as `2 s`. The input is a FIFO fed one byte and then another
// after a sleep, which is what makes that observable and makes both
// sides have copied the same two bytes when the tick lands. A record
// that outlasts several seconds still produces one line rather than one
// per second, since GNU's alarm handler only raises a flag that the
// copy loop reads at a record boundary.
func TestDDProgress(t *testing.T) {
	// Only a duration is masked: a whole number of seconds with no `.`
	// or `e` in it is a progress line's own count and stays compared.
	duration := regexp.MustCompile(`copied, [0-9]+[.e][0-9e.+-]* s,`)
	run := func(bin string, sleep time.Duration) string {
		t.Helper()
		dir := t.TempDir()
		fifo := filepath.Join(dir, "slow")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		fed := make(chan struct{})
		go func() {
			defer close(fed)
			// Opening for writing blocks until dd opens the read end,
			// so the sleep is the gap BETWEEN the two bytes rather
			// than before the first.
			f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
			if err != nil {
				return
			}
			defer f.Close()
			f.Write([]byte("a"))
			time.Sleep(sleep)
			f.Write([]byte("b"))
		}()
		inv := invocation{args: []string{"if=slow", "of=out", "bs=1", "status=progress"}, dir: dir}
		got := inv.run(t, bin, "dd")
		<-fed
		if got.exit != 0 {
			t.Fatalf("%s: exit = %d, want 0\nstderr:\n%s", bin, got.exit, got.stderr)
		}
		return duration.ReplaceAllString(string(got.stderr), "copied, T s,")
	}
	for _, c := range []struct {
		name  string
		sleep time.Duration
		// lines is how many `\r` progress lines the gap produces.
		lines int
	}{
		{"no tick", 100 * time.Millisecond, 0},
		{"one second rounds down", 1300 * time.Millisecond, 1},
		{"one second rounds up", 1700 * time.Millisecond, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			want := run(referenceBin(t, "dd"), c.sleep)
			got := run(fernBin(t, "dd"), c.sleep)
			if got != want {
				t.Errorf("stderr differs with the durations masked\n gnu: %q\nfern: %q", want, got)
			}
			if n := strings.Count(got, "\r"); n != c.lines {
				t.Errorf("%d progress lines, want %d: %q", n, c.lines, got)
			}
		})
	}
}

func TestDDHelp(t *testing.T) {
	requireHelp(t, "dd", []string{"--help"}, 0)
}

func TestDDVersion(t *testing.T) {
	requireVersion(t, "dd", []string{"--version"}, 0)
}

// The third report line is the one part of dd nothing can compare: it
// carries the elapsed time and a rate computed from it. Its SHAPE is
// still a contract — GNU writes `10 bytes copied, 6.3384e-05 s, 158
// kB/s`, and the human-readable pair appears in parentheses once the
// count reaches 1000 — so that is what this pins, on our own output
// rather than against GNU's.
var ddTransferSmall = regexp.MustCompile(`^10 bytes copied, [0-9.e+-]+ s, [0-9.]+ [kMGTPE]B/s\n$`)

var ddTransferLarge = regexp.MustCompile(`^2048 bytes \(2\.0 kB, 2\.0 KiB\) copied, [0-9.e+-]+ s, [0-9.]+ [kMGTPE]B/s\n$`)

func TestDDTransferLine(t *testing.T) {
	dir := t.TempDir()
	ddSeed(t, dir)
	for _, c := range []struct {
		name string
		args []string
		want *regexp.Regexp
	}{
		{"ten bytes", []string{"if=in10", "of=out"}, ddTransferSmall},
		{"two kibibytes", []string{"if=in5000", "of=out", "bs=1024", "count=2"}, ddTransferLarge},
	} {
		t.Run(c.name, func(t *testing.T) {
			inv := invocation{args: c.args, dir: dir}
			got := inv.run(t, fernBin(t, "dd"), "dd")
			if got.exit != 0 {
				t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.exit, got.stderr)
			}
			lines := strings.SplitAfter(string(got.stderr), "\n")
			if len(lines) < 3 {
				t.Fatalf("report has %d lines, want 3:\n%s", len(lines), got.stderr)
			}
			if got := lines[2]; !c.want.MatchString(got) {
				t.Errorf("transfer line = %q, want match %s", got, c.want)
			}
		})
	}
}
