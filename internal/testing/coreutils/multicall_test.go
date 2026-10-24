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

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var catalogueRe = regexp.MustCompile(`"([^"]+)"`)

func multicallSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "coreutils", "multicall", "fern-coreutils.fern")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(src)
}

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

var dispatchRe = regexp.MustCompile(`if \(n == "([^"]+)"\) \{\s*return (\w+)\.main\(\);\s*\}`)

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

	t.Run("utility as argument", func(t *testing.T) {
		for _, argv := range [][]string{
			{"uname", "--bogus"},
			{"echo", "hello", "world"},
			{"basename", "/a/b/c"},
		} {
			want, wantErr := runAs(t, bin, argv[0], argv[1:]...)
			for _, self := range []string{"fern-coreutils", "/usr/local/bin/fern-coreutils"} {
				got, gotErr := runAs(t, bin, self, argv...)
				if string(got) != string(want) {
					t.Errorf("`%s %s` printed %q, `%s` printed %q", self, quoteArgs(argv), got, quoteArgs(argv), want)
				}
				if exitStatus(gotErr) != exitStatus(wantErr) {
					t.Errorf("`%s %s` exited %d, `%s` exited %d", self, quoteArgs(argv), exitStatus(gotErr), quoteArgs(argv), exitStatus(wantErr))
				}
			}
		}
		out, _ := runAs(t, bin, "fern-coreutils", "uname", "--bogus")
		if want := "uname: unrecognized option '--bogus'"; !strings.Contains(string(out), want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	})

	t.Run("unrecognized option", func(t *testing.T) {
		out, _ := runAs(t, bin, "fern-coreutils", "--bogus")
		if want := "unrecognized option '--bogus'"; !strings.Contains(string(out), want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	})

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

func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

func TestMulticallParity(t *testing.T) {
	ours := multicallBin(t)
	for _, util := range utilNames(t) {
		cases, ok := corpusRegistry[util]
		if !ok {
			continue
		}
		t.Run(util, func(t *testing.T) {
			standalone := fernBin(t, util)
			for _, inv := range cases(t) {
				t.Run(inv.name, func(t *testing.T) {
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
