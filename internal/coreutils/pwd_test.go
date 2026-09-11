package coreutils

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pwd(1) has two answers and they differ only where a symbolic link was
// walked to get here, so the corpus runs in a directory reached THROUGH
// one: `-P` resolves it, `-L` keeps it when $PWD says so.
//
// $PWD is not trusted on sight. It has to be absolute, carry no `.` or
// `..` component, and name the same directory `.` does — dev and inode
// both — or the physical answer stands. Each of those three tests has
// its own case, and `/a/.hidden` is the near miss that must NOT be
// rejected: the rule is about components, not about a dot appearing.
//
// Operands are not a usage error here: they are ignored with a
// diagnostic on stderr and the directory is still printed, exit 0.

// pwdTree builds a directory reached two ways — physically at
// `<base>/real`, and through the symbolic link `<base>/link` — and
// returns the two paths. The link is what makes -L and -P differ; every
// case runs with the link path as its working directory.
func pwdTree(t *testing.T) (link, real string) {
	t.Helper()
	base := t.TempDir()
	// The temp directory itself may be reached through a link (macOS
	// puts /tmp behind one), and a case comparing $PWD against `.`
	// would then be asking about the wrong path. Resolve it once here
	// so `real` is genuinely physical.
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatalf("resolve %s: %v", base, err)
	}
	real = filepath.Join(resolved, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link = filepath.Join(resolved, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	return link, real
}

func init() {
	registerCorpus("pwd", pwdCases)
}

func pwdCases(t *testing.T) []invocation {
	link, real := pwdTree(t)
	at := func(inv invocation) invocation {
		inv.dir = link
		return inv
	}
	return []invocation{
		at(invocation{name: "no arguments is physical"}),
		at(invocation{name: "-P", args: []string{"-P"}}),
		at(invocation{name: "-L with a matching PWD", args: []string{"-L"}, env: []string{"PWD=" + link}}),
		at(invocation{name: "--physical", args: []string{"--physical"}}),
		at(invocation{name: "--logical", args: []string{"--logical"}, env: []string{"PWD=" + link}}),
		at(invocation{name: "--log", args: []string{"--log"}, env: []string{"PWD=" + link}}),
		at(invocation{name: "--phys", args: []string{"--phys"}, env: []string{"PWD=" + link}}),
		at(invocation{name: "-L then -P", args: []string{"-L", "-P"}, env: []string{"PWD=" + link}}),
		at(invocation{name: "-P then -L", args: []string{"-P", "-L"}, env: []string{"PWD=" + link}}),
		at(invocation{name: "-LP in one cluster", args: []string{"-LP"}, env: []string{"PWD=" + link}}),
		at(invocation{name: "-PL in one cluster", args: []string{"-PL"}, env: []string{"PWD=" + link}}),
		at(invocation{name: "dashdash alone", args: []string{"--"}}),

		// The physical path is the answer even under -L when $PWD is
		// not fit to print.
		at(invocation{name: "-L with PWD unset", args: []string{"-L"}}),
		at(invocation{name: "-L with an empty PWD", args: []string{"-L"}, env: []string{"PWD="}}),
		at(invocation{name: "-L with a relative PWD", args: []string{"-L"}, env: []string{"PWD=link"}}),
		at(invocation{name: "-L with a PWD naming somewhere else", args: []string{"-L"}, env: []string{"PWD=/"}}),
		at(invocation{name: "-L with a PWD that does not exist", args: []string{"-L"}, env: []string{"PWD=/nonesuch"}}),
		at(invocation{name: "-L with a dot component", args: []string{"-L"}, env: []string{"PWD=" + filepath.Dir(link) + "/./link"}}),
		at(invocation{name: "-L with a trailing dot", args: []string{"-L"}, env: []string{"PWD=" + link + "/."}}),
		at(invocation{name: "-L with a dotdot component", args: []string{"-L"}, env: []string{"PWD=" + filepath.Dir(link) + "/../" + filepath.Base(filepath.Dir(link)) + "/link"}}),
		at(invocation{name: "-L with a trailing dotdot", args: []string{"-L"}, env: []string{"PWD=" + link + "/.."}}),
		at(invocation{name: "-L with a PWD that is the physical path", args: []string{"-L"}, env: []string{"PWD=" + real}}),
		at(invocation{name: "-L with a trailing slash", args: []string{"-L"}, env: []string{"PWD=" + link + "/"}}),
		at(invocation{name: "-L with a doubled slash", args: []string{"-L"}, env: []string{"PWD=" + filepath.Dir(link) + "//link"}}),
		at(invocation{name: "-L with a PWD that is a file", args: []string{"-L"}, env: []string{"PWD=/etc/hostname"}}),
		// A dot INSIDE a component is not a dot component.
		{name: "-L with a dot inside a name", dir: real, args: []string{"-L"}, env: []string{"PWD=" + real}},

		// POSIXLY_CORRECT makes -L the default, and an explicit -P
		// still wins.
		at(invocation{name: "posixly correct is logical", env: []string{"POSIXLY_CORRECT=1", "PWD=" + link}}),
		at(invocation{name: "posixly correct with -P", args: []string{"-P"}, env: []string{"POSIXLY_CORRECT=1", "PWD=" + link}}),
		at(invocation{name: "posixly correct with an unusable PWD", env: []string{"POSIXLY_CORRECT=1", "PWD=/"}}),

		// A subdirectory, so the answer is not the tree root.
		{name: "a subdirectory", dir: filepath.Join(real, "sub")},
		{name: "root", dir: "/"},

		// Operands are ignored, with a diagnostic, and the directory
		// is still printed.
		at(invocation{name: "an operand", args: []string{"x"}}),
		at(invocation{name: "two operands", args: []string{"x", "y"}}),
		at(invocation{name: "lone dash is an operand", args: []string{"-"}}),
		at(invocation{name: "empty operand", args: []string{""}}),
		at(invocation{name: "operand that is not valid UTF-8", args: []string{"\xff\xfe"}}),
		at(invocation{name: "operand after dashdash", args: []string{"--", "x"}}),
		at(invocation{name: "operand before an option is permuted out", args: []string{"x", "-L"}, env: []string{"PWD=" + link}}),
		at(invocation{name: "posixly correct stops at an operand", args: []string{"x", "-L"}, env: []string{"POSIXLY_CORRECT=1", "PWD=" + link}}),

		// getopt faults, which ARE usage errors.
		at(invocation{name: "invalid short option", args: []string{"-x"}}),
		at(invocation{name: "invalid byte in a valid cluster", args: []string{"-Lx"}}),
		at(invocation{name: "unrecognized long option", args: []string{"--foo"}}),
		at(invocation{name: "empty long option is ambiguous", args: []string{"--=x"}}),
		at(invocation{name: "value on an option that takes none", args: []string{"--logical=1"}}),
		at(invocation{name: "help with a value", args: []string{"--help=x"}}),
		at(invocation{name: "version with a value", args: []string{"--version=1"}}),
		at(invocation{name: "bad option before help", args: []string{"--foo", "--help"}}),

		// The write-failure paths: one write, one strerror.
		at(invocation{name: "stdout closed", stdout: stdoutClosed}),
		at(invocation{name: "stdout full", stdout: stdoutFull}),
		at(invocation{name: "stdout closed with an ignored operand", args: []string{"x"}, stdout: stdoutClosed}),
	}
}

func TestPwdParity(t *testing.T) {
	requireParity(t, "pwd", pwdCases(t))
}

func TestPwdHelpVersion(t *testing.T) {
	requireHelp(t, "pwd", []string{"--help"}, 0)
	requireHelp(t, "pwd", []string{"--hel"}, 0)
	requireHelp(t, "pwd", []string{"--help", "x"}, 0)
	requireHelp(t, "pwd", []string{"x", "--help"}, 0)
	requireVersion(t, "pwd", []string{"--version"}, 0)
	requireVersion(t, "pwd", []string{"--vers"}, 0)
	requireVersion(t, "pwd", []string{"--version", "x"}, 0)
}

// The one path the corpus above cannot reach: getcwd(2) refusing to
// answer. An unlinked working directory is the way to make it, and
// exec cannot deliver it — a child whose working directory has already
// been removed never starts — so the removal happens in the child, just
// before it execs the utility.
//
// What both implementations do then is walk up from `.` reading `..`,
// and here that walk finds no entry for the directory that is gone. The
// diagnostic is the whole assertion: it says the walk ran, got one level
// up, and stopped where it should.
func TestPwdUnlinkedWorkingDirectory(t *testing.T) {
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash is what sets argv[0] for this case (exec -a): %v", err)
	}
	run := func(bin string) (string, string, int) {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "gone")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		// `exec -a pwd` keeps argv[0] the bare name on both sides,
		// which is what every diagnostic here is prefixed with.
		cmd := exec.Command(shell, "-c", `cd "$1" && rmdir "$1" && exec -a pwd "$2"`, "_", dir, bin)
		cmd.Env = baseEnv()
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		_ = cmd.Run()
		return out.String(), errb.String(), cmd.ProcessState.ExitCode()
	}
	wantOut, wantErr, wantExit := run(referenceBin(t, "pwd"))
	gotOut, gotErr, gotExit := run(fernBin(t, "pwd"))
	if !strings.Contains(wantErr, "matching i-node") {
		t.Fatalf("the reference did not reach its walk-up path; stderr was %q", wantErr)
	}
	if gotOut != wantOut || gotErr != wantErr || gotExit != wantExit {
		t.Errorf("unlinked working directory:\n fern: out=%q err=%q exit=%d\n  gnu: out=%q err=%q exit=%d",
			gotOut, gotErr, gotExit, wantOut, wantErr, wantExit)
	}
}
