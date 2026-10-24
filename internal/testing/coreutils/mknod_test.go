package coreutils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func init() {
	registerCorpus("mknod", mknodCases)
}

func mknodBare(t *testing.T, dir string) {
	t.Helper()
}

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

var mknodTypes = []string{"p", "c", "u", "b", "pipe", "pp", "character", "block", "cfoo", "q", "x", "", "P", "B", "C", "U", "1", "-"}

var mknodNumbers = []string{
	"0", "1", "00", "010", "08", "0x10", "0X1f", "0x", "+1", " 1", "1 ", " ", "",
	"a", "1_0", "1.5", "4294967295", "4294967296", "99999999999999999999",
	"18446744073709551616", "2147483648", "0xffffffff", "0777",
}

func mknodCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

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
	add(invocation{name: "a fifo with two operands", args: []string{"n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with a third operand", args: []string{"n", "p", "1"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with a major and a minor", args: []string{"n", "p", "1", "2"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with five operands", args: []string{"n", "p", "1", "2", "3"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with an extra operand", args: []string{"n", "p", "extra"}, seedTree: mknodBare})
	add(invocation{name: "a special file with six operands", args: []string{"n", "c", "1", "2", "3", "4"}, seedTree: mknodBare})
	add(invocation{name: "a fifo with six operands", args: []string{"n", "p", "1", "2", "3", "4"}, seedTree: mknodBare})
	add(invocation{name: "an empty name with a fifo", args: []string{"", "p"}, seedTree: mknodBare})
	add(invocation{name: "an empty name and an empty type", args: []string{"", ""}, seedTree: mknodBare})
	add(invocation{name: "an empty type with four operands", args: []string{"n", "", "1", "3"}, seedTree: mknodBare})

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

	for _, n := range mknodNumbers {
		add(invocation{name: fmt.Sprintf("major %q", n), args: []string{"n", "c", n, "3"}, seedTree: mknodBare})
		add(invocation{name: fmt.Sprintf("minor %q", n), args: []string{"n", "c", "1", n}, seedTree: mknodBare})
	}
	add(invocation{name: "a device that is NODEV", args: []string{"n", "c", "4294967295", "4294967295"}, seedTree: mknodBare})
	add(invocation{name: "a device that is NODEV in hex", args: []string{"n", "c", "0xffffffff", "0xffffffff"}, seedTree: mknodBare})
	add(invocation{name: "both numbers wrong reports the major", args: []string{"n", "c", "a", "b"}, seedTree: mknodBare})
	add(invocation{name: "a negative major", args: []string{"n", "c", "-1", "2"}, seedTree: mknodBare})
	add(invocation{name: "a negative minor", args: []string{"n", "c", "1", "-2"}, seedTree: mknodBare})
	add(invocation{name: "a negative major past dashdash", args: []string{"--", "n", "c", "-1", "2"}, seedTree: mknodBare})
	for _, ty := range []string{"b", "c", "u"} {
		add(invocation{name: "a " + ty + " device", args: []string{"n", ty, "1", "3"}, seedTree: mknodBare})
		add(invocation{name: "a " + ty + " device at zero", args: []string{"n", ty, "0", "0"}, seedTree: mknodBare})
		add(invocation{name: "a " + ty + " device with a mode", args: []string{"-m", "600", "n", ty, "1", "3"}, seedTree: mknodBare})
		add(invocation{name: "a " + ty + " device past the kernel limits", args: []string{"n", ty, "4096", "1048576"}, seedTree: mknodBare})
		add(invocation{name: "a " + ty + " device at the kernel limits", args: []string{"n", ty, "4095", "1048575"}, seedTree: mknodBare})
	}
	add(invocation{name: "a device over an existing file", args: []string{"f", "c", "1", "3"}, seedTree: mknodTaken})
	add(invocation{name: "a device in a missing directory", args: []string{"nodir/n", "c", "1", "3"}, seedTree: mknodBare})

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
	for _, mask := range []int{0o000, 0o022, 0o077, 0o002, 0o111, 0o222} {
		add(invocation{name: fmt.Sprintf("the default mode under %03o", mask), args: []string{"n", "p"}, seedTree: mknodBare, umask: withMask(mask)})
		add(invocation{name: fmt.Sprintf("mode 777 under %03o", mask), args: []string{"-m", "777", "n", "p"}, seedTree: mknodBare, umask: withMask(mask)})
		add(invocation{name: fmt.Sprintf("mode +w under %03o", mask), args: []string{"-m", "+w", "n", "p"}, seedTree: mknodBare, umask: withMask(mask)})
		add(invocation{name: fmt.Sprintf("mode = under %03o", mask), args: []string{"-m", "=", "n", "p"}, seedTree: mknodBare, umask: withMask(mask)})
		add(invocation{name: fmt.Sprintf("mode u+X under %03o", mask), args: []string{"-m", "u+X", "n", "p"}, seedTree: mknodBare, umask: withMask(mask)})
	}

	add(invocation{name: "invalid short option", args: []string{"-x", "n", "p"}})
	add(invocation{name: "unrecognized long option", args: []string{"--foo", "n", "p"}})
	add(invocation{name: "the empty long option", args: []string{"--=x"}})
	add(invocation{name: "help with a value", args: []string{"--help=x"}})
	add(invocation{name: "version with a value", args: []string{"--version=1"}})
	add(invocation{name: "an option after an operand permutes", args: []string{"n", "p", "-x"}})
	add(invocation{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"n", "p", "-Z"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: mknodBare})

	add(invocation{name: "Z alone", args: []string{"-Z", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a bare context", args: []string{"--context", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a context with a value", args: []string{"--context=foo", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "a context value twice warns twice", args: []string{"--context=a", "--context=b", "n", "p"}, seedTree: mknodBare})
	add(invocation{name: "the warning precedes the missing operand", args: []string{"--context=foo"}})
	add(invocation{name: "the warning precedes a bad type", args: []string{"--context=foo", "n", "q", "1", "2"}})

	for _, n := range []string{"a b", "a'b", "a\"b", "a\\b", "a\tb", "a\nb", "a~b", "plain"} {
		add(invocation{name: "taken " + fmt.Sprintf("%q", n), args: []string{"--", n, "p"}, seedTree: mknodNames(n)})
		add(invocation{name: "a bad type named " + fmt.Sprintf("%q", n), args: []string{"--", "n", n, "1", "2"}, seedTree: mknodBare})
		add(invocation{name: "a bad major named " + fmt.Sprintf("%q", n), args: []string{"--", "n", "c", n, "2"}, seedTree: mknodBare})
	}

	add(invocation{name: "a closed stdout", args: []string{"n", "p"}, seedTree: mknodBare, stdout: stdoutClosed})
	add(invocation{name: "a closed stdout with a diagnostic", args: []string{"f", "p"}, seedTree: mknodTaken, stdout: stdoutClosed})
	add(invocation{name: "a full stdout", args: []string{"n", "p"}, seedTree: mknodBare, stdout: stdoutFull})
	add(invocation{name: "a full stdout with a diagnostic", args: []string{"f", "p"}, seedTree: mknodTaken, stdout: stdoutFull})

	return cases
}

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
	requireVersion(t, "mknod", []string{"--v"}, 0)
	requireVersion(t, "mknod", []string{"--version", "n"}, 0)
}
