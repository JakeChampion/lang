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
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

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
	// timeout bounds ONE run, after which the harness kills the child
	// and the outcome is "did not finish". Zero leaves it unbounded,
	// which is what every written-down case wants — they all terminate.
	// The randomized differential sets one because the REFERENCE does
	// not always terminate: GNU expr never returns from
	// `expr bba : '\(b\|\|a\)\?*'`, and a sweep that waited would hang
	// instead of naming the input that hung it.
	timeout time.Duration
	// stdinPath, when set, is opened and handed to the child as fd 0
	// instead of a pipe — `prog < file`. A regular file there is what
	// lets a utility seek or fstat its input, and a directory is how
	// `cat < dir` reaches EISDIR on stdin.
	stdinPath string
	// stdoutPath, when set, is opened O_APPEND as the child's fd 1 —
	// `prog >> file` — and its content after the run is what the
	// harness compares as stdout, restoring the file for the other
	// side. It is how `cat f >> f` meets `input file is output file`.
	stdoutPath string
	// follow drives a case whose child never exits on its own — a
	// `tail -f`. The harness plays the writer: each step fires once
	// `after` bytes of stdout have arrived, exactly `limit` bytes are
	// read in all, then the child is sent SIGTERM, which both sides die
	// of. A step that never fires or bytes that never arrive end in
	// followDeadline and a diff.
	//
	// `limit` decides WHEN the child dies, and stderr is compared
	// whatever it wrote before that — so a limit that lands inside the
	// child's startup output makes any diagnostic it prints there a race
	// between the two implementations rather than a comparison. Set it
	// past the whole initial pass and let the deadline stop the read.
	follow []followStep
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
	// sigint sends SIGINT to the child once `limit` bytes of stdout
	// have been read, before the read end closes. It needs `limit` and
	// a stdin long enough to outlast it, which together make the case
	// deterministic: the child is blocked writing into the full pipe
	// when the signal lands, and the stdout compared is exactly the
	// `limit` bytes read before it. The signal disposition is then the
	// whole observable — `tee` dies of SIGINT, `tee -i` ignores it and
	// dies of the SIGPIPE that the closing read end delivers instead.
	sigint bool
	// dir is the working directory the child runs in; the default is the
	// harness's own. A case needs one when an operand has to be
	// RELATIVE, which is the only way to spell uniq's output operand as
	// `-c` or `+2` and see what POSIXLY_CORRECT does with it — and it is
	// the whole of what `pwd` is about, where a path reached through a
	// symbolic link is how the logical and physical answers differ.
	dir string
	// prepare runs immediately before each side starts, so a case for a
	// utility that WRITES hands both implementations the same tree: the
	// GNU run would otherwise leave its output behind for the Fern run
	// to append to.
	prepare func(t *testing.T)
	// artifacts are the paths the case writes. Each is read back after
	// each side has run and the two are required to match, which is
	// docs/COREUTILS.md's "the same resulting tree". A path that does
	// not exist compares equal to a path that does not exist.
	artifacts []string
	// mask rewrites the one part of a run that two correct
	// implementations cannot agree on: mktemp's whole answer is a run of
	// RANDOM characters, so an unmasked case would fail every time. It is
	// applied to stdout and to the names in the tree comparison — never
	// to stderr, which carries the template with its X's intact and is
	// compared byte for byte. The function is the case's own, so it
	// rewrites the run by POSITION and leaves everything around it —
	// directory, prefix, suffix, length — under comparison; a byte
	// outside the alphabet the run is drawn from is left alone so that it
	// still differs from the reference's.
	//
	// Keep it to genuinely unpredictable output. Anything a mask hides
	// is a thing this corpus no longer proves.
	mask func(string) string
	// seedTree is the same requirement for a utility whose output names
	// cannot be listed in advance: `split` chooses `xaa`, `xab`, … from
	// the input's length. It gets a fresh working directory per SIDE,
	// seeded by this function, and EVERYTHING left under it joins the
	// comparison — so a file neither side was expected to write fails
	// too, which an `artifacts` list cannot express. Takes precedence
	// over dir.
	seedTree func(t *testing.T, dir string)
	// umask is the file-mode creation mask the child inherits, for a
	// utility whose answer depends on it. `mkdir` is what needs it: the
	// mode a directory ends up with is the umask applied to 0777, and
	// `-m` changes WHICH of its clauses the mask reaches rather than
	// switching it off.
	//
	// A mask is process-global state that a child inherits at fork, so a
	// case naming one runs with every other case excluded (maskLock) and
	// the mask is restored before the next one starts. 0 is a real mask
	// — it is the one under which `mkdir d` is 0777 — so the field is a
	// pointer and `withMask` writes it.
	umask *int
	// ownership puts each entry's uid and gid into the tree comparison.
	// Off by default, and deliberately: an id is one more thing that can
	// differ between two machines for reasons that are not the utility's,
	// and no other utility here sets one.
	//
	// `chown` and `chgrp` have nothing else to prove. Every other field of
	// treeEntry is one that a run doing NOTHING AT ALL leaves untouched, so
	// without this their whole corpus passes on a utility that parsed its
	// operands, printed every line, and made no call. Needs seedTree.
	ownership bool
	// crossDev seeds a second working directory on a DIFFERENT
	// filesystem from the seedTree one, reachable from it under the
	// name `xdev`. It is the only way a case can reach EXDEV, which is
	// what `mv`'s copy-then-unlink fallback hangs off: `rename` between
	// two names on one filesystem never produces it.
	//
	// Everything under it joins the tree comparison as `xdev/…`, with
	// the hard-link groups numbered across both roots. The link itself
	// does not: its target is a fresh directory per run, so the two
	// sides would differ on that one name while agreeing about every
	// file under it. Needs seedTree.
	crossDev func(t *testing.T, dir string)
}

// treeEntry is one path under a seedTree case's working directory, as the
// comparison sees it: the name relative to that directory, its kind, its
// twelve-bit mode (setuid / setgid / sticky included, which `mkdir -m`
// sets), a symlink's target verbatim, and for a regular file its bytes and
// which hard-link group it belongs to. Timestamps are deliberately absent:
// the two sides run seconds apart and nothing in the corpus sets one.
//
// `owner` is the exception to "absent", and it is OPT-IN rather than always
// read: only a case that sets `invocation.ownership` fills it. `chown` and
// `chgrp` have nothing else to prove — a `chown` that parsed its operands,
// printed every line and changed no id passes every other field here — and
// for every other utility an id is one more thing that can differ between two
// machines for reasons that are not the utility's.
type treeEntry struct {
	name    string
	kind    string
	mode    uint32
	target  string
	content string
	// "uid:gid", or "" for a case that did not ask. Read with lstat, so a
	// symlink reports its own rather than its target's — which is the whole
	// difference between `chown -h` and `chown`.
	owner string
	// rdev is the device number a character or block node carries, raw:
	// the two sides run on one kernel, so the dev_t compares directly and
	// nothing here has to know how that kernel packs a major and a minor.
	// Zero for everything that is not a device, which is what `mknod`
	// needs compared — the kind alone cannot tell `mknod n c 1 3` from
	// `mknod n c 1 4`.
	rdev uint64
	// group numbers the (dev, ino) equivalence classes in walk order, so
	// two names sharing an inode share a number on both sides while the
	// inodes themselves — which differ between the runs — never reach the
	// comparison. That is what separates `ln a b` from `cp a b`.
	group int
}

// followStep is one thing the harness does to a followed file, once
// `after` bytes of stdout have been read.
type followStep struct {
	after int
	// act is "append" (data onto path), "truncate" (path to data),
	// "remove" (unlink path) or "rename" (data, a path, over path).
	act  string
	path string
	data string
}

// followDeadline bounds a follow case: a side that stops producing
// output before `limit` is reported with what it produced.
const followDeadline = 8 * time.Second

// sigintSettle is how long a `sigint` case waits after the signal
// before closing the read end, so a child that dies of SIGINT has done
// so before the SIGPIPE that would otherwise be the cause of death.
const sigintSettle = 250 * time.Millisecond

func (st followStep) run(t *testing.T) {
	t.Helper()
	var err error
	switch st.act {
	case "append":
		var f *os.File
		f, err = os.OpenFile(st.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err == nil {
			_, err = f.WriteString(st.data)
			f.Close()
		}
	case "truncate":
		err = os.WriteFile(st.path, []byte(st.data), 0o644)
	case "remove":
		err = os.Remove(st.path)
	case "rename":
		err = os.Rename(st.data, st.path)
	default:
		t.Fatalf("follow step %q is not one the harness knows", st.act)
	}
	if err != nil {
		t.Fatalf("follow step %s %s: %v", st.act, st.path, err)
	}
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
	// overran is set when invocation.timeout ran out and the harness
	// killed the process, which is not something a signal can be told
	// from: the kill IS a signal.
	overran bool
	// tree is the working directory the run left behind, for a case
	// that asked for one.
	tree []treeEntry
}

func (o outcome) how() string {
	if o.overran {
		return "did not finish"
	}
	if o.signal != "" {
		return "killed by " + o.signal
	}
	return fmt.Sprintf("exit %d", o.exit)
}

// artifact is one path's state after a run: its bytes, or the fact that
// it is absent. `uniq f -` leaves no output file, and a Fern build that
// created an empty one would be diverging.
type artifact struct {
	name    string
	present bool
	data    []byte
}

func (inv invocation) prep(t *testing.T) {
	t.Helper()
	if inv.prepare == nil {
		return
	}
	// prepare CREATES files, and the creation mask is process-global. It
	// is called outside run(), so without this it can land in the window
	// where a case naming a mask holds it and build the whole fixture
	// under someone else's — intermittently, and only visibly on the
	// modes, which is exactly what the corpus compares. Taking the read
	// side excludes it from that window and still lets prepares run
	// concurrently with each other. Not reentrant, and never called from
	// inside run(), which takes the same lock.
	defer holdMask(nil)()
	inv.prepare(t)
}

func (inv invocation) readArtifacts(t *testing.T) []artifact {
	t.Helper()
	out := make([]artifact, 0, len(inv.artifacts))
	for _, name := range inv.artifacts {
		b, err := os.ReadFile(name)
		switch {
		case err == nil:
			out = append(out, artifact{name: name, present: true, data: b})
		case errors.Is(err, os.ErrNotExist):
			out = append(out, artifact{name: name, present: false})
		default:
			t.Fatalf("read artifact %s: %v", name, err)
		}
	}
	return out
}

// diffArtifacts reports every path the two runs left in different states.
func diffArtifacts(t *testing.T, util string, inv invocation, want, got []artifact, wantWho, gotWho string) {
	t.Helper()
	for i := range want {
		w, g := want[i], got[i]
		if w.present != g.present {
			t.Errorf("%s %s: %s %s %s, %s %s it", util, quoteArgs(inv.args), wantWho, presence(w.present), w.name, gotWho, presence(g.present))
			continue
		}
		if w.present && !bytes.Equal(w.data, g.data) {
			t.Errorf("%s %s: %s differs\n%8s: %s\n%8s: %s", util, quoteArgs(inv.args), w.name, wantWho, quote(w.data), gotWho, quote(g.data))
		}
	}
}

func presence(ok bool) string {
	if ok {
		return "wrote"
	}
	return "left no"
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

// The probe reads at most this much and waits at most this long. BSD
// yes(1) takes `--version` as the string to REPEAT and prints it until
// killed, so an unbounded read of a candidate that is not GNU grows
// without limit: /usr/bin is on the candidate list and is BSD on macOS,
// where the probe reached 26 GB in five seconds and the test binary was
// killed with no diagnostic but `signal: killed`.
const (
	gnuProbeBytes   = 4096
	gnuProbeTimeout = 10 * time.Second
)

// gnuVersion reports the coreutils version `dir` holds, or an error if
// it does not hold GNU coreutils at all. `yes --version` is the probe:
// every utility in the corpus answers it, and yes(1) is the one that
// exists nowhere else under that name.
//
// The version line is decided by the output's first line, so the probe
// takes one bounded chunk and kills the child rather than waiting for
// an exit. A candidate that writes nothing before the deadline is not
// the reference either, so the timeout is an answer and not a hang.
func gnuVersion(dir string) (string, error) {
	bin := filepath.Join(dir, "yes")
	if _, err := os.Stat(bin); err != nil {
		return "", err
	}
	argv := crossArgv(bin, "--version")
	ctx, cancel := context.WithTimeout(context.Background(), gnuProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, gnuProbeBytes))
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	first, _, _ := strings.Cut(string(out), "\n")
	if !strings.Contains(first, "(GNU coreutils)") {
		if len(out) == 0 && readErr != nil {
			return "", fmt.Errorf("%s: %w", bin, readErr)
		}
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
	root := repoRoot(t)
	// The compile below is a child process, so nothing it reads reaches the go
	// command's test cache on its own: without this the suite reports its last
	// result after a `.fern` edit, having run nothing (#9087).
	e2eharness.TrackFernSources(t, filepath.Join(root, "coreutils"), util+".fern")
	src := filepath.Join(root, "coreutils", util+".fern")
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

// crossArgv is the argv for running a binary built for FERN_COREUTILS_TARGET:
// the binary alone when the host is that target, and the emulator in front of
// it when it is not. Every target binary needs this, the self-host COMPILER
// included — it is built for the target like the utilities it compiles, so
// exec'ing it directly is an exec-format error the moment the target is not
// the host's.
func crossArgv(bin string, args ...string) []string {
	return append(append(crossPrefix(), bin), args...)
}

// crossDevName is what a crossDev case's second filesystem is reached
// through from the working directory. Both sides spell it the same way,
// which is what keeps the operands — and so the diagnostics quoting them
// — identical between the two runs.
const crossDevName = "xdev"

// crossDevDir makes a fresh directory on a filesystem OTHER than the one
// holding `near`, and fails if it cannot: a case that silently ran both
// of its names on one filesystem would never reach EXDEV and would prove
// the opposite of what it claims.
func crossDevDir(t *testing.T, near string) string {
	t.Helper()
	base := os.Getenv("FERN_COREUTILS_XDEV")
	if base == "" {
		base = "/dev/shm"
	}
	dir, err := os.MkdirTemp(base, "fern-coreutils-xdev-")
	if err != nil {
		t.Fatalf(`make a directory under %s: %v

A cross-device case needs a second filesystem to move a file onto, and
EXDEV is the whole point of it. Name a directory on one with
FERN_COREUTILS_XDEV=/path (a tmpfs mount is the usual answer).`, base, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if deviceOf(t, dir) == deviceOf(t, near) {
		t.Fatalf(`%s is on the same filesystem as %s, so nothing here can reach EXDEV.
Name a directory on another one with FERN_COREUTILS_XDEV=/path.`, dir, near)
	}
	return dir
}

func deviceOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%s: no stat_t, so which filesystem it is on cannot be asked", path)
	}
	return uint64(st.Dev)
}

// withMask is the `umask` field of a case that names one.
func withMask(mask int) *int {
	return &mask
}

// maskLock guards the process creation mask, which a child inherits at
// fork and so cannot be set per-child. A case that names a mask takes
// the write side, excluding every other case for the length of its run;
// every other case takes the read side and they still run concurrently.
// The self-host leg runs its cases in parallel, so without this a case
// under one mask would seed and create under another's.
var maskLock sync.RWMutex

// holdMask takes the lock the case needs and returns the release. It does
// NOT set the mask: the lock is held for the whole run so no other case
// executes under this one's mask, but the mask itself covers only the
// child, applied by applyMask below.
//
// The harness's own scaffolding — t.TempDir, the seeded tree, and the
// tree read back afterwards — must be built under the ordinary mask. A
// case naming 0777 that created its working directory under it produced a
// 0000 directory, which is invisible as root and `permission denied` for
// everyone else, so the whole matrix passed here and failed on CI.
func holdMask(mask *int) func() {
	if mask == nil {
		maskLock.RLock()
		return maskLock.RUnlock
	}
	maskLock.Lock()
	return maskLock.Unlock
}

// applyMask sets the case's mask around the child and returns the restore.
// The caller already holds the write side of maskLock.
func applyMask(mask *int) func() {
	if mask == nil {
		return func() {}
	}
	previous := syscall.Umask(*mask)
	return func() { syscall.Umask(previous) }
}

// run executes `bin` with argv[0] = argv0 and reports what happened.
func (inv invocation) run(t *testing.T, bin, argv0 string) outcome {
	t.Helper()
	defer holdMask(inv.umask)()
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
	var workDir, crossRoot string
	var seeded map[string]bool
	if inv.seedTree != nil {
		workDir = t.TempDir()
		// Registered after t.TempDir so it runs BEFORE TempDir's own
		// removal: cleanups are LIFO.
		t.Cleanup(func() { openTreeForCleanup(workDir) })
		inv.seedTree(t, workDir)
		cmd.Dir = workDir
		if inv.crossDev != nil {
			crossRoot = crossDevDir(t, workDir)
			inv.crossDev(t, crossRoot)
			if err := os.Symlink(crossRoot, filepath.Join(workDir, crossDevName)); err != nil {
				t.Fatalf("link %s into the working directory: %v", crossRoot, err)
			}
		}
		if inv.mask != nil {
			seeded = map[string]bool{}
			for _, e := range readTree(t, workDir, inv.ownership) {
				seeded[e.name] = true
			}
		}
	} else {
		cmd.Dir = inv.dir
	}
	// The mask covers the CHILD and nothing else. Everything above this
	// line — the working directory, the seeded tree, the snapshot of it —
	// is the harness's own and is built under the ordinary mask. What is
	// below only reads and chmods, neither of which a mask touches, so
	// restoring at the end of the run is soon enough.
	defer applyMask(inv.umask)()
	cmd.Stdin = strings.NewReader(inv.stdin)
	if inv.tty {
		pty, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("open a pseudo-terminal for fd 3: %v", err)
		}
		defer pty.Close()
		cmd.ExtraFiles = []*os.File{pty}
	}
	if inv.stdinPath != "" {
		f, err := os.Open(inv.stdinPath)
		if err != nil {
			t.Fatalf("open stdin %s: %v", inv.stdinPath, err)
		}
		defer f.Close()
		cmd.Stdin = f
	}

	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf

	var out []byte
	var overran bool
	switch inv.stdout {
	case stdoutCaptured:
		if inv.stdoutPath != "" {
			out = inv.runToFile(t, cmd)
		} else if len(inv.follow) > 0 {
			out = inv.runFollow(t, cmd)
		}
	case stdoutClosed:
		// A typed nil *os.File reaches os.StartProcess as a nil entry
		// in its Files, which closes that descriptor in the child —
		// the one way through os/exec to hand a child a closed fd 1.
		cmd.Stdout = (*os.File)(nil)
		overran = inv.runBounded(cmd)
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
		overran = inv.runBounded(cmd)
	}
	if inv.stdout != stdoutCaptured || inv.stdoutPath != "" || len(inv.follow) > 0 {
		// Nothing more to read back: either the point is the reaction
		// on stderr and in the exit status, or a runner above did it.
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
		var timedOut bool
		out, timedOut = readUpTo(r, inv.limit, nil)
		if timedOut {
			t.Errorf("%s %s: %d of %d bytes arrived before the deadline", bin, quoteArgs(inv.args), len(out), inv.limit)
			_ = cmd.Process.Kill()
		}
		if inv.sigint && !timedOut {
			_ = cmd.Process.Signal(syscall.SIGINT)
			// The child is blocked writing into the full pipe until the
			// read end closes below, so the signal is delivered while it
			// is still running whatever it does about SIGINT.
			time.Sleep(sigintSettle)
		}
		r.Close()
		_ = cmd.Wait()
	} else {
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		overran = inv.runBounded(cmd)
		out = outBuf.Bytes()
	}

	// A case the kernel refuses to start at all — an argv holding a NUL,
	// say — leaves no ProcessState. Report the case rather than dying on
	// a nil dereference three frames down.
	if cmd.ProcessState == nil {
		t.Fatalf("%s %s never ran: the invocation is not one exec can deliver", bin, quoteArgs(inv.args))
	}
	res := outcome{stdout: out, stderr: errBuf.Bytes(), exit: cmd.ProcessState.ExitCode(), overran: overran}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		res.signal = ws.Signal().String()
	}
	if workDir != "" {
		groups := map[[2]uint64]int{}
		res.tree = readTreeInto(t, workDir, "", groups, inv.ownership)
		if crossRoot != "" {
			// The link to the other filesystem is the harness's own
			// scaffolding and its target is a fresh path per run, so it
			// leaves the comparison and everything it reaches joins it.
			kept := res.tree[:0]
			for _, e := range res.tree {
				if e.name != crossDevName {
					kept = append(kept, e)
				}
			}
			res.tree = append(kept, readTreeInto(t, crossRoot, crossDevName+"/", groups, inv.ownership)...)
			sort.Slice(res.tree, func(i, j int) bool { return res.tree[i].name < res.tree[j].name })
		}
	}
	if inv.mask != nil {
		res.stdout = []byte(inv.mask(string(res.stdout)))
		// Only what the RUN left behind is masked. A seeded name the
		// utility never touched is identical on both sides already, and
		// masking it can only lose signal — `td1` and `td2` both become
		// `XXX` under a three-character mask, and the comparison then has
		// two entries under one name.
		taken := map[string]bool{}
		for i := range res.tree {
			if !seeded[res.tree[i].name] {
				res.tree[i].name = inv.mask(res.tree[i].name)
			}
			if taken[res.tree[i].name] {
				t.Errorf("%s %s: the mask collapses two entries onto %q, so the tree comparison proves nothing — narrow it", bin, quoteArgs(inv.args), res.tree[i].name)
			}
			taken[res.tree[i].name] = true
		}
		regroup(res.tree)
	}
	return res
}

// regroup re-sorts a masked tree by name and renumbers the hard-link
// groups in that order. readTree numbers them by walk order, which visits
// a masked name wherever its UNMASKED spelling sorted to — so two runs
// that agree about every file still disagree about the numbers. The
// equivalence the field encodes is what the comparison is about, and that
// survives; only the order it is read in changes.
func regroup(es []treeEntry) {
	sort.Slice(es, func(i, j int) bool { return es[i].name < es[j].name })
	seen := map[int]int{}
	for i := range es {
		if es[i].group == 0 {
			continue
		}
		n, ok := seen[es[i].group]
		if !ok {
			n = len(seen) + 1
			seen[es[i].group] = n
		}
		es[i].group = n
	}
}

// readTree lists everything under root, deepest paths included, with each
// regular file's bytes. A file too large to hold is summarised by its size
// instead, which still differs when the two sides disagree. Symlinks are
// recorded, not followed.
// ownerOf is the entry's "uid:gid" as lstat reports it. Only a case that set
// `ownership` calls it; nothing else here reads an id.
func ownerOf(t *testing.T, path string, info os.FileInfo) string {
	t.Helper()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("no stat for %s — the ownership comparison cannot run on this platform", path)
	}
	return fmt.Sprintf("%d:%d", st.Uid, st.Gid)
}

func readTree(t *testing.T, root string, ownership bool) []treeEntry {
	t.Helper()
	return readTreeInto(t, root, "", map[[2]uint64]int{}, ownership)
}

// readTreeInto is readTree with each name prefixed and the hard-link
// groups numbered into a caller's map, so a case spanning two roots
// reads as one tree.
// openTreeForCleanup reopens everything under root so t.TempDir's own
// RemoveAll can get in. A utility under test is entitled to leave a tree
// nobody may read or enter — that is the point of `chmod -R 0` — and Go
// reports the failed removal as a test failure, which is a complaint about
// the harness rather than about the code under test. Best effort: anything
// that cannot be opened is left for RemoveAll to report.
func openTreeForCleanup(root string) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Reached when a directory could not be listed. Open it and
			// let the walk carry on; the retry below picks up its
			// contents.
			_ = os.Chmod(path, 0o700)
			return nil
		}
		if info.Mode()&os.ModeSymlink == 0 {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	// One more pass: the first opened the directories that blocked the
	// walk, so their contents are only visible now.
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.Mode()&os.ModeSymlink == 0 {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
}

// openTreeForWalk makes every entry under root readable and returns the
// permission bits each one had, keyed by path, for the ones it changed.
//
// A utility can be ASKED to leave an entry nobody may read — `mkdir -m 0`,
// `chmod 0 f`, or any mode under a mask that takes owner r-x off — and the
// tree still has to be compared. As root that is invisible, which is why this was missed
// locally and failed on CI as an ordinary user.
//
// It cannot be done inside the walk: filepath.Walk reads a directory's names
// BEFORE it calls the callback for that directory, so the callback only ever
// sees the failure. The pass is therefore top-down and ahead of the walk,
// recording each original mode before opening it, and the walk reports the
// recorded one. Nothing is restored: the mode has been captured, and
// t.TempDir's own cleanup has to get in here too.
func openTreeForWalk(t *testing.T, root string) map[string]uint32 {
	t.Helper()
	opened := map[string]uint32{}
	// The root is the harness's own working directory and its mode is not
	// part of the comparison (the walk skips "."), but a utility can have
	// made it unsearchable — `chmod -R 0 .` — and then nothing below it
	// can be reached at all.
	_ = os.Chmod(root, 0o700)
	var descend func(dir string)
	descend = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}
			mode := info.Mode()
			// A directory has to be readable AND searchable to walk
			// into; a regular file only readable, since its contents
			// join the comparison. A symlink is never opened.
			need := uint32(0o400)
			if entry.IsDir() {
				need = 0o500
			} else if !mode.IsRegular() {
				continue
			}
			if perm := permBits(mode); perm&need != need {
				opened[path] = perm
				if err := os.Chmod(path, os.FileMode(perm|0o700)); err != nil {
					t.Fatalf("open %s so the tree can be read: %v", path, err)
				}
			}
			if entry.IsDir() {
				descend(path)
			}
		}
	}
	descend(root)
	return opened
}

func readTreeInto(t *testing.T, root, prefix string, groups map[[2]uint64]int, ownership bool) []treeEntry {
	t.Helper()
	opened := openTreeForWalk(t, root)
	var out []treeEntry
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		mode := info.Mode()
		e := treeEntry{name: prefix + rel, kind: treeKind(mode), mode: permBits(mode)}
		if ownership {
			e.owner = ownerOf(t, path, info)
		}
		if was, ok := opened[path]; ok {
			// Reopened below so the walk could enter it; the mode the
			// utility actually left is the one recorded here.
			e.mode = was
		}
		switch {
		case mode&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			e.target = target
		case mode&os.ModeDevice != 0:
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				e.rdev = uint64(st.Rdev)
			}
		case mode.IsRegular():
			e.group = linkGroup(info, groups)
			if info.Size() > 1<<22 {
				e.content = fmt.Sprintf("<%d bytes>", info.Size())
			} else {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				e.content = string(b)
			}
		}
		out = append(out, e)
		return nil
	})
	if err != nil {
		t.Fatalf("read the tree under %s: %v", root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// runBounded runs cmd and reports whether inv.timeout ran out first, in
// which case the child was killed.
func (inv invocation) runBounded(cmd *exec.Cmd) bool {
	if inv.timeout <= 0 {
		_ = cmd.Run()
		return false
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	fired := make(chan struct{})
	timer := time.AfterFunc(inv.timeout, func() {
		_ = cmd.Process.Kill()
		close(fired)
	})
	_ = cmd.Wait()
	if timer.Stop() {
		return false
	}
	<-fired
	return true
}

// runToFile runs cmd with fd 1 appended to inv.stdoutPath and returns
// the bytes the run added to the file, with the file put back as it
// was for the other side.
func (inv invocation) runToFile(t *testing.T, cmd *exec.Cmd) []byte {
	t.Helper()
	before, err := os.ReadFile(inv.stdoutPath)
	if err != nil {
		t.Fatalf("read %s: %v", inv.stdoutPath, err)
	}
	f, err := os.OpenFile(inv.stdoutPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open stdout %s: %v", inv.stdoutPath, err)
	}
	cmd.Stdout = f
	_ = cmd.Run()
	f.Close()
	after, err := os.ReadFile(inv.stdoutPath)
	if err != nil {
		t.Fatalf("read %s: %v", inv.stdoutPath, err)
	}
	if err := os.WriteFile(inv.stdoutPath, before, 0o644); err != nil {
		t.Fatalf("restore %s: %v", inv.stdoutPath, err)
	}
	if !bytes.HasPrefix(after, before) {
		t.Fatalf("%s was rewritten rather than appended to", inv.stdoutPath)
	}
	return after[len(before):]
}

// runFollow runs a case that follows a file: the steps fire as the
// output reaches each one's threshold, `limit` bytes are read, and the
// child is then terminated. The bytes read are the case's stdout.
// followPaths is every file a case's steps touch: each step's target,
// and a rename's source as well.
func (inv invocation) followPaths() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, st := range inv.follow {
		add(st.path)
		if st.act == "rename" {
			add(st.data)
		}
	}
	return out
}

// snapshotFollowFiles records the files the steps are about to change
// and gives back the restore. Both implementations have to meet the
// same starting state, and the steps rewrite it: without this the
// second side reads a file the first side already appended to,
// truncated, or renamed away.
func (inv invocation) snapshotFollowFiles(t *testing.T) func() {
	t.Helper()
	type shot struct {
		path    string
		data    []byte
		existed bool
	}
	var shots []shot
	for _, p := range inv.followPaths() {
		b, err := os.ReadFile(p)
		switch {
		case err == nil:
			shots = append(shots, shot{path: p, data: b, existed: true})
		case os.IsNotExist(err):
			shots = append(shots, shot{path: p})
		default:
			t.Fatalf("snapshot %s: %v", p, err)
		}
	}
	return func() {
		for _, s := range shots {
			if !s.existed {
				if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
					t.Fatalf("restore (remove) %s: %v", s.path, err)
				}
				continue
			}
			if err := os.WriteFile(s.path, s.data, 0o644); err != nil {
				t.Fatalf("restore %s: %v", s.path, err)
			}
		}
	}
}

// readUpTo reads at most limit bytes from r, calling onData with the running
// total after each chunk arrives, and gives up at followDeadline. The total
// is the caller's only honest view of how much has arrived — a callback that
// tracks its own accumulator cannot see what readUpTo has already buffered.
// The deadline is what keeps a child that has stopped writing without exiting
// — a `tail -f` with nothing left to say — from blocking the whole package
// instead of failing its own case.
func readUpTo(r *os.File, limit int, onData func(total int)) ([]byte, bool) {
	type piece struct {
		b   []byte
		err error
	}
	// Buffered so a read still in flight at the deadline can finish
	// its send and let the goroutine exit rather than stranding it.
	pieces := make(chan piece, 1)
	// want is how much is still owed, so a read never overshoots: the
	// two sides are compared byte for byte, and a chunk read past the
	// limit would make them differ on where an endless stream was cut.
	want := make(chan int, 1)
	want <- limit
	go func() {
		for n := range want {
			buf := make([]byte, n)
			got, rerr := r.Read(buf)
			pieces <- piece{buf[:got], rerr}
			if rerr != nil {
				return
			}
		}
	}()
	var out []byte
	deadline := time.After(followDeadline)
	for len(out) < limit {
		select {
		case p := <-pieces:
			out = append(out, p.b...)
			if onData != nil {
				onData(len(out))
			}
			if p.err != nil {
				close(want)
				return out, false
			}
			if len(out) < limit {
				want <- limit - len(out)
			} else {
				close(want)
			}
		case <-deadline:
			close(want)
			return out, true
		}
	}
	return out, false
}

func (inv invocation) runFollow(t *testing.T, cmd *exec.Cmd) []byte {
	t.Helper()
	if inv.limit <= 0 {
		t.Fatal("a follow case needs a limit: the byte count after which the child is terminated")
	}
	defer inv.snapshotFollowFiles(t)()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	cmd.Stdout = w
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", cmd.Path, err)
	}
	w.Close()
	step := 0
	fire := func(total int) {
		for step < len(inv.follow) && total >= inv.follow[step].after {
			inv.follow[step].run(t)
			step++
		}
	}
	fire(0)
	out, timedOut := readUpTo(r, inv.limit, fire)
	// EOF before the limit means the child is exiting itself. Wait for
	// that exit: sending SIGTERM here races its final stderr write and
	// exit status, as in tail's follow-by-name case after a rename.
	if timedOut || len(out) >= inv.limit {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	// Bound either kind of shutdown, including a child ignoring SIGTERM.
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case <-waited:
	case <-time.After(followDeadline):
		_ = cmd.Process.Kill()
		<-waited
	}
	r.Close()
	if timedOut {
		// Some cases intentionally observe a stream until the deadline,
		// including tail following a regular file beside a directory.
		t.Logf("follow case %q: %d of %d bytes arrived before the deadline", inv.name, len(out), inv.limit)
	}
	if step != len(inv.follow) {
		t.Errorf("follow case %q: %d of %d follow steps fired — a step that never ran is a case that tested nothing", inv.name, step, len(inv.follow))
	}
	if len(out) > inv.limit {
		out = out[:inv.limit]
	}
	return out
}

// requireParity runs every case against both implementations and
// reports each difference.
func requireParity(t *testing.T, util string, cases []invocation) {
	t.Helper()
	requireParityBinary(t, util, fernBin(t, util), cases)
}

func requireParityBinary(t *testing.T, util, ours string, cases []invocation) {
	t.Helper()
	ref := referenceBin(t, util)
	_, ver := gnuDir(t)
	t.Logf("reference: %s (%s)", ref, ver)

	for _, inv := range cases {
		t.Run(inv.name, func(t *testing.T) {
			inv.prep(t)
			want := inv.run(t, ref, util)
			wantFiles := inv.readArtifacts(t)
			inv.prep(t)
			got := inv.run(t, ours, util)
			diffArtifacts(t, util, inv, wantFiles, inv.readArtifacts(t), "gnu", "fern")
			if !bytes.Equal(want.stdout, got.stdout) {
				t.Errorf("stdout differs for %s %s\n gnu: %s\nfern: %s", util, quoteArgs(inv.args), quote(want.stdout), quote(got.stdout))
			}
			if !bytes.Equal(want.stderr, got.stderr) {
				t.Errorf("stderr differs for %s %s\n gnu: %s\nfern: %s", util, quoteArgs(inv.args), quote(want.stderr), quote(got.stderr))
			}
			if want.how() != got.how() {
				t.Errorf("status differs for %s %s: gnu %s, fern %s", util, quoteArgs(inv.args), want.how(), got.how())
			}
			if diff := treeDiff(want.tree, got.tree, "gnu", "fern"); diff != "" {
				t.Errorf("the files left behind differ for %s %s\n%s", util, quoteArgs(inv.args), diff)
			}
		})
	}
}

// treeKind names the entry kind in one word.
func treeKind(mode os.FileMode) string {
	switch {
	case mode.IsDir():
		return "dir"
	case mode&os.ModeSymlink != 0:
		return "symlink"
	case mode.IsRegular():
		return "file"
	case mode&os.ModeNamedPipe != 0:
		return "fifo"
	case mode&os.ModeSocket != 0:
		return "socket"
	case mode&os.ModeCharDevice != 0:
		return "chardev"
	case mode&os.ModeDevice != 0:
		return "blockdev"
	}
	return "other"
}

// permBits is the twelve-bit mode word `chmod` speaks, rebuilt out of Go's
// portable FileMode: the nine permission bits plus setuid / setgid / sticky.
func permBits(mode os.FileMode) uint32 {
	bits := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		bits |= 0o4000
	}
	if mode&os.ModeSetgid != 0 {
		bits |= 0o2000
	}
	if mode&os.ModeSticky != 0 {
		bits |= 0o1000
	}
	return bits
}

// linkGroup numbers the (dev, ino) equivalence classes in walk order, so two
// names sharing an inode share a number on both sides while the inodes
// themselves — which differ between the runs — never reach the comparison.
func linkGroup(info os.FileInfo, groups map[[2]uint64]int) int {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	key := [2]uint64{uint64(st.Dev), uint64(st.Ino)}
	if n, seen := groups[key]; seen {
		return n
	}
	n := len(groups) + 1
	groups[key] = n
	return n
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

// treeDiff reports the first few ways two trees differ, or "" when they
// agree. Naming the path and what differs is what makes a failure
// actionable: a `split` case that gets the suffix sequence wrong otherwise
// reports only that something changed. wantWho and gotWho name the two
// sides in the report.
func treeDiff(want, got []treeEntry, wantWho, gotWho string) string {
	byName := func(es []treeEntry) map[string]treeEntry {
		m := make(map[string]treeEntry, len(es))
		for _, e := range es {
			m[e.name] = e
		}
		return m
	}
	w, g := byName(want), byName(got)
	var names []string
	seen := map[string]bool{}
	for _, e := range append(append([]treeEntry{}, want...), got...) {
		if !seen[e.name] {
			seen[e.name] = true
			names = append(names, e.name)
		}
	}
	sort.Strings(names)
	var lines []string
	for _, n := range names {
		we, wok := w[n]
		ge, gok := g[n]
		switch {
		case wok && !gok:
			lines = append(lines, fmt.Sprintf("  %s: %s wrote it, %s did not", n, wantWho, gotWho))
		case !wok && gok:
			lines = append(lines, fmt.Sprintf("  %s: %s wrote it, %s did not", n, gotWho, wantWho))
		case we.kind != ge.kind:
			lines = append(lines, fmt.Sprintf("  %s: %s %s, %s %s", n, wantWho, we.kind, gotWho, ge.kind))
		case we.mode != ge.mode:
			lines = append(lines, fmt.Sprintf("  %s: %s mode %04o, %s mode %04o", n, wantWho, we.mode, gotWho, ge.mode))
		case we.rdev != ge.rdev:
			lines = append(lines, fmt.Sprintf("  %s: %s device %#x, %s device %#x", n, wantWho, we.rdev, gotWho, ge.rdev))
		case we.target != ge.target:
			lines = append(lines, fmt.Sprintf("  %s: %s -> %s, %s -> %s", n, wantWho, quote([]byte(we.target)), gotWho, quote([]byte(ge.target))))
		case we.content != ge.content:
			lines = append(lines, fmt.Sprintf("  %s: %s %s, %s %s", n, wantWho, quote([]byte(we.content)), gotWho, quote([]byte(ge.content))))
		case we.group != ge.group:
			lines = append(lines, fmt.Sprintf("  %s: %s hard-link group %d, %s hard-link group %d", n, wantWho, we.group, gotWho, ge.group))
		case we.owner != ge.owner:
			lines = append(lines, fmt.Sprintf("  %s: %s owner %s, %s owner %s", n, wantWho, we.owner, gotWho, ge.owner))
		}
		if len(lines) == 8 {
			lines = append(lines, "  … more")
			break
		}
	}
	return strings.Join(lines, "\n")
}
