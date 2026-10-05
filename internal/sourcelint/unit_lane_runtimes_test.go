package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// CLAUDE.md: a SKIP is a missing dependency to fix, not a green light. The
// unit lane was built so that a large family of tests could only ever skip
// (#8472) — it owns the wasm unit packages and the arm64 run tests, and no
// other lane runs them.
//
// Two independent halves, so two gates. A third gate below covers the same
// class of false report from the other direction: a suite the harness kills
// rather than one it never runs.

// TestUnitLaneRequestsWasmtime pins the install. 902 test sites across
// internal/ open with `exec.LookPath("wasmtime")` and t.Skip() without it, and
// a skipped test reports `ok`, so dropping this input returns the whole wasm
// unit surface to running nothing while staying green.
//
// Requesting it also arms setup-fern's preflight, which fails the JOB when a
// requested tool is missing — that is what makes the coverage real rather than
// hopeful.
func TestUnitLaneRequestsWasmtime(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test-units.yml"))
	if err != nil {
		t.Fatalf("read test-units.yml: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, "./.github/actions/setup-fern") {
		t.Fatal("test-units.yml no longer uses setup-fern — if the lane's shape changed, update this gate with it")
	}
	if !regexp.MustCompile(`(?m)^\s+wasmtime:\s*true\s*$`).MatchString(src) {
		t.Error("test-units.yml does not pass `wasmtime: true` to setup-fern, so every wasm unit test t.Skip()s " +
			"and the lane reports ok while executing no module (#8472). It owns internal/wasm/**; " +
			"no other lane runs them.")
	}
}

// TestArm64RunTestsFallBackToNative pins the second half. The lane also runs
// on ubuntu-24.04-arm, which can execute an arm64 binary with no emulator at
// all — but a bare qemu lookup skips there too. A run helper must fall back
// to running the binary natively instead.
//
// internal/e2eselfhost is excluded: it has its own lane, which installs qemu.
func TestArm64RunTestsFallBackToNative(t *testing.T) {
	scanned := 0
	for _, root := range []string{"..", filepath.Join("..", "..", "cmd")} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "e2eselfhost" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "unit_lane_runtimes_test.go") {
				return nil
			}
			scanned++
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), `LookPath("qemu-aarch64")`) {
				t.Errorf("%s looks qemu up directly, so its run tests skip on the arm64 runner that could "+
					"execute them natively (#8472)", filepath.ToSlash(path))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned == 0 {
		t.Fatal("no test files scanned — the walk is reading the wrong tree")
	}
}

// TestUnitLaneSetsAnExplicitTestTimeout pins the lane's -timeout.
//
// go test's default timeout is ten minutes, and a binary it kills reports the
// cases that were in flight as failures with NO output and an unknown elapsed:
// the lane names tests that did not fail and never names the timeout. So the
// lane chooses its own rather than inheriting that default.
func TestUnitLaneSetsAnExplicitTestTimeout(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test-units.yml"))
	if err != nil {
		t.Fatalf("read test-units.yml: %v", err)
	}
	m := regexp.MustCompile(`gotestsum --format pkgname-and-test-fails -- -timeout (\d+)m`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("the units lane's gotestsum line passes no -timeout, so its longest package runs against " +
			"go test's ten-minute default and reports the cases in flight when it is killed as failures " +
			"with no output")
	}
	mins, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse the lane's -timeout %q: %v", m[1], err)
	}
	// internal/ir, the longest package left after internal/coreutils moved to
	// its own lane, measures ~4m40s. The margin is deliberately generous: the
	// lane runs 77 packages and the bound is on the slowest, not the sum.
	if mins < 20 {
		t.Errorf("the units lane's -timeout is %dm, which leaves too little headroom above internal/ir (~4m40s) "+
			"for the lane's other 76 packages to grow into", mins)
	}
}

// TestCoreutilsLaneRequestsWasmtime is the units lane's wasmtime gate, for the
// lane internal/coreutils moved to. internal/coreutils/wasm_test.go opens with
// `exec.LookPath("wasmtime")` and t.Skip()s without it, and a skipped test
// reports ok (#8472) — so the install is what makes that case real, and
// requesting it arms setup-fern's preflight to fail the job when it is absent.
func TestCoreutilsLaneRequestsWasmtime(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test-coreutils.yml"))
	if err != nil {
		t.Fatalf("read test-coreutils.yml: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, "./.github/actions/setup-fern") {
		t.Fatal("test-coreutils.yml no longer uses setup-fern — if the lane's shape changed, update this gate with it")
	}
	if !regexp.MustCompile(`(?m)^\s+wasmtime:\s*true\s*$`).MatchString(src) {
		t.Error("test-coreutils.yml does not pass `wasmtime: true` to setup-fern, so " +
			"internal/coreutils/wasm_test.go t.Skip()s and the lane reports ok having run no module (#8472)")
	}
}

// TestCoreutilsLaneHasTheGNUOracle pins the reference every GNU comparison is
// made against. A parity case asks a GNU binary what the answer is, so without
// the built 9.12 oracle a lane either fails wholesale against the image's 9.4
// or — worse — reports version differences as Fern's bugs. The build is one
// composite action, used by the coreutils lane and by the self-host shards,
// whose byte tests compare the self-host's utilities with GNU's too.
func TestCoreutilsLaneHasTheGNUOracle(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "actions", "gnu-coreutils", "action.yml"))
	if err != nil {
		t.Fatalf("read the gnu-coreutils action: %v", err)
	}
	for _, want := range []string{"FERN_GNU_COREUTILS", "gnu-coreutils-full-"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the gnu-coreutils action no longer mentions %s; the comparisons would be made against "+
				"whatever coreutils the runner image ships (9.4) rather than the pinned oracle", want)
		}
	}
	for _, lane := range []string{"test-coreutils.yml", "test-e2e-selfhost.yml"} {
		if !strings.Contains(workflowSource(t, lane), "./.github/actions/gnu-coreutils") {
			t.Errorf("%s no longer uses the gnu-coreutils action, so its GNU comparisons fall back to the image's 9.4", lane)
		}
	}
	units := workflowSource(t, "test-units.yml")
	if strings.Contains(units, "GNU_COREUTILS_VERSION") {
		t.Error("test-units.yml still builds the GNU coreutils oracle, but no package it runs uses it — " +
			"the build moved to test-coreutils.yml with internal/coreutils")
	}
}
