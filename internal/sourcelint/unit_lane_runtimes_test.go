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
			"and the lane reports ok while executing no module (#8472). It owns internal/codegen/{wasmbin,wasmssa} " +
			"and internal/wasm/**; no other lane runs them.")
	}
}

// TestArm64RunTestsFallBackToNative pins the second half. The lane also runs
// on ubuntu-24.04-arm, which can execute an arm64 binary with no emulator at
// all — but a bare qemu lookup skips there too. Every run helper must use the
// qemuOrNative fallback instead.
//
// internal/e2eselfhost is excluded: it has its own lane, which installs qemu.
func TestArm64RunTestsFallBackToNative(t *testing.T) {
	pkgs := []string{
		filepath.Join("..", "native", "elf"),
		filepath.Join("..", "native", "arm64"),
		filepath.Join("..", "codegen", "arm64ssa"),
	}
	for _, dir := range pkgs {
		files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		if len(files) == 0 {
			t.Errorf("no test files under %s — if the package moved, update this gate with it", dir)
			continue
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			body := string(b)
			// The fallback's own definition contains the lookup, so a file
			// that defines qemuOrNative is allowed to name it.
			if strings.Contains(body, "func qemuOrNative(") {
				continue
			}
			if strings.Contains(body, `LookPath("qemu-aarch64")`) {
				t.Errorf("%s looks qemu up directly instead of calling qemuOrNative, so its run tests skip on the "+
					"arm64 runner that could execute them natively (#8472)", filepath.ToSlash(f))
			}
		}
	}
}

// TestUnitLaneSetsAnExplicitTestTimeout pins the lane's -timeout.
//
// internal/coreutils compiles all ~70 utilities twice — once with the native
// compiler, once with the self-host one — and runs both corpora, which is a
// ten-minute test binary. go test's default timeout is also ten minutes, so
// without an explicit one the broadest package in the lane runs against a
// wall nobody chose. A binary that wall kills reports the cases that were in
// flight as failures with NO output and an unknown elapsed: the lane names
// tests that did not fail and never names the timeout.
func TestUnitLaneSetsAnExplicitTestTimeout(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test-units.yml"))
	if err != nil {
		t.Fatalf("read test-units.yml: %v", err)
	}
	m := regexp.MustCompile(`gotestsum --format pkgname-and-test-fails -- -timeout (\d+)m`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("the units lane's gotestsum line passes no -timeout, so internal/coreutils — a ten-minute test " +
			"binary — runs against go test's ten-minute default and reports the cases in flight when it is killed " +
			"as failures with no output")
	}
	mins, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse the lane's -timeout %q: %v", m[1], err)
	}
	// internal/coreutils measures ~10m; anything under twice that is one
	// utility's worth of growth away from being killed again.
	if mins < 20 {
		t.Errorf("the units lane's -timeout is %dm, which leaves internal/coreutils (~10m) under two times its "+
			"own runtime of headroom", mins)
	}
}
