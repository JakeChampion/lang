package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostPerModuleHeapMetricLinks(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "heapmetricdriver")
	proj := t.TempDir()
	mustWrite(t, proj, "leaf.fern", `@noinline pub function used(): i64 { return __heap_bump_bytes(); }
@noinline pub function fresh(s: string): string[] { return [s]; }
`)
	mustWrite(t, proj, "main.fern", `import "./leaf";
function main(): i32 {
    let before = leaf.used();
    let text = args()[0];
    let xs = leaf.fresh(text);
    let after = leaf.used();
    if (after <= before || xs[0] != text) { return 1; }
    return 0;
}
`)
	copyStdlibTree(t, proj)
	entry := filepath.Join(proj, "main.fern")
	drive := func(args ...string) string {
		t.Helper()
		cmd := runX86_64Bin(runner, driver, append([]string{entry, "-target", "x86-64-linux"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("driver %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(drive("-per-module-count")))
	if err != nil || n < 2 {
		t.Fatalf("module count=%d, error=%v", n, err)
	}
	var needs, objects []string
	for _, need := range strings.Fields(drive("-per-module-needs")) {
		needs = append(needs, "-extra-need", need)
	}
	for i := 0; i < n; i++ {
		asm := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needs...)...)
		objects = append(objects, mustWrite(t, proj, "u"+strconv.Itoa(i)+".s", asm))
	}
	bin := filepath.Join(proj, "heapmetric")
	args := append([]string{"-static", "-nostdlib", "-no-pie"}, objects...)
	if out, err := exec.Command(gcc, append(args, "-o", bin)...).CombinedOutput(); err != nil {
		t.Fatalf("per-module link: %v\n%s", err, out)
	}
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("per-module result: %v\n%s", err, out)
	}
}
