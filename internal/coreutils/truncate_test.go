package coreutils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// truncate(1) is a SIZE grammar wrapped around one system call, so almost
// every case below is a `-s` operand and the file it lands on. The
// grammar has three layers and each has rules that no man page states:
//
//   - The modifier layer accepts blanks around `<` `>` `/` `%` but NOT
//     between `+` / `-` and the digits, because the sign is strtoimax's
//     rather than truncate's own — `-s ' +5'` is relative and `-s '+ 5'`
//     is not a number. Everything reported names the string as it stands
//     AFTER the modifier was taken, so `-s '<<5'` complains about `'<5'`.
//   - The suffix layer is upper case except `g`, `k`, `m` and `t`, so
//     `1P` is a size and `1p` is not; `K` with no digits is 1024; `KB`
//     and the obsolescent `KD` are powers of 1000 where `KiB` is 1024;
//     and `1Ki` and `1KIB` are neither.
//   - The meaning layer clamps a negative result to zero silently and
//     reports a positive one past the maximum, which is the asymmetry
//     `-s -100` and `-s +9223372036854775806` show on one 5-byte file.
//
// The `-o` cases depend on st_blksize, which is the FILE's rather than a
// constant. Both sides read the same file on the same filesystem, so the
// comparison holds whatever that number is — but it is why the `-o`
// overflow diagnostic, which prints the block size, cannot be predicted
// from the case.
//
// Three properties this corpus deliberately does not reach. A `-s` that
// leaves a file so large the extension fails is only ever EFBIG here,
// because nothing sets RLIMIT_FSIZE. The fifo operand GNU answers ENXIO
// for on Darwin BLOCKS on Linux, so there is no case for it. And a
// REFERENCE whose st_size is not a usable file size — a directory, or a
// symlink to one — is the one thing the supported reference versions
// disagree about among themselves: 9.1 asks for OFF_T_MAX and takes the
// EFBIG, 9.10 uses st_size and succeeds. No implementation can match
// both, so `-r` is compared over the shapes every version agrees on.
// Fern follows 9.10, which is the version docs/COREUTILS.md names.

func init() {
	registerCorpus("truncate", truncateCases)
}

// truncateBare is an empty directory: everything in the tree comparison
// is something the run created.
func truncateBare(t *testing.T, dir string) {
	t.Helper()
}

// truncateHello is the one-file fixture most of the grammar runs
// against. Five bytes is small enough that every rounding modifier moves
// it and large enough that shrinking is visible.
func truncateHello(t *testing.T, dir string) {
	t.Helper()
	truncateWrite(t, dir, "f", "hello")
}

// truncateRef adds the `-r` reference, at a different size from the
// operand so a case cannot pass by coincidence.
func truncateRef(t *testing.T, dir string) {
	t.Helper()
	truncateWrite(t, dir, "f", "hello")
	truncateWrite(t, dir, "r", "abcdefgh")
	truncateLink(t, "r", dir, "sr")
}

// truncateShapes holds one of every name the open can fail on: a
// directory, a file with no write permission, a dangling symlink (which
// the open FOLLOWS and creates through), a symlink to a directory and a
// symlink to itself.
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
	truncateLink(t, "missing", dir, "dl")
	truncateLink(t, "d", dir, "sd")
	truncateLink(t, "loop", dir, "loop")
}

// truncateNames seeds the names whose diagnostics show GNU's quoting: an
// apostrophe forces the double-quoted form and a double quote forces the
// single-quoted one.
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
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	// WriteFile applies the creation mask, which a case may have changed;
	// Chmod sets exactly the bits asked for, so the fixture is the same
	// on both sides whatever mask the case runs under.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod %s: %v", name, err)
	}
}

func truncateLink(t *testing.T, target, dir, name string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		t.Fatalf("symlink %s: %v", name, err)
	}
}

// The SIZE operands that name a size, each run against a five-byte file.
// Grouped by the rule each one is there for; the value itself is never
// written down, because GNU is the oracle.
var truncateSizes = []string{
	// plain, and the two ends of the sign rule
	"5", "0", "00", "010", "100", "+5", "-5", "+0", "-0", "-100",
	// every suffix letter, in both cases where GNU takes both
	"1K", "1k", "1M", "1m", "1G", "1g", "1T", "1t", "1P", "1E",
	"1Z", "1Y", "1R", "1Q", "1p", "1e", "1z", "1y", "1r", "1q",
	// the b/B/c/w suffixes gnulib has and truncate does not accept
	"1b", "1B", "b", "5x",
	// the base-1000 and base-1024 spellings, and the near misses
	"1KB", "1kB", "1KiB", "1kiB", "1KD", "1Ki", "1Kib", "1KIB", "1KiC", "1KK", "1E1",
	// a bare suffix is the number 1
	"K", "g", "t", "E", "Q", "R", "Z", "Y", " K", "0K", "+K", "-K",
	// the four rounding modifiers, and blanks around them
	"<20", ">20", "/4", "%4", "<5", ">5", "/1", "%1", "<0", ">0",
	"< 3", " < 3", "  <  5", ">  5", "/3K", " +5", "5K", "5kB",
	// a second modifier, and a sign that is not one
	"<+5", "+<5", "<-3", "<<5", "++5", "--5", "+ 5", "- 5",
	// nothing numeric at all
	"", "x", "0x", "0x10", "0b101", "1.5", "1e3", "+", "-", "<", ">", "/", "%",
	"1 K", "5 ", " 5 ", "1,5", "5~", "a'b", "a\"b",
	// the ends of the representable range, and the two overflow reports
	"9223372036854775807", "9223372036854775808",
	"-9223372036854775808", "-9223372036854775809",
	"9223372036854775808x", "1Zx", "-1Z",
	"+9223372036854775807", "+9223372036854775806", "-9223372036854775807",
	"-1E", "+1E", "2P", "-1P",
	// the rounding modifiers at the top of the range, where GNU's
	// arithmetic is unsigned and the result it stores is not
	"%9223372036854775807", "%4611686018427387904",
	"/9223372036854775807", ">9223372036854775807", "<9223372036854775807",
}

// The masks a creating run is compared under. The mode a new file ends
// up with is `0666 & ~umask` and nothing in the utility can change it,
// so these prove the creation goes through the mask rather than around
// it.
var truncateMasks = []int{0o000, 0o022, 0o002, 0o077, 0o111, 0o222, 0o027}

func truncateCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

	// ---- arity and the option-shaped faults ----
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
	// `--si` and `--s` are unique prefixes of `--size`, so each eats the
	// operand and the number it is then handed is the file name.
	add(invocation{name: "size by two-letter prefix", args: []string{"--si", "f"}, seedTree: truncateHello})
	add(invocation{name: "size by one-letter prefix", args: []string{"--s", "f"}, seedTree: truncateHello})
	add(invocation{name: "no-create by two-letter prefix", args: []string{"--no", "f"}, seedTree: truncateHello})
	add(invocation{name: "an empty long size value", args: []string{"--size=", "f"}, seedTree: truncateHello})
	add(invocation{name: "a glued short size", args: []string{"-s5", "f"}, seedTree: truncateHello})
	add(invocation{name: "a cluster before the glued size", args: []string{"-cs5", "f"}, seedTree: truncateHello})
	add(invocation{name: "the long spellings", args: []string{"--io-blocks", "--size=+1", "f"}, seedTree: truncateHello})

	// ---- the SIZE grammar ----
	for _, s := range truncateSizes {
		add(invocation{
			name:     fmt.Sprintf("size %q", s),
			args:     []string{"-s", s, "f"},
			seedTree: truncateHello,
		})
	}
	// The two rounding modifiers whose operand is zero are a division,
	// and the refusal comes before the missing operand rather than after.
	add(invocation{name: "round down to a multiple of zero", args: []string{"-s", "/0", "f"}, seedTree: truncateHello})
	add(invocation{name: "round up to a multiple of zero", args: []string{"-s", "%0", "f"}, seedTree: truncateHello})
	add(invocation{name: "division by zero outranks the missing operand", args: []string{"-s", "/0"}})
	add(invocation{name: "division by zero outranks the block size", args: []string{"-o", "-s", "/0", "f"}, seedTree: truncateHello})
	// The last -s wins, but an earlier one that is not a number still
	// ends the run: the value is read where it is written.
	add(invocation{name: "the last size wins", args: []string{"-s", "5", "-s", "7", "f"}, seedTree: truncateHello})
	add(invocation{name: "a bad first size outranks a good second", args: []string{"-s", "1p", "-s", "5", "f"}, seedTree: truncateHello})
	add(invocation{name: "a bad second size", args: []string{"-s", "5", "-s", "1p", "f"}, seedTree: truncateHello})

	// ---- -r ----
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
	// The reference is stat'ed only once there is an operand to use it on.
	add(invocation{name: "the missing operand outranks the reference stat", args: []string{"-r", "nosuch"}})
	// The last -r wins, and the reference the run actually reads is it.
	add(invocation{name: "the last reference wins", args: []string{"-r", "nosuch", "-r", "r", "f"}, seedTree: truncateRef})

	// ---- -o ----
	add(invocation{name: "io blocks", args: []string{"-o", "-s", "2", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks of zero", args: []string{"-o", "-s", "0", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks relative", args: []string{"-o", "-s", "+1", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks rounded up", args: []string{"-o", "-s", "%1", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks with a reference", args: []string{"-o", "-r", "r", "-s", "+1", "f"}, seedTree: truncateRef})
	add(invocation{name: "io blocks with a reference and no size", args: []string{"-o", "-r", "r", "f"}, seedTree: truncateRef})
	// The block multiply overflows long before the size does, and the
	// diagnostic prints both factors.
	add(invocation{name: "io blocks overflow", args: []string{"-o", "-s", "9223372036854775807", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks overflow downward", args: []string{"-o", "-s", "-9223372036854775807", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks overflow from a suffix", args: []string{"-o", "-s", "4E", "f"}, seedTree: truncateHello})
	add(invocation{name: "io blocks overflow names the file", args: []string{"-o", "-s", "9223372036854775807", "a'b"}, seedTree: truncateBare})

	// ---- the operands ----
	add(invocation{name: "three files at once", args: []string{"-s", "5", "a", "b", "c"}, seedTree: truncateBare})
	add(invocation{name: "the same file three times", args: []string{"-s", "5", "f", "f", "f"}, seedTree: truncateHello})
	add(invocation{name: "an empty operand", args: []string{"-s", "5", ""}, seedTree: truncateBare})
	add(invocation{name: "a lone dash is a name", args: []string{"-s", "5", "-"}, seedTree: truncateBare})
	add(invocation{name: "dashdash then an option-shaped name", args: []string{"-s", "5", "--", "-x"}, seedTree: truncateBare})
	add(invocation{name: "a name that is not valid UTF-8", args: []string{"-s", "5", "\xff\xfe"}, seedTree: truncateBare})
	add(invocation{name: "a missing directory component", args: []string{"-s", "5", "nodir/x"}, seedTree: truncateBare})
	add(invocation{name: "a directory operand", args: []string{"-s", "5", "d"}, seedTree: truncateShapes})
	add(invocation{name: "a file with no write permission", args: []string{"-s", "5", "ro"}, seedTree: truncateShapes})
	// The open follows the final symlink, so a dangling one is created
	// THROUGH rather than replaced.
	add(invocation{name: "a dangling symlink is created through", args: []string{"-s", "5", "dl"}, seedTree: truncateShapes})
	add(invocation{name: "a symlink to a directory", args: []string{"-s", "5", "sd"}, seedTree: truncateShapes})
	add(invocation{name: "a symlink loop", args: []string{"-s", "5", "loop"}, seedTree: truncateShapes})
	// A failed operand costs the status and not the run.
	add(invocation{name: "the run carries on past a failure", args: []string{"-s", "5", "a", "d", "b"}, seedTree: truncateShapes})
	// The file is CREATED before the size is known, so one whose
	// extension fails is left behind at zero.
	add(invocation{name: "a creation whose extension fails", args: []string{"-s", "9223372036854775807", "new"}, seedTree: truncateBare})
	add(invocation{name: "an extension past the maximum", args: []string{"-s", "9223372036854775807", "f"}, seedTree: truncateHello})
	add(invocation{name: "a relative extension past the maximum", args: []string{"-s", "+9223372036854775807", "f"}, seedTree: truncateHello})

	// ---- -c ----
	add(invocation{name: "no-create skips a missing file", args: []string{"-c", "-s", "5", "nofile"}, seedTree: truncateBare})
	add(invocation{name: "no-create skips it silently mid-run", args: []string{"-c", "-s", "3", "f", "nofile"}, seedTree: truncateHello})
	add(invocation{name: "no-create skips a missing directory component", args: []string{"-c", "-s", "5", "nodir/x"}, seedTree: truncateBare})
	add(invocation{name: "no-create still reports a directory", args: []string{"-c", "-s", "5", "d"}, seedTree: truncateShapes})
	add(invocation{name: "no-create still reports a permission", args: []string{"-c", "-s", "5", "ro"}, seedTree: truncateShapes})
	add(invocation{name: "no-create skips a dangling symlink", args: []string{"-c", "-s", "5", "dl"}, seedTree: truncateShapes})
	add(invocation{name: "no-create on a symlink loop", args: []string{"-c", "-s", "5", "loop"}, seedTree: truncateShapes})
	add(invocation{name: "no-create relative on a missing file", args: []string{"--no-create", "--size=+1", "nofile"}, seedTree: truncateBare})

	// ---- the mode a created file gets ----
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

	// ---- quoting: the size and the file name are quoted differently ----
	for _, n := range []string{"a b", "a'b", "a\"b", "a\\b", "a\tb", "a~b"} {
		add(invocation{name: "cannot open " + fmt.Sprintf("%q", n), args: []string{"-s", "5", "--", n}, seedTree: truncateNames})
	}
	add(invocation{name: "an invalid size with a tab", args: []string{"-s", "a\tb", "f"}, seedTree: truncateHello})
	add(invocation{name: "an invalid size with an apostrophe", args: []string{"-s", "a'b", "f"}, seedTree: truncateHello})
	add(invocation{name: "an invalid size with a high byte", args: []string{"-s", "\xff", "f"}, seedTree: truncateHello})
	add(invocation{name: "an invalid size with a newline", args: []string{"-s", "a\nb", "f"}, seedTree: truncateHello})

	// ---- the write-failure paths ----
	// truncate writes nothing to stdout, so a stdout that cannot be
	// written is never noticed — on either side.
	add(invocation{name: "a closed stdout", args: []string{"-s", "5", "f"}, seedTree: truncateHello, stdout: stdoutClosed})
	add(invocation{name: "a closed stdout with a diagnostic", args: []string{"-s", "5", "d"}, seedTree: truncateShapes, stdout: stdoutClosed})
	add(invocation{name: "a full stdout", args: []string{"-s", "5", "f"}, seedTree: truncateHello, stdout: stdoutFull})
	add(invocation{name: "a full stdout with a diagnostic", args: []string{"-s", "5", "d"}, seedTree: truncateShapes, stdout: stdoutFull})

	return cases
}

func TestTruncateParity(t *testing.T) {
	requireParity(t, "truncate", truncateCases(t))
}

// The two options whose TEXT is ours by design (docs/COREUTILS.md).
// Everything else about them still has to match GNU, including that an
// operand on either side changes nothing: `truncate f --help` prints the
// help and truncates nothing.
func TestTruncateHelpVersion(t *testing.T) {
	requireHelp(t, "truncate", []string{"--help"}, 0)
	requireHelp(t, "truncate", []string{"--hel"}, 0)
	requireHelp(t, "truncate", []string{"--help", "extra"}, 0)
	requireHelp(t, "truncate", []string{"-s", "5", "f", "--help"}, 0)
	requireVersion(t, "truncate", []string{"--version"}, 0)
	requireVersion(t, "truncate", []string{"--vers"}, 0)
	requireVersion(t, "truncate", []string{"--version", "f"}, 0)
}
