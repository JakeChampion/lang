package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestUnitLaneRequestsWasmtime(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test-units.yml"))
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

func TestArm64RunTestsFallBackToNative(t *testing.T) {
	scanned := 0
	for _, root := range []string{"..", filepath.Join("..", "..", "..", "cmd")} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "e2ecompiler" {
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

func TestUnitLaneSetsAnExplicitTestTimeout(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test-units.yml"))
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
	if mins < 20 {
		t.Errorf("the units lane's -timeout is %dm, which leaves too little headroom above internal/syntax/printer (~6m30s) "+
			"for the lane's other packages to grow into", mins)
	}
}

func TestCoreutilsLaneRequestsWasmtime(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test-coreutils.yml"))
	if err != nil {
		t.Fatalf("read test-coreutils.yml: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, "./.github/actions/setup-fern") {
		t.Fatal("test-coreutils.yml no longer uses setup-fern — if the lane's shape changed, update this gate with it")
	}
	if !regexp.MustCompile(`(?m)^\s+wasmtime:\s*true\s*$`).MatchString(src) {
		t.Error("test-coreutils.yml does not pass `wasmtime: true` to setup-fern, so " +
			"internal/testing/coreutils/wasm_test.go t.Skip()s and the lane reports ok having run no module (#8472)")
	}
}

func TestCoreutilsLaneHasTheGNUOracle(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "actions", "gnu-coreutils", "action.yml"))
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
			"the build moved to test-coreutils.yml with internal/testing/coreutils")
	}
}
