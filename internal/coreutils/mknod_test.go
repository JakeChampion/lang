package coreutils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// mknod(1) shares mkfifo's MODE rules exactly — base 0666, permission
// bits only, the bare `invalid mode` refusal — and the cases below do not
// repeat that sweep. What is mknod's own, and what most of this corpus
// is, is the TYPE operand and the arity that hangs off it.
//
// Only the FIRST BYTE of TYPE is read (`mknod n pipe` is a fifo and
// `mknod n block 1 3` is a block device), and the byte decides how many
// operands there should be. So the ARITY diagnostics depend on the type,
// and a TYPE that is not one at all is reported only when there happen
// to be exactly four operands: `mknod n q` is a missing operand and
// `mknod n q 1 2` is `invalid device type 'q'`. The cases below walk
// every (type, count) pair from 0 to 5 because no two adjacent ones
// behave alike, and because two of them print a SECOND line that the
// count alone decides — `Fifos do not have …` at exactly four, `Special
// files require …` at exactly two.
//
// The `-m` ordering is the one place mknod and mkfifo disagree: mknod
// compiles the MODE before it counts operands, so `mknod -m bogus` is
// the bad mode where `mkfifo -m bogus` is the missing operand.
//
// A character or block node needs CAP_MKNOD, so on every machine this
// corpus runs on those cases are EPERM on both sides and the node is
// never created. That is a real comparison — the errno, the message and
// the empty tree all match — but it means the corpus proves the DEVICE
// NUMBER only where it can create one, which is a run as root. The
// harness compares a device's raw dev_t, so such a run does check it.

func init() {
	registerCorpus("mknod", mknodCases)
}

func mknodBare(t *testing.T, dir string) {
	t.Helper()
}

// mknodTaken holds the names the creation can collide with.
func mknodTaken(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o644); err != nil {
		t.Fatalf("write f: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, "f"), 0o644); err != nil {
		t.Fatalf("chmod f: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatalf("mkdir d: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatalf("chmod d: %v", err)
	}
}

// The TYPE operands, including the ones only their first byte makes
// valid and the ones that are refused.
var mknodTypes = []string{"p", "c", "u", "b", "pipe", "pp", "character", "block", "cfoo", "q", "x", "", "P", "B", "C", "U", "1", "-"}

// The MAJOR / MINOR operands, which are gnulib's base-zero strtoumax
// with no suffixes: the prefix picks the base, a leading blank or `+` is
// accepted and a trailing one is not, and the value has to fit in 32
// bits.
var mknodNumbers = []string{
	"0", "1", "00", "010", "08", "0x10", "0X1f", "0x", "+1", " 1", "1 ", " ", "",
	"a", "1_0", "1.5", "4294967295", "4294967296", "99999999999999999999",
	"18446744073709551616", "2147483648", "0xffffffff", "0777",
}

func mknodCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

	// ---- the arity, per type ----
	// Every (type, operand count) pair from zero operands to five. This
	// is the substance: no two adjacent counts report alike, and the
	// second line is decided by the count rather than by the type.
	add(invocation{name: "no operands", seedTree: mknodBare})
	add(invocation{name: "a name and no type", args: []string{"n"}, seedTree: mknodBare})
	for _, ty := range mknodTypes {
		full := []string{"n", ty, "1", "3", "extra"}
		for n := 2; n <= len(full); n++ {
			add(invocation{
				name:     fmt.Sprintf("type %q with %d operands", ty, n),
				args:     append([]string(nil), full[:n]...),
				seedTree: mknodBare,
			})
		}
	}
	// The same walk with a type whose first byte is `p`, so the fifo arm
	// is the one being counted rather than the special-file arm.
	add(invocation{name: "a fifo with two operands", args: []string{"n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with a third operand", args: []string{"n", "p", "1"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with a major and a minor", args: []string{"n", "p", "1", "2"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with five operands", args: []string{"n", "p", "1", "2", "3"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with an extra operand", args: []string{"n", "p", "extra"}, seedTree: mknodBare})
	// The `extra operand` a fifo names is the THIRD, and the one a
	// special file names is the FIFTH.
	add(invocation{name: "a special file with six operands", args: []string{"n", "c", "1", "2", "3", "4"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with six operands", args: []string{"n", "p", "1", "2", "3", "4"}, seedTree: mknodBare})
	// An empty NAME is still a NAME, and an empty TYPE is not a type.
	add(invocation{name: "an empty name with a fifo", args: []string{"", "p"}, seedTree: mknodBare})
	add(invocation{name: "an empty name and an empty type", args: []string{"", ""}, seedTree: mknodBare})
	add(invocation{name: "an empty type with four operands", args: []string{"n", "", "1", "3"}, seedTree: mknodBare})

	// ---- what a fifo run does ----
	add(invocation{name: "a fifo", args: []string{"n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a fifo by its long name", args: []string{"n", "pipe"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with a mode", args: []string{"-m", "700", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a fifo over a file", args: []string{"f", "p"}, seedTree: mknodTaken})
	add(invocation{name: "a fifo over a directory", args: []string{"d", "p"}, seedTree: mknodTaken})
	add(invocation{name: "a fifo in a missing directory", args: []string{"nodir/p", "p"}, seedTree: mknodBare})
	add(invocation{name: "a fifo named dash", args: []string{"-", "p"}, seedTree: mknodBare})
	add(invocation{name: "a fifo past dashdash", args: []string{"--", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with a trailing slash", args: []string{"n/", "p"}, seedTree: mknodBare})
	add(invocation{name: "a fifo whose name is not valid UTF-8", args: []string{"--", "\xff\xfe", "p"}, seedTree: mknodBare})

	// ---- the device numbers ----
	for _, n := range mknodNumbers {
		add(invocation{name: fmt.Sprintf("major %q", n), args: []string{"n", "c", n, "3"}, seedTree: mknodBare})
		add(invocation{name: fmt.Sprintf("minor %q", n), args: []string{"n", "c", "1", n}, seedTree: mknodBare})
	}
	// The major is read before the minor, so a pair that is wrong twice
	// reports the first.
	add(invocation{name: "both numbers wrong reports the major", args: []string{"n", "c", "a", "b"}, seedTree: mknodBare})
	// A negative number never reaches the parse: getopt has read it as an
	// option first, dashdash or no dashdash.
	add(invocation{name: "a negative major", args: []string{"n", "c", "-1", "2"}, seedTree: mknodBare})
	add(invocation{name: "a negative minor", args: []string{"n", "c", "1", "-2"}, seedTree: mknodBare})
	add(invocation{name: "a negative major past dashdash", args: []string{"--", "n", "c", "-1", "2"}, seedTree: mknodBare})
	// The three types that take a pair, each with a pair the kernel
	// accepts and one at the edge of what it does.
	for _, ty := range []string{"b", "c", "u"} {
		add(invocation{name: "a " + ty + " device", args: []string{"n", ty, "1", "3"}, seedTree: mknodBare})
		add(invocation{name: "a " + ty + " device at zero", args: []string{"n", ty, "0", "0"}, seedTree: mknodBare})
		add(invocation{name: "a " + ty + " device with a mode", args: []string{"-m", "600", "n", ty, "1", "3"}, seedTree: mknodBare})
		add(invocation{name: "a " + ty + " device past the kernel limits", args: []string{"n", ty, "4096", "1048576"}, seedTree: mknodBare})
		add(invocation{name: "a " + ty + " device at the kernel limits", args: []string{"n", ty, "4095", "1048575"}, seedTree: mknodBare})
	}
	add(invocation{name: "a device over an existing file", args: []string{"f", "c", "1", "3"}, seedTree: mknodTaken})
	add(invocation{name: "a device in a missing directory", args: []string{"nodir/n", "c", "1", "3"}, seedTree: mknodBare})

	// ---- -m, and the ordering that differs from mkfifo ----
	add(invocation{name: "a bad mode outranks the missing operand", args: []string{"-m", "bogus"}})
	add(invocation{name: "a bad mode with one operand", args: []string{"-m", "bogus", "n"}})
	add(invocation{name: "a non-permission mode outranks the missing operand", args: []string{"-m", "7777", "n"}})
	add(invocation{name: "a non-permission mode", args: []string{"-m", "+t", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "an empty long mode value", args: []string{"--mode=", "n", "p"}})
	add(invocation{name: "mode requires an argument", args: []string{"-m"}})
	add(invocation{name: "mode by one-letter prefix", args: []string{"--m=700", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a glued short mode", args: []string{"-m700", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a mode glued behind Z", args: []string{"-Zm700", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "the last mode wins", args: []string{"-m", "666", "-m", "700", "n", "p"}, seedTree: mknodBare})
	// The mode is 0666 through the mask by default and the MODE's own
	// through a chmod that the mask does not reach.
	for _, mask := range []int{0o000, 0o022, 0o077, 0o002, 0o111, 0o222} {
		add(invocation{name: fmt.Sprintf("the default mode under %03o", mask), args: []string{"n", "p"}, seedTree: mknodBare, umask: withMask(mask)})
		add(invocation{name: fmt.Sprintf("mode 777 under %03o", mask), args: []string{"-m", "777", "n", "p"}, seedTree: mknodBare, umask: withMask(mask)})
		add(invocation{name: fmt.Sprintf("mode +w under %03o", mask), args: []string{"-m", "+w", "n", "p"}, seedTree: mknodBare, umask: withMask(mask)})
		add(invocation{name: fmt.Sprintf("mode = under %03o", mask), args: []string{"-m", "=", "n", "p"}, seedTree: mknodBare, umask: withMask(mask)})
		add(invocation{name: fmt.Sprintf("mode u+X under %03o", mask), args: []string{"-m", "u+X", "n", "p"}, seedTree: mknodBare, umask: withMask(mask)})
	}

	// ---- getopt faults ----
	add(invocation{name: "invalid short option", args: []string{"-x", "n", "p"}})
	add(invocation{name: "unrecognized long option", args: []string{"--foo", "n", "p"}})
	add(invocation{name: "the empty long option", args: []string{"--=x"}})
	add(invocation{name: "help with a value", args: []string{"--help=x"}})
	add(invocation{name: "version with a value", args: []string{"--version=1"}})
	add(invocation{name: "an option after an operand permutes", args: []string{"n", "p", "-x"}})
	add(invocation{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"n", "p", "-Z"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: mknodBare})

	// ---- -Z and --context ----
	add(invocation{name: "Z alone", args: []string{"-Z", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a bare context", args: []string{"--context", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a context with a value", args: []string{"--context=foo", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a context value twice warns twice", args: []string{"--context=a", "--context=b", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "the warning precedes the missing operand", args: []string{"--context=foo"}})
	add(invocation{name: "the warning precedes a bad type", args: []string{"--context=foo", "n", "q", "1", "2"}})

	// ---- quoting ----
	// The creation failure is the one diagnostic here that quotes only
	// when the name needs it; every other one quotes unconditionally.
	for _, n := range []string{"a b", "a'b", "a\"b", "a\\b", "a\tb", "a\nb", "a~b", "plain"} {
		add(invocation{name: "taken " + fmt.Sprintf("%q", n), args: []string{"--", n, "p"}, seedTree: mknodNames(n)})
		add(invocation{name: "a bad type named " + fmt.Sprintf("%q", n), args: []string{"--", "n", n, "1", "2"}, seedTree: mknodBare})
		add(invocation{name: "a bad major named " + fmt.Sprintf("%q", n), args: []string{"--", "n", "c", n, "2"}, seedTree: mknodBare})
	}

	// ---- the write-failure paths ----
	add(invocation{name: "a closed stdout", args: []string{"n", "p"}, seedTree: mknodBare, stdout: stdoutClosed})
	add(invocation{name: "a closed stdout with a diagnostic", args: []string{"f", "p"}, seedTree: mknodTaken, stdout: stdoutClosed})
	add(invocation{name: "a full stdout", args: []string{"n", "p"}, seedTree: mknodBare, stdout: stdoutFull})
	add(invocation{name: "a full stdout with a diagnostic", args: []string{"f", "p"}, seedTree: mknodTaken, stdout: stdoutFull})

	return cases
}

// mknodNames seeds one file under `name`, so the creation over it fails
// and the diagnostic has to quote it.
func mknodNames(name string) func(*testing.T, string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("chmod %q: %v", name, err)
		}
	}
}

func TestMknodParity(t *testing.T) {
	requireParity(t, "mknod", mknodCases(t))
}

func TestMknodHelpVersion(t *testing.T) {
	requireHelp(t, "mknod", []string{"--help"}, 0)
	requireHelp(t, "mknod", []string{"--hel"}, 0)
	requireHelp(t, "mknod", []string{"--help", "extra"}, 0)
	requireHelp(t, "mknod", []string{"n", "p", "--help"}, 0)
	requireVersion(t, "mknod", []string{"--version"}, 0)
	requireVersion(t, "mknod", []string{"--vers"}, 0)
	// `--v` is unambiguous here: there is no `--verbose` to collide with.
	requireVersion(t, "mknod", []string{"--v"}, 0)
	requireVersion(t, "mknod", []string{"--version", "n"}, 0)
}
