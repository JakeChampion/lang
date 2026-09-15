package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// install(1) is the copy engine's third caller, after `cp` and `mv`'s
// cross-device fallback, so most of what it does is already gated by
// their corpora. What this one is about is the part that is install's
// alone, and every item below is a behaviour the documentation does not
// give and a differential against the reference binary does.
//
//   - THE UMASK IS NOT CONSULTED. The default mode is 0755 under `umask
//     077`, and `-m u=rw,g=r` lands 0640. `cp` would mislead you here:
//     its three mode states all filter through the mask or copy the
//     source's bits, and install's does neither.
//   - `-m`'s symbolic form resolves against a base of ZERO, not against
//     the 0755 default. `-m +x` is 0111, `-m go-w` is 0, `-m o=` is 0.
//     0755 is what install uses when `-m` is ABSENT; it is not something
//     `-m` edits. Even the bare `+w`, which POSIX lets consult the
//     umask, is 0222 under every mask.
//   - `-m` with `-d` reaches only the LAST component: `-d -m 700 a/b/c`
//     leaves `a` and `a/b` at 0755.
//   - a directory's line is `install: creating directory 'X'`, on STDOUT
//     and carrying the program prefix, where the file line has none.
//   - `-C` on a match does NOTHING, mtime included; on a mismatch it
//     prints `removed 'X'` and copies.
//   - a failed `-s` prints the `-v` line TWICE and leaves no destination.
//     Both are consequences of how GNU does it rather than choices: the
//     `cannot run` message comes from the forked CHILD, and both
//     processes carry the same unflushed stdout across the fork, so each
//     one's flush emits its own copy of the line. The destination is
//     made and then taken away again.
//
// `-o` / `-g` run as root here, where every chown succeeds, so those
// rows set `ownership` to put uid/gid into the tree comparison. Without
// it an install that parsed the option and called nothing would pass.
//
// `install: cannot change permissions of 'X'` is in the issue's quirk
// list and is NOT covered: it needs a destination whose mode cannot be
// set, which as root wants the same unreachable setup `cp -f`'s retry
// did. The code path is there; the case that would prove it is not
// something this harness can rely on.

// installBasic is the ordinary fixture: a source with a mode the umask
// would not have produced, a directory to install into, an existing
// destination to replace, a symbolic link to be dereferenced, and a
// leading path that already exists so `-D` can be seen not announcing
// it.
func installBasic(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "src", "SRC\n")
	if err := os.Chmod(filepath.Join(dir, "src"), 0o741); err != nil {
		t.Fatalf("chmod src: %v", err)
	}
	seedWrite(t, dir, "other", "OTHER\n")
	seedMkdir(t, dir, "dir")
	seedWrite(t, dir, "tgt", "OLD\n")
	seedSymlink(t, dir, "src", "sl")
	seedMkdir(t, dir, "have/x")
	seedMkdir(t, dir, "empty")
}

// installSame is the `-C` fixture: a destination that already holds
// exactly what installing would put there, and two that differ in one
// way each — the content, and the mode.
func installSame(t *testing.T, dir string) {
	t.Helper()
	seedWrite(t, dir, "src", "SAME\n")
	seedWrite(t, dir, "match", "SAME\n")
	if err := os.Chmod(filepath.Join(dir, "match"), 0o755); err != nil {
		t.Fatalf("chmod match: %v", err)
	}
	seedWrite(t, dir, "content", "OTHER\n")
	if err := os.Chmod(filepath.Join(dir, "content"), 0o755); err != nil {
		t.Fatalf("chmod content: %v", err)
	}
	seedWrite(t, dir, "mode", "SAME\n")
	if err := os.Chmod(filepath.Join(dir, "mode"), 0o700); err != nil {
		t.Fatalf("chmod mode: %v", err)
	}
}

func init() {
	registerCorpus("install", installCases)
}

func installCases(t *testing.T) []invocation {
	t.Helper()
	var out []invocation

	// The operand forms, and the shapes that refuse.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"no-operands", nil},
		{"one-operand", []string{"src"}},
		{"dashdash-alone", []string{"--"}},
		{"plain", []string{"src", "out"}},
		{"verbose", []string{"-v", "src", "out"}},
		{"over-existing", []string{"-v", "src", "tgt"}},
		{"into-directory", []string{"-v", "src", "dir"}},
		{"two-into-directory", []string{"-v", "src", "other", "dir"}},
		{"target-not-a-directory", []string{"-v", "src", "other", "tgt"}},
		{"no-target-directory", []string{"-v", "-T", "src", "out"}},
		{"no-target-directory-onto-dir", []string{"-v", "-T", "src", "dir"}},
		{"no-target-extra-operand", []string{"-T", "src", "out", "extra"}},
		{"target-directory", []string{"-v", "-t", "dir", "src"}},
		{"target-directory-two", []string{"-v", "-t", "dir", "src", "other"}},
		{"target-directory-missing-source", []string{"-v", "-t", "dir"}},
		{"target-directory-not-a-directory", []string{"-t", "src", "other"}},
		{"source-missing", []string{"nosuch", "out"}},
		{"source-is-a-directory", []string{"-v", "dir", "out"}},
		{"source-is-a-symlink", []string{"-v", "sl", "out"}},
		{"dash-is-not-stdin", []string{"-v", "-", "out"}},
		{"destination-parent-missing", []string{"src", "nosuchdir/out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: installBasic})
	}

	// The mode rules, which are the reason this corpus exists.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"mode-default", []string{"-v", "src", "out"}},
		{"mode-octal", []string{"-v", "-m", "700", "src", "out"}},
		{"mode-octal-four-digit", []string{"-v", "-m", "0700", "src", "out"}},
		{"mode-setgid", []string{"-v", "-m", "2755", "src", "out"}},
		{"mode-setuid", []string{"-v", "-m", "u+s", "src", "out"}},
		{"mode-symbolic-equals", []string{"-v", "-m", "u=rw,g=r", "src", "out"}},
		{"mode-symbolic-plus-x", []string{"-v", "-m", "+x", "src", "out"}},
		{"mode-symbolic-all-plus-x", []string{"-v", "-m", "a+x", "src", "out"}},
		{"mode-symbolic-user-plus-w", []string{"-v", "-m", "u+w", "src", "out"}},
		{"mode-symbolic-bare-plus-w", []string{"-v", "-m", "+w", "src", "out"}},
		{"mode-symbolic-minus", []string{"-v", "-m", "go-w", "src", "out"}},
		{"mode-symbolic-empty-other", []string{"-v", "-m", "o=", "src", "out"}},
		{"mode-symbolic-bare-equals", []string{"-v", "-m", "=rw", "src", "out"}},
		{"mode-symbolic-all-read", []string{"-v", "-m", "a=r", "src", "out"}},
		{"mode-invalid", []string{"-m", "bogus", "src", "out"}},
		{"mode-invalid-octal", []string{"-m", "999999", "src", "out"}},
		{"mode-long", []string{"-v", "--mode=640", "src", "out"}},
		{"mode-over-existing", []string{"-v", "-m", "600", "src", "tgt"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: installBasic})
	}

	// The same rules under a umask that would change the answer if one
	// were applied. Every row here is the point of the fixture.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"umask-default", []string{"-v", "src", "out"}},
		{"umask-mode-symbolic", []string{"-v", "-m", "u=rw,g=r", "src", "out"}},
		{"umask-mode-bare-plus-w", []string{"-v", "-m", "+w", "src", "out"}},
		{"umask-directory", []string{"-v", "-d", "a/b/c"}},
		{"umask-directory-mode", []string{"-v", "-d", "-m", "700", "a/b/c"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: installBasic, umask: withMask(0o077)})
	}

	// -d and -D.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"directory-one", []string{"-v", "-d", "made"}},
		{"directory-nested", []string{"-v", "-d", "a/b/c"}},
		{"directory-two-operands", []string{"-v", "-d", "one", "two/deep"}},
		{"directory-existing", []string{"-v", "-d", "empty"}},
		{"directory-existing-nested", []string{"-v", "-d", "have/x"}},
		{"directory-partly-existing", []string{"-v", "-d", "have/x/new"}},
		{"directory-over-a-file", []string{"-v", "-d", "src"}},
		{"directory-mode-last-only", []string{"-v", "-d", "-m", "700", "a/b/c"}},
		{"directory-mode-symbolic", []string{"-v", "-d", "-m", "u=rwx", "a/b/c"}},
		{"directory-trailing-slash", []string{"-v", "-d", "a/b/"}},
		{"directory-doubled-slash", []string{"-v", "-d", "a//b"}},
		{"directory-dot-component", []string{"-v", "-d", "a/./b"}},
		{"leading-created", []string{"-v", "-D", "src", "lead/x/y/out"}},
		{"leading-existing", []string{"-v", "-D", "src", "have/x/out"}},
		{"leading-mode-is-the-file's", []string{"-v", "-D", "-m", "700", "src", "lead/x/out"}},
		{"leading-no-directory-needed", []string{"-v", "-D", "src", "out"}},
		{"leading-with-target-directory", []string{"-v", "-D", "-t", "lead/x", "src"}},
		{"leading-with-no-target-directory", []string{"-v", "-D", "-T", "src", "lead/x/out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: installBasic})
	}

	// -C, against a destination that matches and two that do not.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"compare-match", []string{"-v", "-C", "src", "match"}},
		{"compare-match-quiet", []string{"-C", "src", "match"}},
		{"compare-content-differs", []string{"-v", "-C", "src", "content"}},
		{"compare-mode-differs", []string{"-v", "-C", "src", "mode"}},
		{"compare-new-destination", []string{"-v", "-C", "src", "fresh"}},
		{"compare-long", []string{"-v", "--compare", "src", "match"}},
		{"compare-with-mode", []string{"-v", "-C", "-m", "700", "src", "match"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: installSame})
	}

	// -s, on a strip program that is deterministic because it does
	// nothing. A case comparing the bytes a real `strip` leaves would be
	// pinned to this container's binutils instead of to install.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"strip-noop-program", []string{"-v", "-s", "--strip-program=/bin/true", "src", "out"}},
		{"strip-noop-quiet", []string{"-s", "--strip-program=/bin/true", "src", "out"}},
		{"strip-missing-program", []string{"-v", "-s", "--strip-program=/nosuch/prog", "src", "out"}},
		{"strip-missing-program-quiet", []string{"-s", "--strip-program=/nosuch/prog", "src", "out"}},
		{"strip-missing-program-into-dir", []string{"-v", "-s", "--strip-program=/nosuch/prog", "src", "dir"}},
		{"strip-program-without-s", []string{"-v", "--strip-program=/nosuch/prog", "src", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: installBasic})
	}

	// Ownership. Root makes every chown succeed, so the tree comparison
	// has to read the ids for these to prove anything.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"owner-root", []string{"-v", "-o", "root", "src", "out"}},
		{"owner-numeric", []string{"-v", "-o", "0", "src", "out"}},
		{"group-root", []string{"-v", "-g", "root", "src", "out"}},
		{"group-numeric", []string{"-v", "-g", "0", "src", "out"}},
		{"owner-and-group", []string{"-v", "-o", "0", "-g", "0", "src", "out"}},
		{"owner-invalid", []string{"-o", "nosuchuser", "src", "out"}},
		{"group-invalid", []string{"-g", "nosuchgroup", "src", "out"}},
		{"owner-on-a-directory", []string{"-v", "-d", "-o", "0", "made"}},
		{"owner-on-nested-directory", []string{"-v", "-d", "-o", "0", "made/inner"}},
		{"owner-mode-file", []string{"-v", "-o", "0", "-m", "4755", "src", "out"}},
		{"owner-mode-directory", []string{"-v", "-d", "-o", "0", "-m", "750", "made"}},
		{"owner-with-leading-directories", []string{"-v", "-D", "-o", "0", "src", "made/inner/out"}},
		{"owner-before-strip-failure", []string{"-v", "-s", "--strip-program=/bin/false", "-o", "0", "src", "out"}},
		{"owner-long", []string{"-v", "--owner=0", "src", "out"}},
		{"group-long", []string{"-v", "--group=0", "src", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: installBasic, ownership: true})
	}

	// Backups, timestamps and the ignored flag.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"backup-simple", []string{"-bv", "src", "tgt"}},
		{"backup-long", []string{"-v", "--backup", "src", "tgt"}},
		{"backup-numbered", []string{"-v", "--backup=numbered", "src", "tgt"}},
		{"backup-none", []string{"-v", "--backup=none", "src", "tgt"}},
		{"backup-suffix", []string{"-bv", "-S", ".bak", "src", "tgt"}},
		{"backup-suffix-long", []string{"-bv", "--suffix=.bak", "src", "tgt"}},
		{"backup-new-destination", []string{"-bv", "src", "out"}},
		{"backup-invalid-control", []string{"--backup=bogus", "src", "tgt"}},
		{"preserve-timestamps", []string{"-pv", "src", "out"}},
		{"preserve-timestamps-long", []string{"-v", "--preserve-timestamps", "src", "out"}},
		{"ignored-c", []string{"-cv", "src", "out"}},
		{"context-short", []string{"-Zv", "src", "out"}},
		{"context-long", []string{"-v", "--context", "src", "out"}},
		{"context-valued", []string{"-v", "--context=x", "src", "out"}},
		{"preserve-context", []string{"-v", "--preserve-context", "src", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: installBasic})
	}

	// The environment the backup control reads.
	for _, c := range []struct {
		name string
		args []string
		env  []string
	}{
		{"version-control-numbered", []string{"-bv", "src", "tgt"}, []string{"VERSION_CONTROL=numbered"}},
		{"version-control-none", []string{"-bv", "src", "tgt"}, []string{"VERSION_CONTROL=none"}},
		{"version-control-bogus", []string{"-bv", "src", "tgt"}, []string{"VERSION_CONTROL=bogus"}},
		{"simple-backup-suffix", []string{"-bv", "src", "tgt"}, []string{"SIMPLE_BACKUP_SUFFIX=.S"}},
		{"suffix-beats-environment", []string{"-bv", "-S", ".T", "src", "tgt"}, []string{"SIMPLE_BACKUP_SUFFIX=.S"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, env: c.env, seedTree: installBasic})
	}

	// The getopt surface: the ambiguity lists print their candidates in
	// the option table's declaration order, which is the one place that
	// order is observable.
	for _, pre := range []string{"--s", "--st", "--stri", "--p", "--pre", "--preserve", "--c", "--co", "--d", "--v", "--ve", "--b", "--n", "--a", "--z", "--Z"} {
		out = append(out, invocation{name: "prefix" + pre, args: []string{pre, "src", "out"}, seedTree: installBasic})
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"unknown-short", []string{"-q", "src", "out"}},
		{"unknown-long", []string{"--nosuch", "src", "out"}},
		{"mode-without-value", []string{"-m"}},
		{"suffix-without-value", []string{"-S"}},
		{"target-without-value", []string{"-t"}},
		{"target-and-no-target", []string{"-t", "dir", "-T", "src", "out"}},
		{"debug-takes-no-value", []string{"--debug=x", "src", "out"}},
		{"options-after-operands", []string{"src", "out", "-v"}},
		{"dashdash-then-operands", []string{"--", "src", "out"}},
	} {
		out = append(out, invocation{name: c.name, args: c.args, seedTree: installBasic})
	}

	return out
}

func TestInstallParity(t *testing.T) {
	requireParity(t, "install", installCases(t))
}

// TestInstallDebug holds `--debug` to its exit status and stream shape
// rather than its bytes, for the reason docs/COREUTILS.md records: GNU's
// second line names its own copy_file_range offload and SEEK_HOLE
// probing, and ours states what the copy actually did. What IS compared
// is that the option implies -v, that the report follows the verbose
// line, and that both go to stdout.
func TestInstallDebug(t *testing.T) {
	ours := fernBin(t, "install")
	dir := t.TempDir()
	seedWrite(t, dir, "src", "SRC\n")

	got := runCpIn(t, ours, dir, "--debug", "src", "out")
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("--debug printed %d lines, want the exit line plus two:\n%s", len(lines), got)
	}
	if lines[0] != "exit 0" {
		t.Errorf("--debug exited %q, want 0", lines[0])
	}
	if lines[1] != "'src' -> 'out'" {
		t.Errorf("--debug did not imply -v: first line is %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "copy offload: ") {
		t.Errorf("--debug's report is %q, want it to open with \"copy offload: \"", lines[2])
	}
}

func TestInstallHelpVersion(t *testing.T) {
	requireHelp(t, "install", []string{"--help"}, 0)
	requireHelp(t, "install", []string{"--hel"}, 0)
	requireHelp(t, "install", []string{"--help", "a", "b"}, 0)
	requireHelp(t, "install", []string{"a", "b", "--help"}, 0)
	requireHelp(t, "install", []string{"-d", "--help"}, 0)
	requireVersion(t, "install", []string{"--version"}, 0)
	requireVersion(t, "install", []string{"--vers"}, 0)
	requireVersion(t, "install", []string{"a", "b", "--version"}, 0)
	requireVersion(t, "install", []string{"-m", "700", "--version", "-v"}, 0)
}
