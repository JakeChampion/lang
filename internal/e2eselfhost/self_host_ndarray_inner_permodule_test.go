package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The checked adapter lives in the ndarray unit while the rewritten call
// lives in a user library. Link both enabled and disabled forms so pruning
// cannot silently discard a cross-unit dependency.
func TestSelfHostNdarrayInnerPerModule(t *testing.T) {
	x86gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driver := buildSelfHostBin(t, x86gcc, dir, "asm_modload_run.fern", "inner-module")
	proj := t.TempDir()
	mustWrite(t, proj, "leaf.fern", `import "std/ndarray";
@noinline pub function score(xs: f64[]): f64 {
  let a = ndarray.from_flat(xs, [2, 2]);
  let out = a.inner(a, 0.25, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y);
  return out.get([0, 0]) + out.get([1, 1]);
}`)
	mustWrite(t, proj, "main.fern", `import "./leaf";
function main(): i32 { if (leaf.score([1.0, 2.0, 3.0, 4.0]) != 29.5) { return 1; } return 0; }`)
	copyStdlibTree(t, proj)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, disabled := range []string{"0", "1"} {
			t.Run(target+"/disabled="+disabled, func(t *testing.T) {
				gcc := x86gcc
				var qemu string
				if target == "arm64-linux" {
					gcc, qemu = arm64Tooling(t)
				}
				outDir := t.TempDir()
				cmd := runX86_64Bin(runner, driver, filepath.Join(proj, "main.fern"), "-target", target, "-per-module-emit-all", "-out-dir", outDir)
				cmd.Env = append(os.Environ(), "FERN_NO_PRODUCT_KERNEL="+disabled)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("emit inner modules: %v\n%s", err, out)
				}
				files, err := os.ReadDir(outDir)
				if err != nil {
					t.Fatal(err)
				}
				var units []string
				sawAdapter := false
				for _, file := range files {
					path := filepath.Join(outDir, file.Name())
					units = append(units, path)
					text, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					sawAdapter = sawAdapter || strings.Contains(string(text), "__fn_ndarray__inner_kernel")
				}
				if sawAdapter != (disabled == "0") {
					t.Fatalf("adapter present=%t with disabled=%s", sawAdapter, disabled)
				}
				bin := filepath.Join(t.TempDir(), "inner")
				args := append([]string{"-static", "-nostdlib", "-no-pie"}, units...)
				args = append(args, "-o", bin)
				if out, err := exec.Command(gcc, args...).CombinedOutput(); err != nil {
					t.Fatalf("link inner modules: %v\n%s", err, out)
				}
				run := runX86_64Bin(runner, bin)
				if target == "arm64-linux" {
					run = runArm64Bin(qemu, bin)
				}
				if out, err := run.CombinedOutput(); err != nil {
					t.Fatalf("inner modules: %v\n%s", err, out)
				}
			})
		}
	}
}
