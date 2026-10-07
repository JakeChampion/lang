package coreutils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Darwin's full bound exceeds 300 million filesystem attempts. GNU keeps
// its actual retry loop and gets EEXIST/EIO at its libc boundary. Fern keeps
// its actual retry loop and command handling and gets the same outcomes at
// its candidate expression. Neither implementation's retry bound is replaced.
// Ordinary candidate IO and entropy remain covered by the live corpus.
type mktempRetryFault struct {
	lib, fern string
}

func newMktempRetryFault(t *testing.T) *mktempRetryFault {
	t.Helper()
	dir := t.TempDir()
	lib := filepath.Join(dir, "collision.dylib")
	cmd := exec.Command("cc", "-O2", "-dynamiclib", "testdata/mktemp_retry_darwin.c", "-o", lib)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build collision interposer: %v\n%s", err, out)
	}
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "coreutils", "mktemp.fern"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(source)
	const candidate = "function make(s: Settings, head: string, run: i32, tail: string): Attempt {"
	const attempt = "try_once(s, head + random_run(run) + tail)"
	if strings.Count(s, candidate) != 1 || strings.Count(s, attempt) != 1 {
		t.Fatal("mktemp candidate boundary changed")
	}
	s = strings.Replace(s, candidate, candidate+`
  let stop: i32 = 0;
  match (env("FERN_PROBE_STOP")) {
    Some(v) => { match (v.parse_int()) { Some(n) => { stop = n; }, None => { exit(91); } } },
    None => {}
  }
  `, 1)
	s = strings.Replace(s, attempt, "retry_fixture_candidate(tries, stop)", 1) + `
function retry_fixture_candidate(at: i32, stop: i32): (Attempt, boolean) {
  assert(at <= attempt_limit());
  if (at == stop || at == attempt_limit()) {
    print("attempts:" + at.to_string());
  }
  if (stop > 0 && at == stop) {
    return (Attempt { name: "fixed", ok: false, text: "Input/output error" }, false);
  }
  return (Attempt { name: "fixed", ok: false, text: "File exists" }, true);
}
`
	if err := os.CopyFS(filepath.Join(dir, "lib"), os.DirFS(filepath.Join(repoRoot(t), "coreutils", "lib"))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mktemp.fern")
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := e2eharness.CompileSelfHostFile(t, fernTarget(t), path, []string{"FERN_STRICT_IR=1"})
	return &mktempRetryFault{lib: lib, fern: bin}
}

func (f *mktempRetryFault) run(t *testing.T, inv invocation, stop int) (outcome, outcome) {
	t.Helper()
	report := filepath.Join(t.TempDir(), "calls")
	if err := os.WriteFile(report, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	gnu := inv
	gnu.env = append(append([]string{}, inv.env...), "DYLD_INSERT_LIBRARIES="+f.lib, "FERN_PROBE_REPORT="+report, "FERN_PROBE_STOP="+strconv.Itoa(stop))
	want := gnu.run(t, referenceBin(t, "mktemp"), "mktemp")
	if want.exit != 1 || want.overran || want.flooded {
		t.Fatalf("GNU fault run: %s, want exit 1", want.how())
	}
	count := 308915776
	if stop > 0 {
		count = stop
	}
	calls, err := os.ReadFile(report)
	if err != nil || strings.TrimSpace(string(calls)) != strconv.Itoa(count) {
		t.Fatalf("GNU attempts: got %q (%v), want %d", calls, err, count)
	}
	fern := inv
	fern.env = append(append([]string{}, inv.env...), "FERN_PROBE_STOP="+strconv.Itoa(stop))
	got := fern.run(t, f.fern, "mktemp")
	marker := fmt.Sprintf("attempts:%d\n", count)
	if string(got.stdout) != marker {
		t.Fatalf("Fern attempt report: got %q, want %q", got.stdout, marker)
	}
	got.stdout = nil // The sole instrumentation line, checked exactly above.
	if len(want.stdout) != 0 || want.how() != got.how() || string(want.stderr) != string(got.stderr) {
		t.Fatalf("retry parity: GNU %s stdout=%q stderr=%q; Fern %s stderr=%q", want.how(), want.stdout, want.stderr, got.how(), got.stderr)
	}
	return want, got
}

func TestMktempRetryFaultStop(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin loader probe; Linux uses the real saturated directory in TestMktempRetryExhaustion")
	}
	fault := newMktempRetryFault(t)
	for _, args := range [][]string{{"XXX"}, {"-d", "XXX"}, {"-u", "XXX"}, {"-u", "-d", "XXX"}, {"-q", "XXX"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fault.run(t, invocation{args: args, dir: t.TempDir()}, 1000000)
		})
	}
}
