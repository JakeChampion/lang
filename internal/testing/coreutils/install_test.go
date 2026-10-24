package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	raw := strings.Repeat(string(allBytes), 1025) + "\xff\xc0\x80"
	for _, c := range []struct {
		name, destination string
	}{
		{"compare-raw-equal", raw},
		{"compare-raw-late-difference", raw[:131073] + "x" + raw[131074:]},
		{"compare-raw-short-tail", raw[:len(raw)-1]},
	} {
		out = append(out, invocation{
			name: c.name, args: []string{"-v", "-C", "src", "dest"},
			seedTree: func(t *testing.T, dir string) {
				seedWrite(t, dir, "src", raw)
				seedWrite(t, dir, "dest", c.destination)
				if err := os.Chmod(filepath.Join(dir, "dest"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		})
	}

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
