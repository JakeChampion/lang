package e2e

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
)

// pub(package): a package-scoped helper is usable from a sibling module
// in the same package (directory), end-to-end. helper(41) = 42. See
// docs/PUB-PACKAGE.md.
var pubPackageProject = map[string]string{
	"helpers.fern": `pub(package) function helper(n: i32): i32 { return n + 1; }`,
	"main.fern": `import "./helpers";
function main(): i32 { return helpers.helper(41); }`,
}

func TestInterpPubPackage(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := writeProject(t, pubPackageProject)
	cmd := exec.Command(bin, "-interp", filepath.Join(dir, "main.fern"))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("exit = %d, want 42\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64PubPackage(t *testing.T) {
	dir := writeProject(t, pubPackageProject)
	if _, code := runFixtureX86_64(t, filepath.Join(dir, "main.fern"), ""); code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}
