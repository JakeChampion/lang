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

func LoadCheckMono(t *testing.T, mainPath string) (*checker.Info, *ast.Program) {
	t.Helper()
	return LoadCheckMonoFor(t, mainPath, "")
}

func LoadCheckMonoFor(t *testing.T, mainPath, target string) (*checker.Info, *ast.Program) {
	t.Helper()
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
	constfold.PruneDeadArms(prog)
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

func ConformanceCases() string { return RepoPath("conformance", "cases") }
