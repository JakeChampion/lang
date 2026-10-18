package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// runHelp runs the CLI and returns its stdout, stderr and exit status.
func runHelp(t *testing.T, bin string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = e2eharness.ChildEnv(env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("fern %s did not exit normally\n%s", strings.Join(args, " "), stderr.String())
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

// -h and -help, with one dash or two, print the help to stdout and succeed,
// wherever they come among the driver flags; no arguments at all prints the
// same help to stderr and exits 2.
func TestSelfHostCLIHelp(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	for _, args := range [][]string{{"-h"}, {"-help"}, {"--help"}, {"-check", "-target", "x86-64-linux", "-h"}} {
		stdout, stderr, code := runHelp(t, cli, nil, args...)
		if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "usage: fern [FLAG...] FILE.fern [STDLIB [--] [ARG...]]\n") {
			t.Errorf("fern %s: exit %d, stderr %q, stdout:\n%s", strings.Join(args, " "), code, stderr, stdout)
		}
	}
	help, _, _ := runHelp(t, cli, nil, "-h")
	stdout, stderr, code := runHelp(t, cli, nil)
	if code != 2 || stdout != "" || stderr != help {
		t.Errorf("no arguments: exit %d, stdout %q; want exit 2 and the help on stderr, got:\n%s", code, stdout, stderr)
	}
}

// usageRowRe is one flag row of the help: two spaces, the flag's spellings
// and value, then its description after a run of spaces.
var usageRowRe = regexp.MustCompile(`^  (-\S.*?)\s{2,}\S`)

// usageFlags is every flag the help lists, mapped to the value placeholder it
// is written with ("" for none).
func usageFlags(t *testing.T, help string) map[string]string {
	t.Helper()
	flags := map[string]string{}
	for _, line := range strings.Split(help, "\n") {
		m := usageRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, spelling := range strings.Split(m[1], ", ") {
			name, value := spelling, ""
			if i := strings.IndexAny(spelling, " ["); i >= 0 {
				name, value = spelling[:i], strings.TrimSpace(spelling[i:])
			}
			if _, dup := flags[name]; dup {
				t.Errorf("the help lists %s twice", name)
			}
			flags[name] = value
		}
	}
	if len(flags) == 0 {
		t.Fatalf("no flag rows in the help:\n%s", help)
	}
	return flags
}

// parserFlagRe is one case of the dispatch over a flag read_flag has parsed.
var parserFlagRe = regexp.MustCompile(`\bf\.name == "(-[^"]+)"`)

// parserFlags is every flag name compiler/fern.fern's dispatch has a case
// for, read from the functions that do the dispatching.
func parserFlags(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(e2eharness.RepoPath("compiler", "fern.fern"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, fn := range []string{"pub function run_with(", "function tool_flag(", "function parse_tool_flag("} {
		start := strings.Index(string(src), "\n"+fn)
		if start < 0 {
			t.Fatalf("compiler/fern.fern has no %s...", fn)
		}
		body := string(src[start:])
		body = body[:strings.Index(body, "\n}\n")]
		for _, m := range parserFlagRe.FindAllStringSubmatch(body, -1) {
			seen[m[1]] = true
		}
	}
	var out []string
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// The help lists exactly the flags the parser accepts. Every flag the
// dispatch has a case for has a row, every row has a case, and every row's
// flag is accepted: a probe passes it (with a value where it takes one)
// followed by an unknown flag, so the refusal naming that unknown flag shows
// the listed one parsed.
func TestSelfHostCLIUsageListsEveryFlag(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	help, _, _ := runHelp(t, cli, nil, "-h")
	listed := usageFlags(t, help)
	dispatched := map[string]bool{}
	for _, f := range parserFlags(t) {
		dispatched[f] = true
		if _, ok := listed[f]; !ok {
			t.Errorf("the dispatch has a case for %s but the help does not list it", f)
		}
	}
	for _, line := range strings.Split(help, "\n") {
		m := usageRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := strings.Split(m[1], ", ")[0]
		if i := strings.IndexAny(name, " ["); i >= 0 {
			name = name[:i]
		}
		if !dispatched[name] {
			t.Errorf("the help lists %s but the dispatch has no case for it", name)
		}
	}
	const sentinel = "-no-such-flag-sentinel"
	for name, value := range listed {
		if name == "-h" || name == "-help" {
			continue
		}
		args := []string{name}
		switch {
		case name == "-color":
			args = append(args, "auto")
		case value != "" && !strings.HasPrefix(value, "["):
			args = append(args, "x")
		}
		args = append(args, sentinel)
		_, stderr, code := runHelp(t, cli, nil, args...)
		if code != 2 || stderr != "fern: unknown flag: "+sentinel+"\n" {
			t.Errorf("fern %s: exit %d, stderr:\n%s\nwant the sentinel refused, showing %s parsed", strings.Join(args, " "), code, stderr, name)
		}
	}
}

// A build carrying its stdlib takes no stdlib root, so neither its help nor
// its hint for a flag after the source file names one; a build without it
// names one in both.
func TestSelfHostShippedFernHelpHasNoStdlibPositional(t *testing.T) {
	shipped := e2eharness.ShippedFern(t)
	dev := e2eharness.SelfHostCLI(t)
	cache := filepath.Join(t.TempDir(), "cache")
	prog := writeShippedSrc(t, t.TempDir(), "args.fern", shippedArgsSrc)

	help, stderr, code := runHelp(t, shipped, []string{"FERN_CACHE_DIR=" + cache}, "--help")
	if code != 0 || stderr != "" || !strings.HasPrefix(help, "usage: fern [FLAG...] FILE.fern [--] [ARG...]\n") || strings.Contains(help, "STDLIB") {
		t.Errorf("shipped --help: exit %d, stderr %q, stdout:\n%s", code, stderr, help)
	}
	devHelp, _, _ := runHelp(t, dev, nil, "--help")
	if !strings.Contains(devHelp, "STDLIB") {
		t.Errorf("the help of a build without an embedded stdlib does not name the stdlib root:\n%s", devHelp)
	}
	// The flag rows are the same in both builds; only the synopsis differs.
	if a, b := usageFlags(t, help), usageFlags(t, devHelp); len(a) != len(b) {
		t.Errorf("the shipped help lists %d flags, the other build %d", len(a), len(b))
	}

	for _, c := range []struct {
		bin, want string
		env       []string
	}{
		{shipped, "    fern FILE.fern -- -x\n", []string{"FERN_CACHE_DIR=" + cache}},
		{dev, "    fern FILE.fern STDLIB -- -x\n", nil},
	} {
		_, stderr, code := runHelp(t, c.bin, c.env, "-interp", prog, "-x")
		if code != 2 || !strings.HasSuffix(stderr, c.want) {
			t.Errorf("%s -interp FILE -x: exit %d, stderr:\n%s\nwant a hint ending %q", c.bin, code, stderr, c.want)
		}
	}
}
