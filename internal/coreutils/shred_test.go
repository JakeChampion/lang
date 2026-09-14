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

// shredSeed is the tree every case works over: a file to overwrite, a
// second one for the multi-operand cases, a directory, a name that the
// `-u` rename chain will collide with, a nested operand, and `src` —
// 20,000 bytes of a fixed byte, which is what makes a RANDOM pass
// comparable at all (see below).
// varied returns n deterministic bytes that are all over the place, so a
// file written from them differs from one written from any other span of
// them.
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
	// The first candidate the rename chain tries for `f` is `0`, and for
	// a two-character name `00`; both are taken here so the step to the
	// next candidate is exercised.
	write("0", "taken")
	write("00", "taken")
	write("src", strings.Repeat("Q", 20000))
	// A source whose bytes all DIFFER, which is what makes the ORDER the
	// bytes are consumed in observable: `src` compares equal however
	// many bytes a run took from it or in what order, so only this one
	// gates the double sweep a growing file gets. The generator is a
	// plain LCG so the tree is the same on every run.
	write("vsrc", varied(40000))
	// The lengths the sweep rule turns on: under one block, exactly one
	// block, and over one block.
	write("one", "x")
	write("small", varied(100))
	write("blk", varied(4096))
	write("over", varied(5000))
	// A source too short to finish, for the `end of file` refusal.
	write("dry", varied(50))
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("sub", "deep"), "nested")
	// A file whose mode refuses a write, for `-f`. The suite usually runs
	// as root, where the open succeeds and `-f` never reaches its chmod,
	// so these cases prove the option is accepted and inert rather than
	// that the retry works; the retry is measured by hand under
	// setpriv, and `docs/COREUTILS.md` says so.
	write("ro", "readonly")
	if err := os.Chmod(filepath.Join(dir, "ro"), 0o444); err != nil {
		t.Fatal(err)
	}
}

// shredTypesSeed is the tree for the operands shred refuses for WHAT
// THEY ARE rather than for what they contain. Both need making here
// because a seedTree is the only thing in the harness that can put a
// non-regular entry in front of a utility.
//
// The FIFO is held open by a reader of our own for the length of the
// run: a writer's open BLOCKS until a reader arrives, so without it both
// implementations would wait forever instead of answering. `O_NONBLOCK`
// is what lets this side open it without the same wait.
func shredTypesSeed(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, "p")
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	fd, err := syscall.Open(p, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open fifo for reading: %v", err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	ln, err := net.Listen("unix", filepath.Join(dir, "sock"))
	if err != nil {
		t.Fatalf("unix socket: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// shredCases is shred(1)'s corpus.
//
// Two things bound what can be compared here, and both are about the
// REFERENCE rather than about this implementation.
//
// The pass schedule is RANDOM in GNU. Three runs of `-n 10` over
// identical files give three different pattern orders and three
// different pattern SETS, so `-v` output past the all-random range
// cannot be compared against anything, GNU included. Every case here
// therefore stays at `-n 3` or below, where every pass is `random` and
// the lines are fixed, or at `-n 0`, where there are none.
//
// The BYTES a random pass writes are unrepeatable for the same reason —
// except through `--random-source`, which replaces the CSPRNG with a
// file. Pointed at a seeded `src`, a random pass writes that file's
// bytes, so the whole content of a shredded file compares too, which is
// what carries the write path: the block loop, the rewind between
// passes, and the rounding up to the block size.
//
// Nothing about the operand itself is out of reach any more: the three
// types GNU refuses are all reachable here, the terminal through
// /dev/ptmx. What no case can compare is a write that FAILS — it needs a
// full filesystem or a device, and the harness mounts nothing — so
// GNU's `error writing at offset N` line is measured by hand
// (docs/COREUTILS.md, #9231).
func shredCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, seedTree: shredSeed})
	}

	// --- the deterministic content: a seeded random source ----------------
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

	// --- the sweep a growing file gets, over a source of varied bytes -----
	//
	// A file SHORTER than one block is overwritten at its own length
	// first, once per pass and silently, and only then at the rounded-up
	// length where the pass lines are. Every one of those writes takes
	// its own bytes from the source, so `vsrc` is what proves the sweep
	// happened at all: with `src`'s single repeated byte the content
	// comes out the same either way.
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

	// --- a source that runs out --------------------------------------------
	//
	// It ends the RUN, not the operand: what the failing write would have
	// carried is not written, the operands after it are left alone, and
	// the exit status is 1. Where the refusal lands relative to the pass
	// line follows from the sweep: `one` gets its silent 1-byte write
	// first and fails on the 4096 that follows, so the line is there,
	// while `small` cannot fill even the silent write and so has none.
	add("dry-source-small", "--random-source=dry", "-n", "1", "-v", "small")
	add("dry-source-one", "--random-source=dry", "-n", "1", "-v", "one")
	add("dry-source-quiet", "--random-source=dry", "-n", "1", "one")
	add("dry-source-empty", "--random-source=empty", "-n", "1", "-v", "f")
	add("dry-source-second-operand", "--random-source=dry", "-n", "1", "-v", "-x", "-s", "30", "f", "g")
	add("dry-source-removal-refused", "--random-source=empty", "-n", "1", "-v", "-u", "f")
	// A zero pass takes nothing from the source, so an empty one is fine.
	add("dry-source-zero-pass-only", "--random-source=empty", "-n", "0", "-z", "-v", "f")

	// --- the zero pass, which needs no source -----------------------------
	add("zero-only", "-n", "0", "-z", "-v", "f")
	add("zero-only-exact", "-n", "0", "-z", "-v", "-x", "f")
	add("zero-only-two", "-n", "0", "-z", "f", "g")
	add("zero-only-quiet", "-n", "0", "-z", "f")

	// --- removal, whose whole output is determined by the name ------------
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

	// --- `-`, whose answer depends on how fd 1 was opened -----------------
	//
	// GNU has three, and each is reachable from the harness: a PIPE (what
	// a captured stdout is) is `invalid file type`, an APPEND-only
	// descriptor is `cannot shred append-only file descriptor`, and a
	// plain writable file is overwritten in place — with `-v` naming the
	// operand `-`, the length rounded up to the block as any other
	// operand's is, and `-u` standing in for removal by truncating what
	// it cannot unlink. #9219 is the primitive that makes the three
	// distinguishable at all.
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

	// --- what an operand IS, which is decided before anything is written --
	//
	// A FIFO and a socket have no length to rewind over, so both are
	// refused: the FIFO once it is OPEN, with `invalid file type`, and
	// the socket by the open itself, which answers ENXIO before the
	// question is reached. A character device is not refused — a device
	// is what shred was written for — and /dev/null is the one every
	// machine has. It also cannot be SYNCED, which is not an error
	// either: `fdatasync` on it answers EINVAL, and the run carries on.
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
	// A TERMINAL is the third refusal, and the one a mode cannot
	// answer: /dev/null and /dev/ptmx are both character devices and
	// only one of them is written. /dev/ptmx is the terminal every
	// machine has — opening it allocates a pty master — so it needs no
	// seeding, and `w.isatty()` (#9229) is what tells it from /dev/null
	// below. `-f` is deliberately absent: a chmod of a system device is
	// not something a corpus should be able to attempt.
	add("type-terminal", "-n", "1", "-v", "/dev/ptmx")
	add("type-terminal-size", "-n", "1", "-v", "-s", "64", "/dev/ptmx")
	add("type-terminal-exact", "-n", "1", "-v", "-x", "/dev/ptmx")
	add("type-terminal-zero-pass", "-n", "0", "-z", "-v", "/dev/ptmx")
	add("type-terminal-quiet", "-n", "1", "/dev/ptmx")
	add("type-terminal-then-file", "--random-source=src", "-n", "1", "-v", "/dev/ptmx", "f")

	// Every device case gives a SIZE. Without one the length comes from
	// `lseek(0, SEEK_END)`, which a character device answers 0 for, and
	// both implementations then write until a write FAILS — on
	// /dev/null, never. A case that does not terminate is not a case;
	// the length a real device DOES report is measured by hand over a
	// loop device, and docs/COREUTILS.md carries the commands.
	add("device-null-size", "-n", "1", "-v", "-s", "1024", "/dev/null")
	add("device-null-two-passes", "-n", "2", "-v", "-s", "512", "/dev/null")
	add("device-null-zero-pass", "-n", "0", "-z", "-v", "-s", "512", "/dev/null")

	// --- `-f`, which is an open RETRY and not an eager chmod --------------
	//
	// GNU makes the entry writable only after the open was refused for
	// the one reason a mode can fix, so an operand that could never be
	// opened for writing is left exactly as it was found — the tree
	// comparison is what gates that, since the diagnostic alone cannot
	// tell a chmodded directory from an untouched one.
	add("force-directory", "-n", "1", "-v", "-f", "d")
	add("force-directory-zero-pass", "-n", "0", "-z", "-f", "d")
	add("force-missing", "-n", "1", "-v", "-f", "nosuch")
	add("force-nested-missing", "-n", "1", "-v", "-f", "sub/nosuch")

	// --- the diagnostics ---------------------------------------------------
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

	// --- the option scan ---------------------------------------------------
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
