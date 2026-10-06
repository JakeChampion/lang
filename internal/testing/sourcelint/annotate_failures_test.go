package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/ci-annotate-failures turns a failed test's test2json events into
// one `::error` annotation naming the test, with the tail of its output,
// so a failure is readable from the Checks tab and the REST API when the
// log is not. The fixture is a real worker stream from a shard run, trimmed
// to one failing test and its parent.
func TestAnnotateFailuresNamesTheTestAndItsOutput(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "ci-annotate-failures")
	fixture := filepath.Join("testdata", "annotate-failures.jsonl")
	cmd := exec.Command(script, fixture)
	cmd.Env = ciEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ci-annotate-failures: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 1 {
		t.Fatalf("want one annotation (the parent fails only through its subtest and is not a row), got %d:\n%s", len(lines), out)
	}
	line := lines[0]
	for _, want := range []string{
		"::error title=FAIL ",
		".TestSelfHostAllocCountMatrixX86_64/str_self_append_chain::",
		"blocks per round moved",
		"%0A",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("annotation lacks %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, "=== RUN") || strings.Contains(line, "--- FAIL") {
		t.Errorf("annotation carries the harness framing instead of the test's output:\n%s", line)
	}

	// No failure, no output, exit 0: the step runs only on failure, and a
	// stream of passes must not invent a row.
	passing := filepath.Join(t.TempDir(), "pass.jsonl")
	if err := os.WriteFile(passing, []byte(`{"Time":"2026-10-01T21:00:00Z","Action":"run","Package":"p","Test":"TestOk"}
{"Time":"2026-10-01T21:00:00Z","Action":"output","Package":"p","Test":"TestOk","Output":"=== RUN   TestOk\n"}
{"Time":"2026-10-01T21:00:01Z","Action":"pass","Package":"p","Test":"TestOk","Elapsed":1}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(script, passing)
	cmd.Env = ciEnv()
	out, err = cmd.Output()
	if err != nil || len(out) != 0 {
		t.Errorf("a passing stream: err=%v out=%q; want no output and exit 0", err, out)
	}
}
