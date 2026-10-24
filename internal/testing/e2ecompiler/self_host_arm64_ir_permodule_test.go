package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostIRPerModuleLinkArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_modload_run.fern", "arm64driver")

	colSrc := "pub enum Color { Red(i32), Green, Blue(i32) }\n" +
		"pub function mk(): Color { return Blue(7); }\n"
	mainSrc := "import \"./col\";\n" +
		"function main(): i32 {\n" +
		"    let c: col.Color = col.mk();\n" +
		"    match (c) {\n" +
		"        Red(x) => { return x + 100; },\n" +
		"        Green => { return 200; },\n" +
		"        Blue(y) => { return y; },\n" +
		"    }\n" +
		"    return 0;\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dir, "col.fern"), []byte(colSrc), 0o644); err != nil {
		t.Fatalf("write col.fern: %v", err)
	}
	entryPath := filepath.Join(dir, "ce_main.fern")
	if err := os.WriteFile(entryPath, []byte(mainSrc), 0o644); err != nil {
		t.Fatalf("write ce_main.fern: %v", err)
	}

	drive := func(t *testing.T, args ...string) string {
		t.Helper()
		out, err := runX86_64Bin(x86runner, driverBin, append([]string{entryPath, "-target", "arm64-linux"}, args...)...).Output()
		if err != nil {
			t.Fatalf("driver failed (args %v): %v", args, err)
		}
		return string(out)
	}

	n, err := strconv.Atoi(strings.TrimSpace(drive(t, "-per-module-count")))
	if err != nil || n < 2 {
		t.Fatalf("-per-module-count = %d (err=%v), want >=2", n, err)
	}
	var needArgs []string
	for _, ln := range strings.Split(strings.TrimSpace(drive(t, "-per-module-needs")), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	var objs []string
	sawEntry := false
	for i := 0; i < n; i++ {
		unit := drive(t, append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)...)
		if len(unit) == 0 {
			t.Fatalf("module %d: per-module emit bailed (not IR-eligible)", i)
		}
		if strings.Contains(unit, "\n_start:\n") || strings.HasPrefix(unit, "_start:\n") {
			sawEntry = true
		}
		p := filepath.Join(dir, "ce_unit_"+strconv.Itoa(i)+".s")
		if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
			t.Fatalf("write unit %d: %v", i, err)
		}
		objs = append(objs, p)
	}
	if !sawEntry {
		t.Fatal("no unit emitted _start — entry module not identified")
	}

	binPath := filepath.Join(dir, "ce_prog")
	if out, err := exec.Command(armgcc, append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", binPath)...)...).CombinedOutput(); err != nil {
		t.Fatalf("aarch64 link failed: %v\n%s", err, out)
	}

	cmd := runArm64Bin(qemu, binPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("arm64 per-module binary exit = %d, want 7 (cross-module enum shape identity Blue(7))", code)
	}
}
