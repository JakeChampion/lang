package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostIRPerModuleLinkArm64 is the arm64 counterpart of the x86
// per-module link/driver tests (#3451 / #3457 step 0a): the arm64
// asm_modload_run -target arm64-linux driver compiling a multi-module program by emitting
// each module as its OWN arm64 translation unit and linking them.
//
// The program is the cross-module ENUM case (mirroring
// TestSelfHostIRPerModuleCrossEnum): module `col` constructs `Blue(7)`; the
// entry `match`es it. That match lowers to a shape-pointer compare (variant_is),
// so it is the load-bearing test for the arm64 shape-symbol SHARING this slice
// adds — each unit emits the enum variant's shape name as a `.weak` global
// (__fern_shp_col__Blue), so the SAME variant constructed in `col` and compared
// in the entry resolves to ONE linker-merged address and the match finds its
// arm across the module boundary. Without it the match silently fails (the
// per-module bootstrap miscompile this slice prevents). The linked binary,
// run under qemu-aarch64, returns 7.
//
// The driver itself is built as an x86 host binary (only its OUTPUT is arm64
// asm); the emitted units are assembled+linked with the aarch64 cross gcc.
func TestSelfHostIRPerModuleLinkArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	x86gcc, _ := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	// The arm64 backend + its per-module driver, alongside the shared modload
	// project (which already holds util/lexer/parser/flatten/asm_ir/builtins/…).

	// Build the arm64 driver as an x86 host binary (mirrors the fixpoint harness).
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_modload_run.fern", "arm64driver")

	// Two-module program: col constructs Blue(7); the entry matches it.
	colSrc := "pub enum Color { Red(i32), Green, Blue(i32) }\n" +
		"pub function mk(): Color { return Blue(7); }\n"
	mainSrc := "import \"./col\";\n" +
		"function main(): i32 {\n" +
		"    var c: col.Color = col.mk();\n" +
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
		out, err := exec.Command(driverBin, append([]string{entryPath, "-target", "arm64-linux"}, args...)...).Output()
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

	cmd := exec.Command(qemu, binPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("arm64 per-module binary exit = %d, want 7 (cross-module enum shape identity Blue(7))", code)
	}
}
