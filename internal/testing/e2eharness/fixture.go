// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/testing/e2e and
// internal/testing/e2ecompiler (#4398 part 3). Extracted verbatim from
// internal/testing/e2e/fixture_test.go.
package e2eharness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/ast"
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
//
// It names no target, so a `target_os()` / `target_arch()` call reaches the
// lowering unfolded and the backend answers it with its own target — which
// is also what the driver's fold would have said. LoadCheckMonoFor folds
// the two before the check, as the driver does.
func LoadCheckMono(t *testing.T, mainPath string) (*checker.Info, *ast.Program) {
	t.Helper()
	return LoadCheckMonoFor(t, mainPath, "")
}

// LoadCheckMonoFor is LoadCheckMono for a program compiled for the named
// target ("arm64-linux", "wasm32-wasi", …), whose two halves are what
// `target_os()` and `target_arch()` fold to. An empty or unknown name
// leaves both calls unfolded.
func LoadCheckMonoFor(t *testing.T, mainPath, target string) (*checker.Info, *ast.Program) {
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
	targetOS, targetArch := "", ""
	if d := platforms.ForTarget(target); d != nil {
		targetOS, targetArch = d.Environment, d.ISA
	}
	if err := constfold.FoldWith(prog, constfold.Inputs{TargetOS: targetOS, TargetArch: targetArch}); err != nil {
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

// ConformanceCases is the conformance corpus root.
//
// The corpus deliberately lives OUTSIDE internal/: it describes the
// language, not any one implementation of it, and both the Go-side
// backends and the self-host compiler are measured against it. See
// conformance/README.md for the case format.
func ConformanceCases() string { return RepoPath("conformance", "cases") }
