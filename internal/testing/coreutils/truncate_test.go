package coreutils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func init() {
	registerCorpus("truncate", truncateCases)
}

func truncateBare(t *testing.T, dir string) {
	t.Helper()
}

func truncateHello(t *testing.T, dir string) {
	t.Helper()
	truncateWrite(t, dir, "f", "hello")
}

func truncateRef(t *testing.T, dir string) {
	t.Helper()
	truncateWrite(t, dir, "f", "hello")
	truncateWrite(t, dir, "r", "abcdefgh")
	seedSymlink(t, dir, "r", "sr")
}

func truncateShapes(t *testing.T, dir string) {
	t.Helper()
	truncateWrite(t, dir, "f", "hello")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatalf("mkdir d: %v", err)
	}
	truncateWrite(t, dir, "ro", "hello")
	if err := os.Chmod(filepath.Join(dir, "ro"), 0o444); err != nil {
		t.Fatalf("chmod ro: %v", err)
	}
	seedSymlink(t, dir, "missing", "dl")
	seedSymlink(t, dir, "d", "sd")
	seedSymlink(t, dir, "loop", "loop")
}

func truncateNames(t *testing.T, dir string) {
	t.Helper()
	for _, n := range []string{"a b", "a'b", "a\"b", "a\\b", "a\tb", "a~b"} {
		truncateWrite(t, dir, n, "hello")
		if err := os.Chmod(filepath.Join(dir, n), 0o444); err != nil {
			t.Fatalf("chmod %s: %v", n, err)
		}
	}
}

func truncateWrite(t *testing.T, dir, name, body string) {
	t.Helper()
	seedWrite(t, dir, name, body)
	if err := os.Chmod(filepath.Join(dir, name), 0o644); err != nil {
		t.Fatalf("chmod %s: %v", name, err)
	}
}

var truncateSizes = []string{
	"5", "0", "00", "010", "100", "+5", "-5", "+0", "-0", "-100",
	"1K", "1k", "1M", "1m", "1G", "1g", "1T", "1t", "1P", "1E",
	"1Z", "1Y", "1R", "1Q", "1p", "1e", "1z", "1y", "1r", "1q",
	"1b", "1B", "b", "5x",
	"1KB", "1kB", "1KiB", "1kiB", "1KD", "1Ki", "1Kib", "1KIB", "1KiC", "1KK", "1E1",
	"K", "g", "t", "E", "Q", "R", "Z", "Y", " K", "0K", "+K", "-K",
	"<20", ">20", "/4", "%4", "<5", ">5", "/1", "%1", "<0", ">0",
	"< 3", " < 3", "  <  5", ">  5", "/3K", " +5", "5K", "5kB",
	"<+5", "+<5", "<-3", "<<5", "++5", "--5", "+ 5", "- 5",
	"", "x", "0x", "0x10", "0b101", "1.5", "1e3", "+", "-", "<", ">", "/", "%",
	"1 K", "5 ", " 5 ", "1,5", "5~", "a'b", "a\"b",
	"9223372036854775807", "9223372036854775808",
	"-9223372036854775808", "-9223372036854775809",
	"9223372036854775808x", "1Zx", "-1Z",
	"+9223372036854775807", "+9223372036854775806", "-9223372036854775807",
	"-1E", "+1E", "2P", "-1P",
	"%9223372036854775807", "%4611686018427387904",
	"/9223372036854775807", ">9223372036854775807", "<9223372036854775807",
}

var truncateMasks = []int{0o000, 0o022, 0o002, 0o077, 0o111, 0o222, 0o027}

func truncateCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

	add(invocation{name: "no arguments"})
	add(invocation{name: "dashdash alone", args: []string{"--"}})
	add(invocation{name: "no size and no reference", args: []string{"f"}, seedTree: truncateHello})
	add(invocation{name: "no-create without a size", args: []string{"-c", "f"}, seedTree: truncateHello})
	add(invocation{name: "io-blocks without a size", args: []string{"-o"}})
	add(invocation{name: "no-create alone", args: []string{"-c"}})
	add(invocation{name: "size with no operand", args: []string{"-s", "5"}})
	add(invocation{name: "size with no operand past dashdash", args: []string{"-s", "5", "--"}})
	add(invocation{name: "dashdash before the options", args: []string{"--", "-s", "5"}})
	add(invocation{name: "io-blocks and a size with no operand", args: []string{"-o", "-s", "1"}})
	add(invocation{name: "size requires an argument", args: []string{"-s"}})
	add(invocation{name: "reference requires an argument", args: []string{"-r"}})
	add(invocation{name: "the long reference eats the operand", args: []string{"--reference", "f"}, seedTree: truncateHello})
	add(invocation{name: "reference takes dashdash as its value", args: []string{"-r", "--", "f"}, seedTree: truncateHello})
	add(invocation{name: "a size then a reference with no value", args: []string{"-s", "+1", "-r"}})
	add(invocation{name: "help with a value", args: []string{"--help=x"}})
	add(invocation{name: "version with a value", args: []string{"--version=1"}})
	add(invocation{name: "invalid short option", args: []string{"-x", "f"}})
	add(invocation{name: "unrecognized long option", args: []string{"--foo", "f"}})
	add(invocation{name: "the empty long option", args: []string{"--=x"}})
	add(invocation{name: "an option after an operand permutes", args: []string{"f", "-x"}})
	add(invocation{name: "getopt runs before the size is read", args: []string{"-s", "1p", "-x", "f"}})
	add(invocation{name: "size by two-letter prefix", args: []string{"--si", "f"}, seedTree: truncateHello})
	add(invocation{name: "size by one-letter prefix", args: []string{"--s", "f"}, seedTree: truncateHello})
	add(invocation{name: "no-create by two-letter prefix", args: []string{"--no", "f"}, seedTree: truncateHello})
	add(invocation{name: "an empty long size value", args: []string{"--size=", "f"}, seedTree: truncateHello})
	add(invocation{name: "a glued short size", args: []string{"-s5", "f"}, seedTree: truncateHello})
	add(invocation{name: "a cluster before the glued size", args: []string{"-cs5", "f"}, seedTree: truncateHello})
	add(invocation{name: "the long spellings", args: []string{"--io-blocks", "--size=+1", "f"}, seedTree: truncateHello})

	for _, s := range truncateSizes {
		add(invocation{
			name:     fmt.Sprintf("size %q", s),
			args:     []string{"-s", s, "f"},
			seedTree: truncateHello,
		})
	}
	add(invocation{name: "round down to a multiple of zero", args: []string{"-s", "/0", "f"}, seedTree: truncateHello})
	add(invocation{name: "round up to a multiple of zero", args: []string{"-s", "%0", "f"}, seedTree: truncateHello})
	add(invocation{name: "division by zero outranks the missing operand", args: []string{"-s", "/0"}})
	add(invocation{name: "division by zero outranks the block size", args: []string{"-o", "-s", "/0", "f"}, seedTree: truncateHello})
	add(invocation{name: "the last size wins", args: []string{"-s", "5", "-s", "7", "f"}, seedTree: truncateHello})
	add(invocation{name: "a bad first size outranks a good second", args: []string{"-s", "1p", "-s", "5", "f"}, seedTree: truncateHello})
	add(invocation{name: "a bad second size", args: []string{"-s", "5", "-s", "1p", "f"}, seedTree: truncateHello})

	add(invocation{name: "a reference alone is absolute", args: []string{"-r", "r", "f"}, seedTree: truncateRef})
	add(invocation{name: "a reference and a relative size", args: []string{"-r", "r", "-s", "+2", "f"}, seedTree: truncateRef})
	add(invocation{name: "a reference and an absolute size", args: []string{"-r", "r", "-s", "5", "f"}, seedTree: truncateRef})
	add(invocation{name: "the size before the reference", args: []string{"-s", "+1", "-r", "r", "f"}, seedTree: truncateRef})
	add(invocation{name: "an absolute size outranks a missing reference", args: []string{"-s", "1", "--reference=nosuch", "f"}, seedTree: truncateHello})
	add(invocation{name: "a missing reference", args: []string{"-r", "nosuch", "f"}, seedTree: truncateHello})
	add(invocation{name: "a missing reference is quoted the file way", args: []string{"-r", "no'such", "f"}, seedTree: truncateHello})
	add(invocation{name: "a symlink as the reference is followed", args: []string{"-r", "sr", "f"}, seedTree: truncateRef})
	add(invocation{name: "the reference and the operand are one file", args: []string{"-r", "f", "-s", "+1", "f"}, seedTree: truncateHello})
	add(invocation{name: "a reference with each rounding modifier", args: []string{"-r", "r", "-s", "%3", "f"}, seedTree: truncateRef})
	add(invocation{name: "a reference rounded down", args: []string{"-r", "r", "-s", "/3", "f"}, seedTree: truncateRef})
	add(invocation{name: "a reference bounded above", args: []string{"-r", "r", "-s", "<3", "f"}, seedTree: truncateRef})
	add(invocation{name: "a reference bounded below", args: []string{"-r", "r", "-s", ">30", "f"}, seedTree: truncateRef})
	add(invocation{name: "the missing operand outranks the reference stat", args: []string{"-r", "nosuch"}})
	add(invocation{name: "the last reference wins", args: []string{"-r", "nosuch", "-r", "r", "f"}, seedTree: truncateRef})

	add(invocation{name: "io blocks", args: []string{"-o", "-s", "2", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks of zero", args: []string{"-o", "-s", "0", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks relative", args: []string{"-o", "-s", "+1", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks rounded up", args: []string{"-o", "-s", "%1", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks with a reference", args: []string{"-o", "-r", "r", "-s", "+1", "f"}, seedTree: truncateRef})
	add(invocation{name: "io blocks with a reference and no size", args: []string{"-o", "-r", "r", "f"}, seedTree: truncateRef})
	add(invocation{name: "io blocks overflow", args: []string{"-o", "-s", "9223372036854775807", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks overflow downward", args: []string{"-o", "-s", "-9223372036854775807", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks overflow from a suffix", args: []string{"-o", "-s", "4E", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks overflow names the file", args: []string{"-o", "-s", "9223372036854775807", "a'b"}, seedTree: truncateBare})

	add(invocation{name: "three files at once", args: []string{"-s", "5", "a", "b", "c"}, seedTree: truncateBare})
	add(invocation{name: "the same file three times", args: []string{"-s", "5", "f", "f", "f"}, seedTree: truncateHello})
	add(invocation{name: "an empty operand", args: []string{"-s", "5", ""}, seedTree: truncateBare})
	add(invocation{name: "a lone dash is a name", args: []string{"-s", "5", "-"}, seedTree: truncateBare})
	add(invocation{name: "dashdash then an option-shaped name", args: []string{"-s", "5", "--", "-x"}, seedTree: truncateBare})
	add(invocation{name: "a name that is not valid UTF-8", args: []string{"-s", "5", "\xff\xfe"}, seedTree: truncateBare})
	add(invocation{name: "a missing directory component", args: []string{"-s", "5", "nodir/x"}, seedTree: truncateBare})
	add(invocation{name: "a directory operand", args: []string{"-s", "5", "d"}, seedTree: truncateShapes})
	add(invocation{name: "a file with no write permission", args: []string{"-s", "5", "ro"}, seedTree: truncateShapes})
	add(invocation{name: "a dangling symlink is created through", args: []string{"-s", "5", "dl"}, seedTree: truncateShapes})
	add(invocation{name: "a symlink to a directory", args: []string{"-s", "5", "sd"}, seedTree: truncateShapes})
	add(invocation{name: "a symlink loop", args: []string{"-s", "5", "loop"}, seedTree: truncateShapes})
	add(invocation{name: "the run carries on past a failure", args: []string{"-s", "5", "a", "d", "b"}, seedTree: truncateShapes})
	add(invocation{name: "a creation whose extension fails", args: []string{"-s", "9223372036854775807", "new"}, seedTree: truncateBare})
	add(invocation{name: "an extension past the maximum", args: []string{"-s", "9223372036854775807", "f"}, seedTree: truncateHello})
	add(invocation{name: "a relative extension past the maximum", args: []string{"-s", "+9223372036854775807", "f"}, seedTree: truncateHello})

	add(invocation{name: "no-create skips a missing file", args: []string{"-c", "-s", "5", "nofile"}, seedTree: truncateBare})
	add(invocation{name: "no-create skips it silently mid-run", args: []string{"-c", "-s", "3", "f", "nofile"}, seedTree: truncateHello})
	add(invocation{name: "no-create skips a missing directory component", args: []string{"-c", "-s", "5", "nodir/x"}, seedTree: truncateBare})
	add(invocation{name: "no-create still reports a directory", args: []string{"-c", "-s", "5", "d"}, seedTree: truncateShapes})
	add(invocation{name: "no-create still reports a permission", args: []string{"-c", "-s", "5", "ro"}, seedTree: truncateShapes})
	add(invocation{name: "no-create skips a dangling symlink", args: []string{"-c", "-s", "5", "dl"}, seedTree: truncateShapes})
	add(invocation{name: "no-create on a symlink loop", args: []string{"-c", "-s", "5", "loop"}, seedTree: truncateShapes})
	add(invocation{name: "no-create relative on a missing file", args: []string{"--no-create", "--size=+1", "nofile"}, seedTree: truncateBare})

	for _, mask := range truncateMasks {
		add(invocation{
			name:     fmt.Sprintf("a created file under %03o", mask),
			args:     []string{"-s", "5", "new"},
			seedTree: truncateBare,
			umask:    withMask(mask),
		})
		add(invocation{
			name:     fmt.Sprintf("an existing file under %03o", mask),
			args:     []string{"-s", "5", "f"},
			seedTree: truncateHello,
			umask:    withMask(mask),
		})
	}

	for _, n := range []string{"a b", "a'b", "a\"b", "a\\b", "a\tb", "a~b"} {
		add(invocation{name: "cannot open " + fmt.Sprintf("%q", n), args: []string{"-s", "5", "--", n}, seedTree: truncateNames})
	}
	add(invocation{name: "an invalid size with a tab", args: []string{"-s", "a\tb", "f"}, seedTree: truncateHello})
	add(invocation{name: "an invalid size with an apostrophe", args: []string{"-s", "a'b", "f"}, seedTree: truncateHello})
	add(invocation{name: "an invalid size with a high byte", args: []string{"-s", "\xff", "f"}, seedTree: truncateHello})
	add(invocation{name: "an invalid size with a newline", args: []string{"-s", "a\nb", "f"}, seedTree: truncateHello})

	add(invocation{name: "a closed stdout", args: []string{"-s", "5", "f"}, seedTree: truncateHello, stdout: stdoutClosed})
	add(invocation{name: "a closed stdout with a diagnostic", args: []string{"-s", "5", "d"}, seedTree: truncateShapes, stdout: stdoutClosed})
	add(invocation{name: "a full stdout", args: []string{"-s", "5", "f"}, seedTree: truncateHello, stdout: stdoutFull})
	add(invocation{name: "a full stdout with a diagnostic", args: []string{"-s", "5", "d"}, seedTree: truncateShapes, stdout: stdoutFull})

	return cases
}

func TestTruncateParity(t *testing.T) {
	requireParity(t, "truncate", truncateCases(t))
}

func TestTruncateHelpVersion(t *testing.T) {
	requireHelp(t, "truncate", []string{"--help"}, 0)
	requireHelp(t, "truncate", []string{"--hel"}, 0)
	requireHelp(t, "truncate", []string{"--help", "extra"}, 0)
	requireHelp(t, "truncate", []string{"-s", "5", "f", "--help"}, 0)
	requireVersion(t, "truncate", []string{"--version"}, 0)
	requireVersion(t, "truncate", []string{"--vers"}, 0)
	requireVersion(t, "truncate", []string{"--version", "f"}, 0)
}
