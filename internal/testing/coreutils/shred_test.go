package coreutils

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func init() {
	registerCorpus("shred", shredCases)
}

func varied(n int) string {
	b := make([]byte, n)
	x := uint32(0x1234567)
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return string(b)
}

func shredSeed(t *testing.T, dir string) {
	t.Helper()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("f", "hello world")
	write("g", "ab")
	write("empty", "")
	write("0", "taken")
	write("00", "taken")
	write("src", strings.Repeat("Q", 20000))
	write("vsrc", varied(40000))
	write("one", "x")
	write("small", varied(100))
	write("blk", varied(4096))
	write("over", varied(5000))
	write("dry", varied(50))
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("sub", "deep"), "nested")
	write("ro", "readonly")
	if err := os.Chmod(filepath.Join(dir, "ro"), 0o444); err != nil {
		t.Fatal(err)
	}
}

func shredTypesSeed(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, "p")
	seedFifo(t, p)
	fd, err := syscall.Open(p, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open fifo for reading: %v", err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	shortDir, err := os.MkdirTemp("", "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(shortDir) })
	ln, err := net.Listen("unix", filepath.Join(shortDir, "s"))
	if err != nil {
		t.Fatalf("unix socket: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	if err := os.Rename(filepath.Join(shortDir, "s"), filepath.Join(dir, "sock")); err != nil {
		t.Fatalf("move the socket into the fixture tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func shredCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, seedTree: shredSeed})
	}

	for _, n := range []string{"1", "2", "3"} {
		add("source-n"+n, "--random-source=src", "-n", n, "-v", "f")
		add("source-n"+n+"-zero", "--random-source=src", "-n", n, "-v", "-z", "f")
		add("source-n"+n+"-exact", "--random-source=src", "-n", n, "-v", "-x", "f")
	}
	add("source-default-passes", "--random-source=src", "-v", "f")
	add("source-two-operands", "--random-source=src", "-n", "1", "-v", "f", "g")
	add("source-nested", "--random-source=src", "-n", "1", "-v", "sub/deep")
	add("source-empty-file", "--random-source=src", "-n", "1", "-v", "empty")
	add("source-size", "--random-source=src", "-n", "1", "-v", "-s", "5", "f")
	add("source-size-suffix", "--random-source=src", "-n", "1", "-v", "-s", "1K", "f")
	add("source-size-exact", "--random-source=src", "-n", "1", "-v", "-s", "3", "-x", "f")
	add("source-force", "--random-source=src", "-n", "1", "-v", "-f", "f")
	add("source-force-readonly", "--random-source=src", "-n", "1", "-v", "-f", "ro")
	add("source-readonly-no-force", "--random-source=src", "-n", "1", "-v", "ro")

	add("varied-one", "--random-source=vsrc", "-n", "1", "-v", "one")
	add("varied-small", "--random-source=vsrc", "-n", "1", "-v", "small")
	add("varied-small-n2", "--random-source=vsrc", "-n", "2", "-v", "small")
	add("varied-small-zero", "--random-source=vsrc", "-n", "1", "-v", "-z", "small")
	add("varied-small-exact", "--random-source=vsrc", "-n", "1", "-v", "-x", "small")
	add("varied-block", "--random-source=vsrc", "-n", "1", "-v", "blk")
	add("varied-over-block", "--random-source=vsrc", "-n", "1", "-v", "over")
	add("varied-over-block-n2", "--random-source=vsrc", "-n", "2", "-v", "over")
	add("varied-size-grows", "--random-source=vsrc", "-n", "1", "-v", "-s", "5000", "small")
	add("varied-size-shrinks", "--random-source=vsrc", "-n", "1", "-v", "-s", "100", "blk")
	add("varied-two-operands", "--random-source=vsrc", "-n", "1", "-v", "one", "small")

	add("dry-source-small", "--random-source=dry", "-n", "1", "-v", "small")
	add("dry-source-one", "--random-source=dry", "-n", "1", "-v", "one")
	add("dry-source-quiet", "--random-source=dry", "-n", "1", "one")
	add("dry-source-empty", "--random-source=empty", "-n", "1", "-v", "f")
	add("dry-source-second-operand", "--random-source=dry", "-n", "1", "-v", "-x", "-s", "30", "f", "g")
	add("dry-source-removal-refused", "--random-source=empty", "-n", "1", "-v", "-u", "f")
	add("dry-source-zero-pass-only", "--random-source=empty", "-n", "0", "-z", "-v", "f")

	add("zero-only", "-n", "0", "-z", "-v", "f")
	add("zero-only-exact", "-n", "0", "-z", "-v", "-x", "f")
	add("zero-only-two", "-n", "0", "-z", "f", "g")
	add("zero-only-quiet", "-n", "0", "-z", "f")

	for _, n := range []string{"0", "1", "2", "3"} {
		add("remove-n"+n, "-n", n, "-v", "-u", "f")
	}
	add("remove-zero-pass", "-n", "1", "-v", "-z", "-u", "f")
	add("remove-two-char-name", "-n", "0", "-v", "-u", "00")
	add("remove-collides", "-n", "0", "-v", "-u", "g")
	add("remove-nested", "-n", "0", "-v", "-u", "sub/deep")
	add("remove-unlink", "-n", "0", "-v", "--remove=unlink", "f")
	add("remove-wipe", "-n", "0", "-v", "--remove=wipe", "f")
	add("remove-wipesync", "-n", "0", "-v", "--remove=wipesync", "f")
	add("remove-bare", "-n", "0", "-v", "--remove", "f")
	add("remove-abbreviated-how", "-n", "0", "-v", "--remove=wipes", "f")
	add("remove-two-operands", "-n", "0", "-v", "-u", "f", "g")
	add("remove-quiet", "-n", "0", "-u", "f")

	cases = append(cases,
		invocation{name: "dash-pipe", args: []string{"-n", "1", "-v", "-"}, seedTree: shredSeed},
		invocation{name: "dash-append", args: []string{"--random-source=src", "-n", "1", "-v", "-"},
			seedTree: shredSeed, stdoutPath: "f"},
		invocation{name: "dash-regular", args: []string{"--random-source=src", "-n", "1", "-v", "-"},
			seedTree: shredSeed, stdoutFile: "f"},
		invocation{name: "dash-regular-exact", args: []string{"--random-source=src", "-n", "1", "-v", "-x", "-"},
			seedTree: shredSeed, stdoutFile: "f"},
		invocation{name: "dash-regular-two-passes", args: []string{"--random-source=src", "-n", "2", "-v", "-x", "-"},
			seedTree: shredSeed, stdoutFile: "f"},
		invocation{name: "dash-remove", args: []string{"--random-source=src", "-n", "1", "-v", "-u", "-"},
			seedTree: shredSeed, stdoutFile: "f"},
		invocation{name: "dash-zero-length", args: []string{"--random-source=src", "-n", "1", "-v", "-"},
			seedTree: shredSeed, stdoutFile: "empty"},
		invocation{name: "dash-zero-pass", args: []string{"-n", "0", "-z", "-v", "-"},
			seedTree: shredSeed, stdoutFile: "f"},
		invocation{name: "dash-and-a-name", args: []string{"--random-source=src", "-n", "1", "-v", "g", "-"},
			seedTree: shredSeed, stdoutFile: "f"},
	)

	addType := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, seedTree: shredTypesSeed})
	}
	addType("type-fifo", "--random-source=f", "-n", "1", "-v", "p")
	addType("type-fifo-quiet", "--random-source=f", "-n", "1", "p")
	addType("type-fifo-size", "--random-source=f", "-n", "1", "-v", "-s", "4", "p")
	addType("type-fifo-remove", "-n", "0", "-v", "-u", "p")
	addType("type-fifo-zero-pass", "-n", "0", "-z", "-v", "p")
	addType("type-fifo-then-file", "--random-source=f", "-n", "0", "-z", "-v", "p", "f")
	addType("type-socket", "-n", "0", "-z", "-v", "sock")
	addType("type-socket-force", "-n", "0", "-z", "-v", "-f", "sock")
	add("type-terminal", "-n", "1", "-v", "/dev/ptmx")
	add("type-terminal-size", "-n", "1", "-v", "-s", "64", "/dev/ptmx")
	add("type-terminal-exact", "-n", "1", "-v", "-x", "/dev/ptmx")
	add("type-terminal-zero-pass", "-n", "0", "-z", "-v", "/dev/ptmx")
	add("type-terminal-quiet", "-n", "1", "/dev/ptmx")
	add("type-terminal-then-file", "--random-source=src", "-n", "1", "-v", "/dev/ptmx", "f")

	add("device-null-size", "-n", "1", "-v", "-s", "1024", "/dev/null")
	add("device-null-two-passes", "-n", "2", "-v", "-s", "512", "/dev/null")
	add("device-null-zero-pass", "-n", "0", "-z", "-v", "-s", "512", "/dev/null")

	add("force-directory", "-n", "1", "-v", "-f", "d")
	add("force-directory-zero-pass", "-n", "0", "-z", "-f", "d")
	add("force-missing", "-n", "1", "-v", "-f", "nosuch")
	add("force-nested-missing", "-n", "1", "-v", "-f", "sub/nosuch")

	add("missing-operand")
	add("no-such-file", "nosuch")
	add("no-such-file-verbose", "-v", "-n", "1", "nosuch")
	add("directory", "-n", "0", "d")
	add("directory-verbose", "-v", "-n", "0", "-z", "d")
	add("one-bad-one-good", "--random-source=src", "-n", "1", "-v", "nosuch", "f")
	add("bad-passes", "-n", "bogus", "f")
	add("bad-passes-negative", "-n", "-1", "f")
	add("bad-passes-multiplier", "-n", "1x2", "f")
	add("bad-size", "-s", "bogus", "f")
	add("bad-size-negative", "-s", "-5", "f")
	add("bad-size-multiplier", "-s", "1x2", "f")
	add("bad-remove-how", "--remove=bogus", "-n", "0", "f")
	add("bad-random-source", "--random-source=nosuch", "-n", "1", "f")
	add("bad-option", "-Q", "f")
	add("unknown-long-option", "--bogus", "f")
	add("ambiguous-long-option", "--r", "f")

	add("clustered", "--random-source=src", "-vxn1", "f")
	add("dash-dash-then-name", "--random-source=src", "-n", "1", "-v", "--", "f")
	add("operand-then-option", "--random-source=src", "f", "-n", "1", "-v")
	cases = append(cases, invocation{
		name:     "posix stops at the operand",
		args:     []string{"--random-source=src", "f", "-n", "1", "-v"},
		env:      []string{"POSIXLY_CORRECT=1"},
		seedTree: shredSeed,
	})
	add("long-iterations", "--random-source=src", "--iterations=1", "--verbose", "f")
	add("long-size", "--random-source=src", "--iterations=1", "--size=4", "--verbose", "f")
	add("long-zero-exact", "--zero", "--exact", "--iterations=0", "--verbose", "f")

	return cases
}

func TestShred(t *testing.T) {
	requireParity(t, "shred", shredCases(t))
}

func TestShredHelp(t *testing.T) {
	requireHelp(t, "shred", []string{"--help"}, 0)
	requireHelp(t, "shred", []string{"--hel"}, 0)
}

func TestShredVersion(t *testing.T) {
	requireVersion(t, "shred", []string{"--version"}, 0)
}
