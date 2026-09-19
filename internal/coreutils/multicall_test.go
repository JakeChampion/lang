package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The multicall leg: every utility in `coreutils/` compiled into the single
// binary `coreutils/multicall/fern-coreutils.fern` builds, run over the same
// corpus, and required to agree with the standalone build byte for byte.
//
// The comparison is against the NATIVE STANDALONE binary rather than GNU for
// the reason the self-host leg gives: standalone is already held to GNU by the
// gate above, so agreeing with it is agreeing with GNU, and a failure here says
// "one binary and 106 binaries disagree" rather than re-reporting a parity bug
// in both.
//
// No symlink farm is needed to exercise it. The harness already runs a utility
// with argv[0] set to the bare utility name, which is exactly what the
// dispatcher reads, so the multicall binary can stand in for a standalone one
// wherever a binary path is taken.

// catalogueRe matches one name in the dispatcher's `catalogue()` table.
var catalogueRe = regexp.MustCompile(`"([^"]+)"`)

// multicallSource is the dispatcher, read once and shared by the gates below.
func multicallSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "coreutils", "multicall", "fern-coreutils.fern")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(src)
}

// multicallCatalogue is the names `catalogue()` returns, in source order.
func multicallCatalogue(t *testing.T) []string {
	t.Helper()
	src := multicallSource(t)
	const open = "function catalogue(): string[] {"
	i := strings.Index(src, open)
	if i < 0 {
		t.Fatalf("no catalogue() in the dispatcher — this gate is reading the wrong shape")
	}
	body := src[i+len(open):]
	if j := strings.Index(body, "\n}"); j >= 0 {
		body = body[:j]
	}
	var names []string
	for _, m := range catalogueRe.FindAllStringSubmatch(body, -1) {
		names = append(names, m[1])
	}
	return names
}

// multicallImports is the utility each `import "../x" as u_x;` line names.
func multicallImports(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, m := range fernImportReTest.FindAllStringSubmatch(multicallSource(t), -1) {
		if strings.HasPrefix(m[1], "../") && !strings.Contains(strings.TrimPrefix(m[1], "../"), "/") {
			names = append(names, strings.TrimPrefix(m[1], "../"))
		}
	}
	return names
}

var fernImportReTest = regexp.MustCompile(`(?m)^\s*import\s+"([^"]+)"`)

// dispatchRe matches one arm of the dispatch chain in `main()`, and pairs the
// name tested against the module called so that a copy-paste mismatch —
// `if (n == "sum") { return u_shuf.main(); }` — is caught rather than counted.
var dispatchRe = regexp.MustCompile(`if \(n == "([^"]+)"\) \{\s*return (\w+)\.main\(\);\s*\}`)

// multicallDispatch is the utility each arm of `main()` dispatches to. An arm
// whose name and module disagree is reported here rather than returned.
func multicallDispatch(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, m := range dispatchRe.FindAllStringSubmatch(multicallSource(t), -1) {
		if want := "u_" + strings.NewReplacer("[", "bracket").Replace(m[1]); want != m[2] {
			t.Errorf("the dispatch arm for %q calls %s.main(), not %s.main()", m[1], m[2], want)
			continue
		}
		names = append(names, m[1])
	}
	return names
}

// TestMulticallCatalogue fails when a utility is missing from the dispatcher,
// or listed there without a source file behind it.
//
// The catalogue is checked in rather than generated, so that the file is
// reviewable; this is what stops it drifting. A new utility joins the
// multicall binary by being added in two places, and the message says both.
func TestMulticallCatalogue(t *testing.T) {
	want := utilNames(t)
	sort.Strings(want)

	for _, got := range []struct {
		what  string
		names []string
	}{
		{"catalogue()", multicallCatalogue(t)},
		{"the imports", multicallImports(t)},
		{"the dispatch chain in main()", multicallDispatch(t)},
	} {
		// A regex that matches nothing would otherwise pass every check
		// below by vacuity, which is the shape this gate exists to refuse.
		if len(got.names) == 0 {
			t.Fatalf("read no names out of %s — this gate is matching the wrong shape, not finding an empty list", got.what)
		}
		have := map[string]bool{}
		for _, n := range got.names {
			have[n] = true
		}
		for _, n := range want {
			if !have[n] {
				t.Errorf("coreutils/%s.fern is missing from %s in coreutils/multicall/fern-coreutils.fern — a utility reaches the multicall binary through an `import \"../%s\" as u_%s;` line, a %q entry in catalogue(), and an arm of the dispatch chain in main()", n, got.what, n, n, n)
			}
		}
		for _, n := range got.names {
			if _, err := os.Stat(filepath.Join(repoRoot(t), "coreutils", n+".fern")); err != nil {
				t.Errorf("%s names %q, but coreutils/%s.fern does not exist", got.what, n, n)
			}
		}
	}
}

var (
	multicallOnce sync.Once
	multicallPath string
	multicallFail string
)

// multicallBin compiles the dispatcher for the host, once per test process.
//
// Built without -O for the reason fernBin gives: the parity gate wants the
// assert() checks live.
func multicallBin(t *testing.T) string {
	t.Helper()
	multicallOnce.Do(func() {
		fern := e2eharness.BuildLangBinForInterp(t)
		dir, err := os.MkdirTemp("", "fern-coreutils-multicall-")
		if err != nil {
			multicallFail = err.Error()
			return
		}
		root := repoRoot(t)
		srcDir := filepath.Join(root, "coreutils", "multicall")
		// The compile is a child process, so the sources it reads reach the go
		// command's test cache only if this process reads them too (#9087).
		// The closure walk follows the `../` imports out into coreutils/, so
		// this covers all 106 utilities and the lib/ they share.
		e2eharness.TrackFernSources(t, srcDir, "fern-coreutils.fern")
		bin := filepath.Join(dir, "fern-coreutils")
		src := filepath.Join(srcDir, "fern-coreutils.fern")
		out, cerr := exec.Command(fern, "-target", fernTarget(t), "-o", bin, src).CombinedOutput()
		if cerr != nil {
			multicallFail = string(out)
			return
		}
		multicallPath = bin
	})
	if multicallPath == "" {
		t.Fatalf("build the multicall binary: %s", multicallFail)
	}
	return multicallPath
}

// TestMulticallDispatch covers what the corpus below cannot: the dispatcher
// answering for ITSELF, when argv[0] names no utility.
func TestMulticallDispatch(t *testing.T) {
	bin := multicallBin(t)
	names := utilNames(t)

	t.Run("list", func(t *testing.T) {
		out, err := runAs(t, bin, "fern-coreutils", "--list")
		if err != nil {
			t.Fatalf("--list: %v\n%s", err, out)
		}
		got := strings.Fields(string(out))
		if len(got) != len(names) {
			t.Fatalf("--list printed %d names, coreutils/ holds %d", len(got), len(names))
		}
		have := map[string]bool{}
		for _, n := range got {
			have[n] = true
		}
		for _, n := range names {
			if !have[n] {
				t.Errorf("--list does not print %q", n)
			}
		}
	})

	// A utility named as an argument cannot work (the binary reads argv[0]),
	// so it is refused with a line that says what to do instead rather than
	// being run with the wrong program name in its diagnostics.
	t.Run("utility as argument is refused", func(t *testing.T) {
		out, err := runAs(t, bin, "fern-coreutils", "yes")
		if err == nil {
			t.Fatalf("`fern-coreutils yes` succeeded; it must refuse\n%s", out)
		}
		if !strings.Contains(string(out), "symlink") {
			t.Errorf("the refusal does not say to use a symlink:\n%s", out)
		}
	})

	t.Run("unrecognized option", func(t *testing.T) {
		out, _ := runAs(t, bin, "fern-coreutils", "--bogus")
		if want := "unrecognized option '--bogus'"; !strings.Contains(string(out), want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	})

	// The symlink IS the supported invocation, so one is exercised end to end
	// rather than trusting that setting argv[0] is the same thing.
	t.Run("through a symlink", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "echo")
		if err := os.Symlink(bin, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		out, err := runAs(t, link, link, "hello", "world")
		if err != nil {
			t.Fatalf("echo through a symlink: %v\n%s", err, out)
		}
		if string(out) != "hello world\n" {
			t.Errorf("echo through a symlink printed %q", out)
		}
	})
}

// runAs runs bin with argv[0] set to argv0, the way the corpus harness does.
//
// Under the emulator that needs qemu's -0: the guest otherwise reads the
// emulator's own argv, whose argv[0] is the binary's path, so every case
// would dispatch on the dispatcher's name and exercise the own-name path
// instead of the utility it names.
func runAs(t *testing.T, bin, argv0 string, args ...string) ([]byte, error) {
	t.Helper()
	if pre := crossPrefix(); len(pre) > 0 {
		argv := append(append(append([]string{}, pre...), "-0", argv0, bin), args...)
		return exec.Command(argv[0], argv[1:]...).CombinedOutput()
	}
	cmd := exec.Command(bin, args...)
	cmd.Args = append([]string{argv0}, args...)
	return cmd.CombinedOutput()
}

// TestMulticallParity runs every utility's corpus against the multicall binary
// and its standalone build and requires the two to agree.
func TestMulticallParity(t *testing.T) {
	ours := multicallBin(t)
	for _, util := range utilNames(t) {
		cases, ok := corpusRegistry[util]
		if !ok {
			continue // reported by TestSelfHostCoreutilsCoverage
		}
		t.Run(util, func(t *testing.T) {
			standalone := fernBin(t, util)
			for _, inv := range cases(t) {
				t.Run(inv.name, func(t *testing.T) {
					// Parallel on the same terms the self-host leg states:
					// two processes per case, almost all of the cost in
					// spawning them, and a case that writes a corpus file
					// runs alone.
					if inv.stdoutPath == "" && len(inv.follow) == 0 &&
						inv.prepare == nil && len(inv.artifacts) == 0 {
						t.Parallel()
					}
					inv := inv.own(t)
					inv.prep(t)
					want := inv.run(t, standalone, util)
					wantFiles := inv.readArtifacts(t)
					inv.prep(t)
					got := inv.run(t, ours, util)
					diffArtifacts(t, util, inv, wantFiles, inv.readArtifacts(t), "standalone", "multicall")
					if !sameOutput(inv, want.stdout, got.stdout) {
						t.Errorf("stdout differs for %s %s\nstandalone: %s\n multicall: %s", util, quoteArgs(inv.args), quote(want.stdout), quote(got.stdout))
					}
					if !sameOutput(inv, want.stderr, got.stderr) {
						t.Errorf("stderr differs for %s %s\nstandalone: %s\n multicall: %s", util, quoteArgs(inv.args), quote(want.stderr), quote(got.stderr))
					}
					if want.how() != got.how() {
						t.Errorf("status differs for %s %s: standalone %s, multicall %s", util, quoteArgs(inv.args), want.how(), got.how())
					}
					if diff := treeDiff(want.tree, got.tree, "standalone", "multicall"); diff != "" {
						t.Errorf("the files left behind differ for %s %s\n%s", util, quoteArgs(inv.args), diff)
					}
				})
			}
		})
	}
}
