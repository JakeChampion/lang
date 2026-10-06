package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A plain if accepts an integer truth value in the interpreter's desugared
// guard path. Logical operators and equality expose a lost Boolean type.
func TestSelfHostCapturedBooleanType(t *testing.T) {
	const src = `function both(a: boolean, b: boolean): boolean { return a && b; }
function main(): i32 {
  let flag: boolean = false;
  let flip: () => boolean = (): boolean => { flag = !flag; return flag; };
  let read: () => boolean = (): boolean => flag;
  if (flag || read() || flag != false) { return 1; }
  if (!flip() || !(flag && read()) || !both(flag, true) || flag != true) { return 2; }
  if (flip() || flag || !(!read() && true)) { return 3; }
  flag = true;
  if (!read() || !both(true, flag)) { return 4; }
  flag = false;
  if (read() || !flip()) { return 5; }
  let n: i32 = 3;
  let inc = (): i32 => { n = n + 2; return n; };
  if (inc() != 5 || n * 2 != 10) { return 6; }
  return 0;
}`
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, compiler := range []string{buildLangBinForInterp(t), e2eharness.SelfHostCLI(t)} {
		cmd := exec.Command(compiler, "-interp", path, e2eharness.SelfHostStdlibRoot(t))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", compiler, err, out)
		}
	}
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			if out, code := runSelfHostFusionProgram(t, target, src); code != 0 {
				t.Fatalf("captured Boolean type: exit %d\n%s", code, out)
			}
		})
	}
}
