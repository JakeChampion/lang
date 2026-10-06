package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

// sync(1) prints nothing on success, so the corpus is its diagnostics
// and its exit status: which of the three modes each operand kind
// accepts, and the three errors an operand can raise — it cannot be
// opened, it cannot be flushed, or it cannot be closed. The mode
// matters per kind: a FIFO and a character device refuse fsync and
// fdatasync with `Invalid argument` and accept -f, which flushes the
// file system they sit on rather than the file. A FIFO with no peer is
// opened non-blocking, as GNU opens it, and its cases pin what the
// kernel then says of the descriptor.

func init() {
	registerCorpus("sync", syncCases)
}

// syncTree holds one of every operand kind: a file, a directory, a
// symlink to each, a dangling symlink, a FIFO, and a nested file.
func syncTree(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("data\n"), 0o644); err != nil {
		t.Fatalf("write f: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatalf("mkdir d: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d", "inner"), []byte("inner\n"), 0o644); err != nil {
		t.Fatalf("write d/inner: %v", err)
	}
	if err := os.Symlink("f", filepath.Join(dir, "lf")); err != nil {
		t.Fatalf("symlink lf: %v", err)
	}
	if err := os.Symlink("d", filepath.Join(dir, "ld")); err != nil {
		t.Fatalf("symlink ld: %v", err)
	}
	if err := os.Symlink("nowhere", filepath.Join(dir, "dangling")); err != nil {
		t.Fatalf("symlink dangling: %v", err)
	}
	seedFifo(t, filepath.Join(dir, "p"))
}

func syncCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(inv invocation) { cases = append(cases, inv) }

	// ---- the no-operand forms ----
	add(invocation{name: "no operands"})
	add(invocation{name: "dashdash alone", args: []string{"--"}})
	add(invocation{name: "file-system with no operands", args: []string{"-f"}})
	add(invocation{name: "the long file-system with no operands", args: []string{"--file-system"}})
	add(invocation{name: "data needs an argument", args: []string{"-d"}})
	add(invocation{name: "the long data needs an argument", args: []string{"--data"}})
	// Both flags are refused before the operand count is looked at.
	add(invocation{name: "data and file-system", args: []string{"-d", "-f", "f"}, seedTree: syncTree})
	add(invocation{name: "data and file-system clustered", args: []string{"-df"}})
	add(invocation{name: "file-system then data", args: []string{"-f", "-d"}})
	add(invocation{name: "data and file-system with no operands", args: []string{"--data", "--file-system"}})
	add(invocation{name: "help with a value", args: []string{"--help=x"}})
	add(invocation{name: "version with a value", args: []string{"--version=1"}})

	// ---- getopt faults ----
	add(invocation{name: "invalid short option", args: []string{"-x"}})
	add(invocation{name: "invalid byte in a cluster", args: []string{"-dx", "f"}})
	add(invocation{name: "unrecognized long option", args: []string{"--foo"}})
	add(invocation{name: "data refuses a value", args: []string{"--data=1", "f"}})
	add(invocation{name: "file-system refuses a value", args: []string{"--file-system=1", "f"}})
	// The ambiguity list is in declaration order: data, file-system,
	// help, version.
	add(invocation{name: "the empty long option", args: []string{"--=x"}})
	add(invocation{name: "the empty long option with an operand", args: []string{"--=x", "f"}, seedTree: syncTree})
	add(invocation{name: "file-system by prefix", args: []string{"--fi", "f"}, seedTree: syncTree})
	add(invocation{name: "file-system by one letter", args: []string{"--f", "f"}, seedTree: syncTree})
	add(invocation{name: "data by one letter", args: []string{"--d", "f"}, seedTree: syncTree})
	add(invocation{name: "an option after an operand permutes", args: []string{"f", "-x"}, seedTree: syncTree})
	add(invocation{name: "POSIXLY_CORRECT stops at the first operand", args: []string{"f", "-x"}, env: []string{"POSIXLY_CORRECT=1"}, seedTree: syncTree})
	add(invocation{name: "getopt runs before the operands", args: []string{"missing", "-x"}})

	// ---- each operand kind under each mode ----
	for _, mode := range [][]string{nil, {"-d"}, {"-f"}, {"--data"}, {"--file-system"}} {
		label := "default"
		if mode != nil {
			label = mode[0]
		}
		for _, op := range []string{"f", "d", "d/inner", "lf", "ld", "dangling", "p", "missing", "d/", "f/", "lf/", ".", ""} {
			add(invocation{name: label + " on " + op, args: append(append([]string{}, mode...), op), seedTree: syncTree})
		}
		add(invocation{name: label + " on /dev/null", args: append(append([]string{}, mode...), "/dev/null")})
		add(invocation{name: label + " on the operand tree", args: append(append([]string{}, mode...), "f", "d", "p", "missing", "d/inner"), seedTree: syncTree})
	}
	add(invocation{name: "a lone dash is a name", args: []string{"-"}})
	add(invocation{name: "dashdash then an option-shaped name", args: []string{"--", "-d"}})
	add(invocation{name: "dashdash then dashdash", args: []string{"--", "--"}})
	add(invocation{name: "the same name twice", args: []string{"f", "f"}, seedTree: syncTree})
	add(invocation{name: "a name that is not valid UTF-8", args: []string{"--", "\xff\xfe"}})
	add(invocation{name: "a missing directory component", args: []string{"nodir/f"}})
	for _, n := range []string{"a b", "a'b", "a\"b", "a\\b", "a\tb", "a\nb"} {
		add(invocation{name: "missing " + n, args: []string{"--", n}})
	}
	// A failed operand costs the status and not the run: the second
	// diagnostic still appears.
	add(invocation{name: "two failures", args: []string{"missing", "p"}, seedTree: syncTree})
	add(invocation{name: "a failure between two successes", args: []string{"f", "missing", "d"}, seedTree: syncTree})

	// ---- the write-failure paths ----
	// Nothing is written to stdout, so a stdout that cannot be written is
	// not noticed at all.
	add(invocation{name: "a closed stdout", args: []string{"f"}, seedTree: syncTree, stdout: stdoutClosed})
	add(invocation{name: "a closed stdout with a diagnostic", args: []string{"missing"}, stdout: stdoutClosed})
	add(invocation{name: "a full stdout", args: []string{"f"}, seedTree: syncTree, stdout: stdoutFull})
	add(invocation{name: "a full stdout with no operands", stdout: stdoutFull})

	return cases
}

func TestSyncParity(t *testing.T) {
	requireParity(t, "sync", syncCases(t))
}

func TestSyncHelpVersion(t *testing.T) {
	requireHelp(t, "sync", []string{"--help"}, 0)
	requireHelp(t, "sync", []string{"--hel"}, 0)
	requireHelp(t, "sync", []string{"--help", "extra"}, 0)
	requireHelp(t, "sync", []string{"f", "--help"}, 0)
	requireVersion(t, "sync", []string{"--version"}, 0)
	requireVersion(t, "sync", []string{"--vers"}, 0)
	requireVersion(t, "sync", []string{"--v"}, 0)
	requireVersion(t, "sync", []string{"--version", "f"}, 0)
}
