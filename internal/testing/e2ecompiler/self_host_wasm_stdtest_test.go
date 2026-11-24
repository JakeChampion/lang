package e2ecompiler

import (
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The std/test programs that wasm32-wasi cannot run, and why. Each reason is
// a limit of the target, not a fault to fix.
var wasmUnsupportedStdTests = map[string]string{
	"fern_binary_e2e":          "subprocess: wasm has no processes (E066)",
	"process_assertions":       "subprocess: wasm has no processes (E066)",
	"process_output_shortcuts": "subprocess: wasm has no processes (E066)",
	"filesystem_ops":           "fsdir: wasm has no directory listing (E066)",
	"worker":                   "proc: wasm has no worker processes (E066)",
	"net_broadcast":            "wasi:sockets has no broadcast option",
	"pagetable":                "AArch64 page tables need a 64-bit usize, and wasm32 has a 32-bit usize",
}

// Each std/test program, built as a wasm32-wasi component and run under
// wasmtime, must print the same TAP stream and exit with the same code as the
// Go oracle's interpreter. The program gets the environment of the host and
// one directory, so a temporary file or directory goes there. A test that the
// program skips because the host lacks something counts as a pass.
func TestSelfHostStdTestWasm(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	all := selfHostStdTestCases(t, false)
	known := map[string]bool{}
	for _, tc := range all {
		known[tc.name] = true
	}
	for name := range wasmUnsupportedStdTests {
		if !known[name] {
			t.Errorf("wasmUnsupportedStdTests names %q, which is not a std/test case", name)
		}
	}
	cases := selfHostStdTestShard(t, all)
	t.Run("cases", func(t *testing.T) {
		for _, tc := range cases {
			if _, ok := wasmUnsupportedStdTests[tc.name]; ok {
				continue
			}
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				wantOut, wantExit := e2eharness.InterpVerdict(t, interpBin, tc.src)
				comp := cli.wasmComponent(t, tc.src, "FERN_STRICT_IR=1")
				run := exec.Command(wasmtime, "run", "-S", "inherit-env", "--dir", t.TempDir(), comp)
				gotOut, runErr := stdTestOutput(run)
				if run.ProcessState == nil {
					t.Fatalf("wasmtime did not start: %v", runErr)
				}
				if gotExit := run.ProcessState.ExitCode(); gotExit != wantExit {
					var exitErr *exec.ExitError
					if errors.As(runErr, &exitErr) {
						t.Logf("wasmtime stderr:\n%s", exitErr.Stderr)
					}
					t.Errorf("exit code: wasm %d, interp %d", gotExit, wantExit)
				}
				gotStr, wantStr := tapSkipsAsPasses(string(gotOut)), tapSkipsAsPasses(string(wantOut))
				if tc.stripPrefix != "" {
					gotStr = stripLinesWithPrefix(gotStr, tc.stripPrefix)
					wantStr = stripLinesWithPrefix(wantStr, tc.stripPrefix)
				}
				if gotStr != wantStr {
					t.Errorf("TAP output mismatch:\n--- wasm ---\n%s\n--- interp ---\n%s", gotStr, wantStr)
				}
			})
		}
	})
}

var tapSkipLine = regexp.MustCompile(`^ok (\d+ - .*) # SKIP .*$`)

// tapSkipsAsPasses gives a TAP stream of std/test with each skipped test
// written as a pass, and with no skip count or list of skipped tests.
func tapSkipsAsPasses(tap string) string {
	lines := strings.Split(tap, "\n")
	skips := 0
	var out []string
	inList := false
	for _, ln := range lines {
		if inList && strings.HasPrefix(ln, "#   ") {
			continue
		}
		inList = false
		if m := tapSkipLine.FindStringSubmatch(ln); m != nil {
			out = append(out, "ok "+m[1])
			skips++
			continue
		}
		if strings.HasPrefix(ln, "# skip ") {
			continue
		}
		if ln == "# skipped:" {
			inList = true
			continue
		}
		out = append(out, ln)
	}
	for i, ln := range out {
		if n, ok := strings.CutPrefix(ln, "# pass "); ok {
			if v, err := strconv.Atoi(n); err == nil {
				out[i] = "# pass " + strconv.Itoa(v+skips)
			}
		}
	}
	return strings.Join(out, "\n")
}

func TestTapSkipsAsPasses(t *testing.T) {
	skipped := "TAP version 13\n# Suite: s\nok 1 - a\nok 2 - b # SKIP the host has no x\n1..2\n# tests 2\n# pass 1\n# fail 0\n# skip 1\n# skipped:\n#   b: the host has no x\n"
	passed := "TAP version 13\n# Suite: s\nok 1 - a\nok 2 - b\n1..2\n# tests 2\n# pass 2\n# fail 0\n"
	if got, want := tapSkipsAsPasses(skipped), tapSkipsAsPasses(passed); got != want {
		t.Errorf("a skip did not read as a pass:\n--- skipped ---\n%s\n--- passed ---\n%s", got, want)
	}
	failed := "TAP version 13\n# Suite: s\nok 1 - a\nnot ok 2 - b\n1..2\n# tests 2\n# pass 1\n# fail 1\n"
	if tapSkipsAsPasses(failed) == tapSkipsAsPasses(passed) {
		t.Error("a failure read as a pass")
	}
}
