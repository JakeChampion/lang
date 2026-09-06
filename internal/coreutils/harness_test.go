// Package coreutils is the differential gate for coreutils/*.fern: it
// runs each Fern utility and the GNU coreutils binary of the same name
// over the same invocation and requires the two to agree byte for byte
// on stdout, on stderr, and on how they exited.
//
// GNU is the ORACLE here, not a set of golden files. Expected output is
// never written down, so a case cannot record a wrong expectation, and
// adding a case costs one line. What that buys is only as good as the
// reference: `docs/COREUTILS.md` states which version the corpus is
// held to, the two outputs that are deliberately ours (`--help` and
// `--version` text), and the one gap that is open.
//
// Both sides run with argv[0] set to the bare utility name. GNU prints
// argv[0] verbatim in diagnostics and in the `Try '… --help'` line, so
// without that the two would differ by their install paths on every
// error case and prove nothing.
package coreutils

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// invocation is one differential case: an argv (argv[0] excluded), the
// stdin to feed it, and any environment on top of the fixed base.
type invocation struct {
	// name identifies the case in test output. Keep it short; the argv
	// is printed alongside it on failure.
	name string
	args []string
	// stdin is fed to both processes and closed.
	stdin string
	// env are KEY=VALUE entries added to the fixed base environment.
	env []string
	// limit bounds the stdout read for a utility that does not stop on
	// its own: the harness reads exactly this many bytes, closes the
	// read end, and then compares how each side reacted to the closed
	// pipe as well as the bytes it got. Zero reads to EOF.
	limit int
	// stdout is where the child's fd 1 goes; the default captures it.
	// The other two make the first write fail, which is how the
	// write-error paths (`prog: standard output: <strerror>`) are
	// reached, and both sides meet the same one.
	stdout stdoutMode
	// tty hands the child a pseudo-terminal master as fd 3. Fds 0-2
	// are pipes here, so this is the one descriptor on which `test -t`
	// can answer true.
	tty bool
}

type stdoutMode int

const (
	// stdoutCaptured is the default: a pipe the harness reads and compares.
	stdoutCaptured stdoutMode = iota
	// stdoutClosed leaves fd 1 unopened in the child, as `prog >&-`
	// does: the first write fails with EBADF.
	stdoutClosed
	// stdoutFull is /dev/full, as `prog > /dev/full` does: the first
	// write fails with ENOSPC. Linux only.
	stdoutFull
)

// outcome is everything observable about one run.
type outcome struct {
	stdout []byte
	stderr []byte
	// exit is the exit status, or -1 when a signal killed the process.
	exit int
	// signal is the signal name when one killed the process, else "".
	signal string
}

func (o outcome) how() string {
	if o.signal != "" {
		return "killed by " + o.signal
	}
	return fmt.Sprintf("exit %d", o.exit)
}

// The environment both sides run under. Parity is asserted in the C
// locale: GNU's diagnostics quote with U+2018/U+2019 in a UTF-8 locale
// and with ASCII apostrophes here, and its collation, case folding and
// number formatting are all locale-dependent too. One locale has to be
// named, and C is the one whose behaviour is fixed by POSIX.
func baseEnv() []string {
	return []string{
		"LC_ALL=C",
		"LANG=C",
		"TZ=UTC",
		"PATH=/usr/bin:/bin",
	}
}

var (
	gnuDirOnce sync.Once
	gnuDirPath string
	gnuDirVer  string
	gnuDirErr  error
)

// gnuDir returns the directory holding the GNU coreutils binaries the
// corpus is compared against, and the version they report.
//
// Missing reference binaries are a FAILURE, not a skip: a suite that
// quietly passes when it cannot find its oracle is the shape that lets
// a real divergence sit green for months. The message names every way
// to provide one.
func gnuDir(t *testing.T) (string, string) {
	t.Helper()
	gnuDirOnce.Do(func() {
		for _, dir := range gnuCandidates() {
			ver, err := gnuVersion(dir)
			if err != nil {
				continue
			}
			gnuDirPath, gnuDirVer = dir, ver
			return
		}
		gnuDirErr = errors.New("no GNU coreutils found")
	})
	if gnuDirErr != nil {
		t.Fatalf(`%v.

The coreutils parity gate compares each coreutils/*.fern utility against
the GNU binary of the same name, so it cannot run without one. Provide it
by any of:

  FERN_GNU_COREUTILS=/path/to/coreutils/bin go test ./internal/coreutils/
  apt-get install coreutils           (Debian/Ubuntu: already the system default)
  nix-shell -p coreutils              (macOS: the system tools are BSD, not GNU)

Searched: %s`, gnuDirErr, strings.Join(gnuCandidates(), ", "))
	}
	return gnuDirPath, gnuDirVer
}

// gnuCandidates lists the directories to probe, most explicit first.
func gnuCandidates() []string {
	var dirs []string
	if d := os.Getenv("FERN_GNU_COREUTILS"); d != "" {
		dirs = append(dirs, d)
	}
	if p, err := exec.LookPath("yes"); err == nil {
		dirs = append(dirs, filepath.Dir(p))
	}
	dirs = append(dirs, "/usr/bin", "/bin", "/usr/local/bin", "/opt/homebrew/opt/coreutils/libexec/gnubin")
	// A nix store has no stable path, so it is globbed rather than named.
	if matches, err := filepath.Glob("/nix/store/*-coreutils-*/bin"); err == nil {
		dirs = append(dirs, matches...)
	}
	return dirs
}

// gnuVersion reports the coreutils version `dir` holds, or an error if
// it does not hold GNU coreutils at all. `yes --version` is the probe:
// every utility in the corpus answers it, and yes(1) is the one that
// exists nowhere else under that name.
func gnuVersion(dir string) (string, error) {
	bin := filepath.Join(dir, "yes")
	if _, err := os.Stat(bin); err != nil {
		return "", err
	}
	argv := append(crossPrefix(), bin, "--version")
	out, err := exec.Command(argv[0], argv[1:]...).Output()
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(string(out), "\n")
	if !strings.Contains(first, "(GNU coreutils)") {
		return "", fmt.Errorf("%s is not GNU coreutils: %q", bin, first)
	}
	return strings.TrimSpace(first), nil
}

// referenceBin is the GNU binary for `util`.
func referenceBin(t *testing.T, util string) string {
	t.Helper()
	dir, ver := gnuDir(t)
	bin := filepath.Join(dir, util)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("reference %s: %v (from %s, %s)", util, err, dir, ver)
	}
	return bin
}

// fernTarget is the -target the utilities are compiled for: the host's,
// unless FERN_COREUTILS_TARGET names another one to cross-run under
// FERN_COREUTILS_QEMU.
//
// That cross leg exists because `long double` is the machine's, so this
// corpus proves exactly one format — the host's — and the second is
// otherwise reachable only from CI's aarch64 runner. It is a debug
// affordance for that class of bug (#8513) and not a gate;
// docs/COREUTILS.md has the recipe.
func fernTarget(t *testing.T) string {
	t.Helper()
	if target := os.Getenv("FERN_COREUTILS_TARGET"); target != "" {
		if len(crossPrefix()) == 0 {
			t.Fatalf("FERN_COREUTILS_TARGET=%s needs FERN_COREUTILS_QEMU to run the binaries it builds", target)
		}
		return target
	}
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "x86-64-linux"
	case "linux/arm64":
		return "arm64-linux"
	case "darwin/arm64":
		return "arm64-darwin"
	default:
		t.Fatalf("no Fern target for %s/%s (docs/BACKEND-PARITY.md: there is no Darwin x86-64 backend)", runtime.GOOS, runtime.GOARCH)
		return ""
	}
}

var (
	fernBinsMu sync.Mutex
	fernBins   = map[string]string{}
	fernBinDir string
)

// fernBin compiles coreutils/<util>.fern for the host and returns the
// binary, once per test process.
//
// Built without -O: the parity gate wants the assert() checks live, so
// a violated internal invariant fails loudly here instead of being
// elided into whatever the release build does next. The bench script
// builds with -O, where the comparison against GNU's own -O2 binaries
// is the point.
func fernBin(t *testing.T, util string) string {
	t.Helper()
	fernBinsMu.Lock()
	defer fernBinsMu.Unlock()
	if bin, ok := fernBins[util]; ok {
		return bin
	}
	if fernBinDir == "" {
		dir, err := os.MkdirTemp("", "fern-coreutils-")
		if err != nil {
			t.Fatalf("temp dir: %v", err)
		}
		fernBinDir = dir
	}
	fern := e2eharness.BuildLangBinForInterp(t)
	src := filepath.Join(repoRoot(t), "coreutils", util+".fern")
	bin := filepath.Join(fernBinDir, util)
	cmd := exec.Command(fern, "-target", fernTarget(t), "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile %s: %v\n%s", src, err, out)
	}
	fernBins[util] = bin
	return bin
}

// repoRoot is the checkout root, derived from this file's own path so
// it does not depend on where `go test` was invoked.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(self)))
}

// crossPrefix is the emulator command FERN_COREUTILS_QEMU names, split on
// blanks — for example `qemu-aarch64 -L /usr/aarch64-linux-gnu`, where the
// sysroot is what the dynamically linked GNU binaries need and the static
// Fern ones do not. Empty when the corpus runs natively.
func crossPrefix() []string {
	return strings.Fields(os.Getenv("FERN_COREUTILS_QEMU"))
}

// run executes `bin` with argv[0] = argv0 and reports what happened.
func (inv invocation) run(t *testing.T, bin, argv0 string) outcome {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Path = bin
	cmd.Args = append([]string{argv0}, inv.args...)
	if pre := crossPrefix(); len(pre) > 0 {
		// qemu's -0 sets the argv[0] the emulated process sees, which is
		// the whole point of running both sides with the bare name.
		emu, err := exec.LookPath(pre[0])
		if err != nil {
			t.Fatalf("FERN_COREUTILS_QEMU names %s: %v", pre[0], err)
		}
		cmd.Path = emu
		cmd.Args = append(append(append([]string{pre[0]}, pre[1:]...), "-0", argv0, bin), inv.args...)
	}
	cmd.Env = append(baseEnv(), inv.env...)
	cmd.Stdin = strings.NewReader(inv.stdin)
	if inv.tty {
		pty, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("open a pseudo-terminal for fd 3: %v", err)
		}
		defer pty.Close()
		cmd.ExtraFiles = []*os.File{pty}
	}

	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf

	var out []byte
	switch inv.stdout {
	case stdoutClosed:
		// A typed nil *os.File reaches os.StartProcess as a nil entry
		// in its Files, which closes that descriptor in the child —
		// the one way through os/exec to hand a child a closed fd 1.
		cmd.Stdout = (*os.File)(nil)
		_ = cmd.Run()
	case stdoutFull:
		if runtime.GOOS != "linux" {
			t.Skip("/dev/full is a Linux device")
		}
		f, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("open /dev/full: %v", err)
		}
		defer f.Close()
		cmd.Stdout = f
		_ = cmd.Run()
	}
	if inv.stdout != stdoutCaptured {
		// Nothing to read back: the point is the reaction on stderr and
		// in the exit status.
	} else if inv.limit > 0 {
		// A utility that never stops: read a bounded prefix, then close
		// the read end so the process meets a closed pipe. How it reacts
		// to that is part of the comparison — GNU dies of SIGPIPE, and a
		// utility that instead exited 0 would be diverging.
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		cmd.Stdout = w
		if err := cmd.Start(); err != nil {
			t.Fatalf("start %s: %v", bin, err)
		}
		w.Close()
		buf := make([]byte, inv.limit)
		n, rerr := io.ReadFull(r, buf)
		if rerr != nil && !errors.Is(rerr, io.EOF) && !errors.Is(rerr, io.ErrUnexpectedEOF) {
			t.Fatalf("read %s: %v", bin, rerr)
		}
		out = buf[:n]
		r.Close()
		_ = cmd.Wait()
	} else {
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		_ = cmd.Run()
		out = outBuf.Bytes()
	}

	// A case the kernel refuses to start at all — an argv holding a NUL,
	// say — leaves no ProcessState. Report the case rather than dying on
	// a nil dereference three frames down.
	if cmd.ProcessState == nil {
		t.Fatalf("%s %s never ran: the invocation is not one exec can deliver", bin, quoteArgs(inv.args))
	}
	res := outcome{stdout: out, stderr: errBuf.Bytes(), exit: cmd.ProcessState.ExitCode()}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		res.signal = ws.Signal().String()
	}
	return res
}

// requireParity runs every case against both implementations and
// reports each difference.
func requireParity(t *testing.T, util string, cases []invocation) {
	t.Helper()
	ref := referenceBin(t, util)
	ours := fernBin(t, util)
	_, ver := gnuDir(t)
	t.Logf("reference: %s (%s)", ref, ver)

	for _, inv := range cases {
		t.Run(inv.name, func(t *testing.T) {
			want := inv.run(t, ref, util)
			got := inv.run(t, ours, util)
			if !bytes.Equal(want.stdout, got.stdout) {
				t.Errorf("stdout differs for %s %s\n gnu: %s\nfern: %s", util, quoteArgs(inv.args), quote(want.stdout), quote(got.stdout))
			}
			if !bytes.Equal(want.stderr, got.stderr) {
				t.Errorf("stderr differs for %s %s\n gnu: %s\nfern: %s", util, quoteArgs(inv.args), quote(want.stderr), quote(got.stderr))
			}
			if want.how() != got.how() {
				t.Errorf("status differs for %s %s: gnu %s, fern %s", util, quoteArgs(inv.args), want.how(), got.how())
			}
		})
	}
}

// quote renders bytes readably: printable ASCII as itself, everything
// else as an escape, so a difference in a NUL or a stray CR is visible
// in the failure rather than swallowed by the terminal.
func quote(b []byte) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, c := range b {
		switch {
		case c == '\n':
			sb.WriteString(`\n`)
		case c == '\t':
			sb.WriteString(`\t`)
		case c == '"':
			sb.WriteString(`\"`)
		case c == '\\':
			sb.WriteString(`\\`)
		case c >= 0x20 && c < 0x7f:
			sb.WriteByte(c)
		default:
			fmt.Fprintf(&sb, `\x%02x`, c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func quoteArgs(args []string) string {
	if len(args) == 0 {
		return "(no arguments)"
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = quote([]byte(a))
	}
	return strings.Join(parts, " ")
}

// requireHelpVersion gates the two outputs docs/COREUTILS.md exempts
// from byte parity, because their content names the implementation:
// `--version` says which program this is, and `--help` is our own prose
// rather than GNU's GPL-licensed text and hyperlink escapes.
//
// Exempt is not unchecked. Everything ABOUT them still has to match —
// the exit status, which stream carries the output, that nothing lands
// on the other one — and our own text has to be well formed. Without
// this a utility could answer `--help` with silence and exit 0.
func requireHelpVersion(t *testing.T, util string, args []string, wantExit int, wantFirstWord string) {
	t.Helper()
	ref := referenceBin(t, util)
	ours := fernBin(t, util)
	inv := invocation{args: args}
	want := inv.run(t, ref, util)
	got := inv.run(t, ours, util)

	if want.exit != wantExit {
		t.Fatalf("%s %s: reference exited %d, the corpus says %d — the case is wrong, not the utility", util, quoteArgs(args), want.exit, wantExit)
	}
	if got.how() != want.how() {
		t.Errorf("%s %s: gnu %s, fern %s", util, quoteArgs(args), want.how(), got.how())
	}
	if len(want.stderr) != 0 || len(got.stderr) != 0 {
		t.Errorf("%s %s: stderr must be empty on both sides\n gnu: %s\nfern: %s", util, quoteArgs(args), quote(want.stderr), quote(got.stderr))
	}
	if len(got.stdout) == 0 {
		t.Errorf("%s %s: wrote nothing to stdout", util, quoteArgs(args))
		return
	}
	first, _, _ := strings.Cut(string(got.stdout), "\n")
	if !strings.HasPrefix(first, wantFirstWord) {
		t.Errorf("%s %s: first line is %q, want it to start with %q", util, quoteArgs(args), first, wantFirstWord)
	}
	if !strings.HasSuffix(string(got.stdout), "\n") {
		t.Errorf("%s %s: output does not end in a newline", util, quoteArgs(args))
	}
}

// requireVersion is requireHelpVersion for `--version`, whose first
// line is fixed by docs/COREUTILS.md: `<util> (Fern coreutils) <ver>`.
func requireVersion(t *testing.T, util string, args []string, wantExit int) {
	t.Helper()
	requireHelpVersion(t, util, args, wantExit, util+" (Fern coreutils) ")
}

// requireHelp is requireHelpVersion for `--help`, whose first line
// names the program the way every usage line does.
func requireHelp(t *testing.T, util string, args []string, wantExit int) {
	t.Helper()
	requireHelpVersion(t, util, args, wantExit, "Usage: "+util)
}
