// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/fixture_test.go.
package e2eharness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

func RunFixtureInterp(t *testing.T, mainPath, stdin string) (string, int) {
	t.Helper()
	bin := BuildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", mainPath)
	cmd.Stdin = strings.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	_ = cmd.Run()
	return so.String(), cmd.ProcessState.ExitCode()
}

// LoadCheckMono runs the shared front of the pipeline (modload →
// constfold → check → monomorph) against a fixture's entry file. It
// loads from the real fixture directory so relative `./sibling`
// imports resolve against the on-disk layout.
func LoadCheckMono(t *testing.T, mainPath string) (*checker.Info, *ast.Program) {
	t.Helper()
	// Ensure core/int is in the import closure so the wasm runner's
	// BuildOptions.PrintMainResult wrapper can stringify main()'s i32
	// return (int_to_string) — the auto-prelude used to supply that
	// name. Injected via a LoadWith override on the entry file so
	// relative `./sibling` imports still resolve against the real
	// fixture directory; harmless (unused, tree-shaken) on the x86 /
	// arm64 runners, which don't print the result. Negative `err_*`
	// fixtures bypass this (they go through runFixtureCompileError).
	abs, err := filepath.Abs(mainPath)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	orig, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	overrides := map[string]string{abs: "import \"core/int\";\n" + string(orig)}
	prog, _, err := modload.LoadWith(mainPath, overrides)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	return info, prog
}

func RunBin(cmd *exec.Cmd, stdin string) (string, int) {
	cmd.Stdin = strings.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	_ = cmd.Run()
	return so.String(), cmd.ProcessState.ExitCode()
}

func Contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
