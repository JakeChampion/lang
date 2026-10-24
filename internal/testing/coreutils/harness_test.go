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

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

type invocation struct {
	name                  string
	args                  []string
	stdin                 string
	timeout               time.Duration
	stdinPath             string
	stdoutPath            string
	stdoutFile            string
	follow                []followStep
	env                   []string
	limit                 int
	stdout                stdoutMode
	tty                   bool
	ttyIn                 bool
	ttyOut                bool
	ttyErr                bool
	ttyPre                []string
	ttyState              bool
	sigint                bool
	dir                   string
	prepare               func(t *testing.T)
	artifacts             []string
	mask                  func(string) string
	stderrMask            func(string) string
	seedTree              func(t *testing.T, dir string)
	rootRename            bool
	umask                 *int
	withoutClockPrivilege bool
	ownership             bool
	context               bool
	sparse                bool
	merged                bool
	unordered             bool
	crossDev              func(t *testing.T, dir string)
	rawByteName           bool
	darwinLibcDefect      string
}

type treeEntry struct {
	name    string
	kind    string
	mode    uint32
	target  string
	content string
	selinux string
	owner   string
	rdev    uint64
	blocks  int64
	group   int
}

type followStep struct {
	after int
	act   string
	path  string
	data  string
}

const followDeadline = 8 * time.Second

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
	stdoutCaptured stdoutMode = iota
	stdoutClosed
	stdoutFull
)

type outcome struct {
	stdout   []byte
	stderr   []byte
	exit     int
	signal   string
	overran  bool
	flooded  bool
	tree     []treeEntry
	ttyState string
}

func (o outcome) how() string {
	if o.flooded {
		return "wrote past the capture cap"
	}
	if o.overran {
		return "did not finish"
	}
	if o.signal != "" {
		return "killed by " + o.signal
	}
	return fmt.Sprintf("exit %d", o.exit)
}

func rootThroughChild(t *testing.T) string {
	t.Helper()
	const spelling = "/usr/.."
	got, err := filepath.EvalSymlinks(spelling)
	if err != nil {
		t.Fatalf("resolving %s: %v", spelling, err)
	}
	if got != "/" {
		t.Fatalf("%s resolves to %q, not the root: a --preserve-root case built on it "+
			"would recursively chmod that directory instead of being refused", spelling, got)
	}
	return spelling
}

const captureCap = 64 << 20

type cappedBuffer struct {
	buf  bytes.Buffer
	full bool
	kill func()
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.full {
		return len(p), nil
	}
	if room := captureCap - c.buf.Len(); len(p) > room {
		c.buf.Write(p[:room])
		c.full = true
		if c.kill != nil {
			c.kill()
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) Bytes() []byte { return c.buf.Bytes() }

type artifact struct {
	name    string
	present bool
	data    []byte
}

func (inv invocation) own(t *testing.T) invocation {
	t.Helper()
	if inv.dir == "" && inv.seedTree == nil {
		defer holdMask(nil)()
		inv.dir = t.TempDir()
	}
	return inv
}

func (inv invocation) prep(t *testing.T) {
	t.Helper()
	if inv.rawByteName && !rawByteNamesHeld(t) {
		t.Skipf("the fixture name is not valid UTF-8 and this filesystem refuses one (%s validates it)", runtime.GOOS)
	}
	if inv.darwinLibcDefect != "" && runtime.GOOS == "darwin" {
		t.Skipf("the Darwin reference is wrong here: %s", inv.darwinLibcDefect)
	}
	if inv.prepare == nil {
		return
	}
	defer holdMask(nil)()
	inv.prepare(t)
}

func (inv invocation) readArtifacts(t *testing.T) []artifact {
	t.Helper()
	out := make([]artifact, 0, len(inv.artifacts))
	for _, name := range inv.artifacts {
		b, err := os.ReadFile(childPath(name, inv.dir))
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

func diffArtifacts(t *testing.T, util string, inv invocation, want, got []artifact, wantWho, gotWho string) {
	t.Helper()
	for i := range want {
		w, g := want[i], got[i]
		if w.present != g.present {
			t.Errorf("%s %s: %s %s %s, %s %s it", util, quoteArgs(inv.args), wantWho, presence(w.present), w.name, gotWho, presence(g.present))
			continue
		}
		if w.present && !bytes.Equal(w.data, g.data) {
			t.Errorf("%s %s: %s differs%s", util, quoteArgs(inv.args), w.name,
				diffBody(wantWho, gotWho, w.data, g.data))
		}
	}
}

func presence(ok bool) string {
	if ok {
		return "wrote"
	}
	return "left no"
}

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

  FERN_GNU_COREUTILS=/path/to/coreutils/bin go test ./internal/testing/coreutils/
  apt-get install coreutils           (Debian/Ubuntu: already the system default)
  nix-shell -p coreutils              (macOS: the system tools are BSD, not GNU)

Searched: %s`, gnuDirErr, strings.Join(gnuCandidates(), ", "))
	}
	return gnuDirPath, gnuDirVer
}

func gnuCandidates() []string {
	var dirs []string
	if d := os.Getenv("FERN_GNU_COREUTILS"); d != "" {
		dirs = append(dirs, filepath.SplitList(d)...)
	}
	if p, err := exec.LookPath("yes"); err == nil {
		dirs = append(dirs, filepath.Dir(p))
	}
	dirs = append(dirs, "/usr/bin", "/bin", "/usr/local/bin", "/opt/homebrew/opt/coreutils/libexec/gnubin")
	if matches, err := filepath.Glob("/nix/store/*-coreutils-*/bin"); err == nil {
		dirs = append(dirs, matches...)
	}
	return dirs
}

const (
	gnuProbeBytes   = 4096
	gnuProbeTimeout = 10 * time.Second
)

func gnuVersion(dir string) (string, error) {
	return gnuVersionOf(filepath.Join(dir, "yes"))
}

func gnuVersionOf(bin string) (string, error) {
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
	if !strings.Contains(first, "(GNU coreutils)") && !strings.Contains(first, "(coreutils)") {
		if len(out) == 0 && readErr != nil {
			return "", fmt.Errorf("%s: %w", bin, readErr)
		}
		return "", fmt.Errorf("%s is not GNU coreutils: %q", bin, first)
	}
	return strings.TrimSpace(first), nil
}

func referenceBin(t *testing.T, util string) string {
	t.Helper()
	dir, ver := gnuDir(t)
	refBinsMu.Lock()
	defer refBinsMu.Unlock()
	if bin, ok := refBins[util]; ok {
		return bin
	}
	var tried []string
	for _, cand := range append([]string{dir}, gnuCandidates()...) {
		bin := filepath.Join(cand, util)
		if _, err := os.Stat(bin); err != nil {
			continue
		}
		probe := bin
		if util == "test" {
			probe = filepath.Join(cand, "[")
		}
		if _, err := gnuVersionOf(probe); err != nil {
			tried = append(tried, fmt.Sprintf("%s (%v)", probe, err))
			continue
		}
		refBins[util] = bin
		return bin
	}
	t.Fatalf(`no GNU coreutils %[1]s on this host (the corpus reference is %[2]s from %[3]s).

Rejected: %[4]s

A utility whose oracle is missing cannot be gated, and a SKIP here would
report a green suite that compared nothing. Debian and Ubuntu do not build
%[1]s into their coreutils package; add a directory holding one to
FERN_GNU_COREUTILS, which takes a PATH-style list.`,
		util, ver, dir, strings.Join(tried, ", "))
	return ""
}

var (
	refBinsMu sync.Mutex
	refBins   = map[string]string{}
)

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

func repoRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(self))))
}

func crossPrefix() []string {
	return strings.Fields(os.Getenv("FERN_COREUTILS_QEMU"))
}

func crossArgv(bin string, args ...string) []string {
	return append(append(crossPrefix(), bin), args...)
}

const crossDevName = "xdev"

func crossDevDir(t *testing.T, near string) string {
	t.Helper()
	base := os.Getenv("FERN_COREUTILS_XDEV")
	named := base != ""
	if !named {
		base = "/dev/shm"
	}
	if !named {
		if _, err := os.Stat(base); err != nil {
			t.Skipf(`no second filesystem to reach EXDEV: %s does not exist on %s.
Name one with FERN_COREUTILS_XDEV=/path (a tmpfs mount on Linux; on Darwin,
`+"`hdiutil attach -nomount ram://…`"+` and a mounted volume).`, base, runtime.GOOS)
		}
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

func rootRenameSafety(rootMount, destinationMount uint64) error {
	if rootMount == 0 || destinationMount == 0 || rootMount != destinationMount {
		return errors.New("unsafe root-rename fixture: destination is not verified on /'s mount; set TMPDIR to a writable directory on the root mount and run in a disposable environment (GNU's EXDEV fallback can copy the live root, even across same-device bind mounts)")
	}
	return nil
}

func withMask(mask int) *int {
	return &mask
}

var maskLock sync.RWMutex

func holdMask(mask *int) func() {
	if mask == nil {
		maskLock.RLock()
		return maskLock.RUnlock
	}
	maskLock.Lock()
	return maskLock.Unlock
}

func applyMask(mask *int) func() {
	if mask == nil {
		return func() {}
	}
	previous := syscall.Umask(*mask)
	return func() { syscall.Umask(previous) }
}

func (inv invocation) run(t *testing.T, bin, argv0 string) outcome {
	t.Helper()
	defer holdMask(inv.umask)()
	cmd := exec.Command(bin)
	cmd.Path = bin
	cmd.Args = append([]string{argv0}, inv.args...)
	if pre := crossPrefix(); len(pre) > 0 {
		emu, err := exec.LookPath(pre[0])
		if err != nil {
			t.Fatalf("FERN_COREUTILS_QEMU names %s: %v", pre[0], err)
		}
		cmd.Path = emu
		cmd.Args = append(append(append([]string{pre[0]}, pre[1:]...), "-0", argv0, bin), inv.args...)
	}
	cmd.Env = append(baseEnv(), inv.env...)
	if inv.withoutClockPrivilege {
		e2eharness.WithoutClockPrivilege(t, cmd)
	}
	var workDir, crossRoot string
	var seeded map[string]bool
	if inv.seedTree != nil {
		workDir = t.TempDir()
		if inv.rootRename && runtime.GOOS == "linux" {
			if err := rootRenameSafety(linuxMountOf(t, "/"), linuxMountOf(t, workDir)); err != nil {
				t.Fatal(err)
			}
		}
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
			for _, e := range readTree(t, workDir, treeOptsOf(inv)) {
				seeded[e.name] = true
			}
		}
	} else {
		cmd.Dir = inv.dir
	}
	defer applyMask(inv.umask)()
	cmd.Stdin = strings.NewReader(inv.stdin)
	if inv.ttyIn && inv.stdinPath != "" {
		t.Fatalf("%s %s: ttyIn and stdinPath both claim fd 0", bin, quoteArgs(inv.args))
	}
	if (inv.ttyState || len(inv.ttyPre) > 0) && !inv.ttyIn {
		t.Fatalf("%s %s: ttyState and ttyPre are about fd 0's terminal, which needs ttyIn", bin, quoteArgs(inv.args))
	}
	if inv.ttyOut && (inv.stdout != stdoutCaptured || inv.stdoutPath != "" || inv.stdoutFile != "" || len(inv.follow) > 0 || inv.limit > 0 || inv.merged) {
		t.Fatalf("%s %s: ttyOut and another fd 1 mode both claim it", bin, quoteArgs(inv.args))
	}
	if inv.ttyErr && inv.merged {
		t.Fatalf("%s %s: ttyErr gives fd 2 its own terminal, which merged cannot share with fd 1", bin, quoteArgs(inv.args))
	}
	if (inv.ttyIn || inv.ttyErr) && (len(inv.follow) > 0 || inv.limit > 0 || inv.stdoutPath != "" || inv.stdoutFile != "") {
		t.Fatalf("%s %s: a terminal and the fd 1 paths that read a file back are not combined", bin, quoteArgs(inv.args))
	}
	var ptySlaves []*os.File
	var ptyOut, ptyErr *ptyReader
	var outSlave, errSlave *os.File
	closeSlaves := func() {
		for _, f := range ptySlaves {
			f.Close()
		}
		ptySlaves = nil
	}
	defer closeSlaves()
	defer func() {
		for _, f := range []*os.File{outSlave, errSlave} {
			if f != nil {
				f.Close()
			}
		}
	}()
	var ptyState *os.File
	if inv.ttyIn {
		master, slave := openPty(t)
		stopEcho := discardPtyEcho(master)
		defer func() {
			slave.Close()
			stopEcho()
		}()
		if inv.ttyState {
			ptyState = slave
			defer slave.Close()
		} else {
			ptySlaves = append(ptySlaves, slave)
		}
		cmd.Stdin = slave
		if len(inv.ttyPre) > 0 {
			ptyPrepare(t, referenceBin(t, argv0), argv0, slave, inv.ttyPre)
		}
		feedPty(master, inv.stdin)
	}
	if inv.ttyOut {
		master, slave := openOutputPty(t)
		defer master.Close()
		outSlave = slave
		cmd.Stdout = slave
		ptyOut = drainPty(master)
	}
	if inv.ttyErr {
		master, slave := openOutputPty(t)
		defer master.Close()
		errSlave = slave
		cmd.Stderr = slave
		ptyErr = drainPty(master)
	}
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

	killChild := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	errBuf := cappedBuffer{kill: killChild}
	if !inv.ttyErr {
		cmd.Stderr = &errBuf
	}
	if inv.merged && (inv.stdout != stdoutCaptured || inv.stdoutPath != "" || inv.stdoutFile != "" || len(inv.follow) > 0 || inv.limit > 0) {
		t.Fatalf("%s %s: merged needs fd 1 captured into the default buffer, which this case sends elsewhere", bin, quoteArgs(inv.args))
	}

	var out []byte
	var overran bool
	flooded := false
	switch inv.stdout {
	case stdoutCaptured:
		if inv.stdoutPath != "" {
			out, overran = inv.runToFile(t, cmd, cmd.Dir)
		} else if inv.stdoutFile != "" {
			out, overran = inv.runToRegularFile(t, cmd, cmd.Dir)
		} else if len(inv.follow) > 0 {
			out = inv.runFollow(t, cmd)
		}
	case stdoutClosed:
		cmd.Stdout = (*os.File)(nil)
		overran = inv.runBounded(cmd, closeSlaves)
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
		overran = inv.runBounded(cmd, closeSlaves)
	}
	if inv.stdout != stdoutCaptured || inv.stdoutPath != "" || inv.stdoutFile != "" || len(inv.follow) > 0 {
	} else if inv.limit > 0 {
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
			time.Sleep(sigintSettle)
		}
		r.Close()
		_ = cmd.Wait()
	} else {
		outBuf := cappedBuffer{kill: killChild}
		if !inv.ttyOut {
			cmd.Stdout = &outBuf
			if inv.merged {
				cmd.Stderr = &outBuf
			}
		}
		overran = inv.runBounded(cmd, closeSlaves)
		if inv.ttyOut {
			out = ptyOut.finish(outSlave)
			outSlave = nil
		} else {
			out = outBuf.Bytes()
		}
		flooded = flooded || outBuf.full
	}

	if cmd.ProcessState == nil {
		t.Fatalf("%s %s never ran: the invocation is not one exec can deliver", bin, quoteArgs(inv.args))
	}
	stderr := errBuf.Bytes()
	if inv.ttyErr {
		stderr = ptyErr.finish(errSlave)
		errSlave = nil
	}
	res := outcome{
		stdout:  out,
		stderr:  stderr,
		exit:    cmd.ProcessState.ExitCode(),
		overran: overran,
		flooded: flooded || errBuf.full,
	}
	if inv.ttyState {
		res.ttyState = ptySettings(t, ptyState)
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		res.signal = ws.Signal().String()
	}
	if workDir != "" {
		groups := map[[2]uint64]int{}
		res.tree = readTreeInto(t, workDir, "", groups, treeOptsOf(inv))
		if crossRoot != "" {
			kept := res.tree[:0]
			for _, e := range res.tree {
				if e.name != crossDevName {
					kept = append(kept, e)
				}
			}
			res.tree = append(kept, readTreeInto(t, crossRoot, crossDevName+"/", groups, treeOptsOf(inv))...)
			sort.Slice(res.tree, func(i, j int) bool { return res.tree[i].name < res.tree[j].name })
		}
	}
	if inv.stderrMask != nil {
		res.stderr = []byte(inv.stderrMask(string(res.stderr)))
	}
	if inv.mask != nil {
		res.stdout = []byte(inv.mask(string(res.stdout)))
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

func ownerOf(t *testing.T, path string, info os.FileInfo) string {
	t.Helper()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("no stat for %s — the ownership comparison cannot run on this platform", path)
	}
	return fmt.Sprintf("%d:%d", st.Uid, st.Gid)
}

type treeOpts struct {
	ownership bool
	sparse    bool
	context   bool
}

func treeOptsOf(inv invocation) treeOpts {
	return treeOpts{ownership: inv.ownership, sparse: inv.sparse, context: inv.context}
}

func readTree(t *testing.T, root string, opts treeOpts) []treeEntry {
	t.Helper()
	return readTreeInto(t, root, "", map[[2]uint64]int{}, opts)
}

func openTreeForCleanup(root string) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			_ = os.Chmod(path, 0o700)
			return nil
		}
		if info.Mode()&os.ModeSymlink == 0 {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.Mode()&os.ModeSymlink == 0 {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
}

func openTreeForWalk(t *testing.T, root string) map[string]uint32 {
	t.Helper()
	opened := map[string]uint32{}
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

func readTreeInto(t *testing.T, root, prefix string, groups map[[2]uint64]int, opts treeOpts) []treeEntry {
	t.Helper()
	opened := openTreeForWalk(t, root)
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("read the tree under %s: %v", root, err)
	}
	defer r.Close()
	var out []treeEntry
	var walk func(dir string) error
	walk = func(dir string) error {
		d, err := r.Open(dir)
		if err != nil {
			return err
		}
		names, err := d.Readdirnames(-1)
		d.Close()
		if err != nil {
			return err
		}
		sort.Strings(names)
		for _, name := range names {
			rel := filepath.Join(dir, name)
			path := filepath.Join(root, rel)
			info, err := r.Lstat(rel)
			if err != nil {
				return err
			}
			mode := info.Mode()
			e := treeEntry{name: prefix + rel, kind: treeKind(mode), mode: permBits(mode), blocks: -1}
			if opts.ownership {
				e.owner = ownerOf(t, path, info)
			}
			if opts.context {
				e.selinux = entryContext(path)
			}
			if was, ok := opened[path]; ok {
				e.mode = was
			}
			switch {
			case mode&os.ModeSymlink != 0:
				target, err := r.Readlink(rel)
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
				if opts.sparse {
					if st, ok := info.Sys().(*syscall.Stat_t); ok {
						e.blocks = st.Blocks
					}
				}
				if info.Size() > 1<<22 {
					e.content = fmt.Sprintf("<%d bytes>", info.Size())
				} else {
					b, err := r.ReadFile(rel)
					if err != nil {
						return err
					}
					e.content = string(b)
				}
			}
			out = append(out, e)
			if info.IsDir() {
				if err := walk(rel); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk("."); err != nil {
		t.Fatalf("read the tree under %s: %v", root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

const corpusRunLimit = 5 * time.Minute

func (inv invocation) bound() time.Duration {
	if inv.timeout > 0 {
		return inv.timeout
	}
	return corpusRunLimit
}

func (inv invocation) runBounded(cmd *exec.Cmd, afterStart func()) bool {
	if err := cmd.Start(); err != nil {
		afterStart()
		return false
	}
	afterStart()
	fired := make(chan struct{})
	timer := time.AfterFunc(inv.bound(), func() {
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

func (inv invocation) runToFile(t *testing.T, cmd *exec.Cmd, workDir string) ([]byte, bool) {
	t.Helper()
	path := childPath(inv.stdoutPath, workDir)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open stdout %s: %v", path, err)
	}
	cmd.Stdout = f
	overran := inv.runBounded(cmd, func() {})
	f.Close()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatalf("restore %s: %v", path, err)
	}
	if !bytes.HasPrefix(after, before) {
		t.Fatalf("%s was rewritten rather than appended to", path)
	}
	return after[len(before):], overran
}

func childPath(name, workDir string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(workDir, name)
}

func (inv invocation) runToRegularFile(t *testing.T, cmd *exec.Cmd, workDir string) ([]byte, bool) {
	t.Helper()
	path := childPath(inv.stdoutFile, workDir)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open stdout %s: %v", path, err)
	}
	cmd.Stdout = f
	overran := inv.runBounded(cmd, func() {})
	f.Close()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatalf("restore %s: %v", path, err)
	}
	return after, overran
}

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

func readUpTo(r *os.File, limit int, onData func(total int)) ([]byte, bool) {
	type piece struct {
		b   []byte
		err error
	}
	pieces := make(chan piece, 1)
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
	if timedOut || len(out) >= inv.limit {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
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

func requireParity(t *testing.T, util string, cases []invocation) {
	t.Helper()
	requireParityBinary(t, util, fernBin(t, util), cases)
}

const rawByteNameFixture = "na\xffme"

var rawByteNamesOnce struct {
	sync.Once
	ok bool
}

func rawByteNamesHeld(t *testing.T) bool {
	t.Helper()
	rawByteNamesOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rawbyte")
		if err != nil {
			return
		}
		defer os.RemoveAll(dir)
		if err := os.WriteFile(filepath.Join(dir, rawByteNameFixture), nil, 0o600); err != nil {
			return
		}
		rawByteNamesOnce.ok = true
	})
	return rawByteNamesOnce.ok
}

func rawByteCase(inv invocation) invocation {
	inv.rawByteName = true
	return inv
}

func seedRawByteName(t *testing.T, dir, content string) bool {
	t.Helper()
	if !rawByteNamesHeld(t) {
		return false
	}
	if err := os.WriteFile(filepath.Join(dir, rawByteNameFixture), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return true
}

func rawByteFile(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, rawByteNameFixture)
	if !rawByteNamesHeld(t) {
		return p
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func requireParityBinary(t *testing.T, util, ours string, cases []invocation) {
	t.Helper()
	ref := referenceBin(t, util)
	_, ver := gnuDir(t)
	t.Logf("reference: %s (%s)", ref, ver)

	for _, inv := range cases {
		t.Run(inv.name, func(t *testing.T) {
			inv := inv.own(t)
			inv.prep(t)
			want := inv.run(t, ref, util)
			wantFiles := inv.readArtifacts(t)
			inv.prep(t)
			got := inv.run(t, ours, util)
			if want.flooded || got.flooded {
				t.Logf("output past %d bytes was not compared (gnu flooded=%v, fern flooded=%v)",
					captureCap, want.flooded, got.flooded)
			}
			diffArtifacts(t, util, inv, wantFiles, inv.readArtifacts(t), "gnu", "fern")
			if !sameOutput(inv, want.stdout, got.stdout) {
				t.Errorf("stdout differs for %s %s%s", util, quoteArgs(inv.args), diffBody("gnu", "fern", want.stdout, got.stdout))
			}
			if !sameOutput(inv, want.stderr, got.stderr) {
				t.Errorf("stderr differs for %s %s%s", util, quoteArgs(inv.args), diffBody("gnu", "fern", want.stderr, got.stderr))
			}
			if want.how() != got.how() {
				t.Errorf("status differs for %s %s: gnu %s, fern %s", util, quoteArgs(inv.args), want.how(), got.how())
			}
			if diff := treeDiff(want.tree, got.tree, "gnu", "fern"); diff != "" {
				t.Errorf("the files left behind differ for %s %s\n%s", util, quoteArgs(inv.args), diff)
			}
			if want.ttyState != got.ttyState {
				t.Errorf("the terminal settings left behind differ for %s %s\n gnu: %s\nfern: %s",
					util, quoteArgs(inv.args), want.ttyState, got.ttyState)
			}
		})
	}
}

func sameOutput(inv invocation, want, got []byte) bool {
	if !inv.unordered {
		return bytes.Equal(want, got)
	}
	return bytes.Equal(sortedLines(want), sortedLines(got))
}

func sortedLines(b []byte) []byte {
	parts := strings.Split(string(b), "\n")
	tail := parts[len(parts)-1]
	lines := parts[:len(parts)-1]
	sort.Strings(lines)
	return []byte(strings.Join(append(lines, tail), "\n"))
}

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

const (
	diffFull    = 512
	diffContext = 96
)

func diffBody(leftName, rightName string, left, right []byte) string {
	width := len(leftName)
	if len(rightName) > width {
		width = len(rightName)
	}
	var head, l, r string
	if len(left) > diffFull || len(right) > diffFull {
		at := firstDiff(left, right)
		head = fmt.Sprintf("\n%*s  %d and %d bytes, first differing at byte %d",
			width, "", len(left), len(right), at)
		l, r = excerptAt(left, at), excerptAt(right, at)
	} else {
		l, r = quote(left), quote(right)
	}
	return fmt.Sprintf("%s\n%*s: %s\n%*s: %s", head, width, leftName, l, width, rightName, r)
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func excerptAt(b []byte, at int) string {
	lo, hi := at-diffContext, at+diffContext
	if lo < 0 {
		lo = 0
	}
	if hi > len(b) {
		hi = len(b)
	}
	s := quote(b[lo:hi])
	if lo > 0 {
		s = fmt.Sprintf("[%d bytes before] %s", lo, s)
	}
	if hi < len(b) {
		s = fmt.Sprintf("%s [%d bytes after]", s, len(b)-hi)
	}
	return s
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
		t.Errorf("%s %s: stderr must be empty on both sides%s", util, quoteArgs(args), diffBody("gnu", "fern", want.stderr, got.stderr))
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

func requireVersion(t *testing.T, util string, args []string, wantExit int) {
	t.Helper()
	requireHelpVersion(t, util, args, wantExit, util+" (Fern coreutils) ")
}

func requireHelp(t *testing.T, util string, args []string, wantExit int) {
	t.Helper()
	requireHelpVersion(t, util, args, wantExit, "Usage: "+util)
}

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
		case we.selinux != ge.selinux:
			lines = append(lines, fmt.Sprintf("  %s: %s context %s, %s context %s", n, wantWho, quote([]byte(we.selinux)), gotWho, quote([]byte(ge.selinux))))
		case we.blocks != ge.blocks:
			lines = append(lines, fmt.Sprintf("  %s: %s %d blocks, %s %d blocks", n, wantWho, we.blocks, gotWho, ge.blocks))
		}
		if len(lines) == 8 {
			lines = append(lines, "  … more")
			break
		}
	}
	return strings.Join(lines, "\n")
}
