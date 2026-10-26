package e2ecompiler

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func internalRuntimeSource(t *testing.T, target, roots string, mode int, reuse bool) string {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	const driver = `import "./rtmodule";
import "./printer";
function main(): i32 {
  let av = args();
  let mode = 1;
  if (av[3] == "2") { mode = 2; }
  if (av[3] == "3") { mode = 3; }
  if (av[3] == "4") { mode = 4; }
  print(printer.print_module(rtmodule.load_with(av[1], av[2], mode, "", av[4] == "true")));
  return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "runtime_source.fern"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "runtime_source.fern", "runtime_source")
	out, err := runX86_64Bin(runner, bin, target, roots, strconv.Itoa(mode), strconv.FormatBool(reuse)).CombinedOutput()
	if err != nil {
		t.Fatalf("runtime source: %v\n%s", err, out)
	}
	return string(out)
}

func TestSelfHostInternalRuntimeModules(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/runtime_modules_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/runtime_modules_run.fern", "runtime_modules")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("runtime modules: %v\n%s", err, out)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin", "wasm32-wasi", "wasm32-wasi-http"} {
		if !strings.Contains(string(out), target+" runtime modules ok") {
			t.Errorf("no runtime module check for %s:\n%s", target, out)
		}
	}
}
