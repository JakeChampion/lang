package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostIRPerModuleDriver exercises the per-module emit + link driver path
// (#3451 step 4 / #3456) end to end. asm_load_run loads a real multi-module
// program (entry importing a local module) and drives per-module compilation —
// flatten.bundle_per_module mangles but keeps the modules SEPARATE, the whole-
// program signature view + runtime-need union span all modules, and each module
// is emitted as its own translation unit (entry → `_start` + the single shared
// runtime; the import → a library unit exposing its `.globl __fn_*`).
//
// The native runtime's global string buffer holds only ONE emit per process, so
// — like a real build system — the build is driven across separate invocations:
// `-per-module-count`, `-per-module-needs`, then `-per-module-emit N` per
// module (the entry folds the need union via `-extra-need`). The test assembles
// + links every unit with gcc and runs the binary — exactly #3456's acceptance
// ("compiles a multi-module program by emitting per-module + linking, producing
// a working binary").
//
// The entry calls greet.greeting_len() (length of "hello" = 5) and adds 1 →
// exit 6. The import allocates its string box via __fern_alloc, an extern
// resolved against the entry's single runtime (the heap need aggregated).
func TestSelfHostIRPerModuleDriver(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")

	greetSrc := "function greeting_len(): i32 { var s = \"hello\"; return s.len(); }\n"
	mainSrc := "import \"./greet\";\nfunction main(): i32 { return greet.greeting_len() + 1; }\n"
	if err := os.WriteFile(filepath.Join(dir, "greet.fern"), []byte(greetSrc), 0o644); err != nil {
		t.Fatalf("write greet.fern: %v", err)
	}
	entryPath := filepath.Join(dir, "prog_main.fern")
	if err := os.WriteFile(entryPath, []byte(mainSrc), 0o644); err != nil {
		t.Fatalf("write prog_main.fern: %v", err)
	}

	// drive runs the driver over the entry with the given trailing args.
	drive := func(t *testing.T, args ...string) string {
		t.Helper()
		full := append([]string{entryPath}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("driver failed (args %v): %v", args, err)
		}
		return string(out)
	}

	// 1. Module count.
	n, err := strconv.Atoi(strings.TrimSpace(drive(t, "-per-module-count")))
	if err != nil || n < 2 {
		t.Fatalf("-per-module-count = %q (n=%d err=%v), want >=2", drive(t, "-per-module-count"), n, err)
	}

	// 2. Whole-program runtime-need union.
	var needArgs []string
	for _, ln := range strings.Split(strings.TrimSpace(drive(t, "-per-module-needs")), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}
	if !strings.Contains(strings.Join(needArgs, " "), "heap") {
		t.Fatalf("need union missing heap: %v", needArgs)
	}

	// 3. Emit each module as its own unit (the entry folds in -extra-need).
	var objs []string
	var sawEntry, sawLib bool
	for i := 0; i < n; i++ {
		emitArgs := append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)
		asm := drive(t, emitArgs...)
		if strings.Contains(asm, "_start:") {
			sawEntry = true
		} else {
			sawLib = true
		}
		p := filepath.Join(dir, "pm_unit_"+strconv.Itoa(i)+".s")
		if err := os.WriteFile(p, []byte(asm), 0o644); err != nil {
			t.Fatalf("write unit %d: %v", i, err)
		}
		objs = append(objs, p)
	}
	if !sawEntry || !sawLib {
		t.Fatalf("expected one entry unit (with _start) and one library unit; sawEntry=%v sawLib=%v", sawEntry, sawLib)
	}

	binPath := filepath.Join(dir, "prog_pm")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", binPath)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link per-module units failed: %v\n%s", err, lout)
	}

	var rcmd *exec.Cmd
	if len(runner) == 0 {
		rcmd = exec.Command(binPath)
	} else {
		rcmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = rcmd.CombinedOutput()
	if code := rcmd.ProcessState.ExitCode(); code != 6 {
		t.Errorf("per-module driver binary exit = %d, want 6 (len \"hello\" + 1)", code)
	}
}

// TestSelfHostIRPerModuleCrossStruct guards the per-module eligibility-gate
// VIEW fix (#3451): the gate (all_eligible_known_view / all_eligible_lib_known_view
// → eligible_core_known_main_view) must lower each function against the WHOLE-
// PROGRAM struct view (all_structs), exactly as emit_module_funcs does — not the
// module's own structs. Without it, a module that touches a SIBLING module's
// struct LAYOUT fails the gate and bails to AST even though the per-module emit
// (which already lowers against all_structs) would succeed.
//
// The program is the minimal trigger: `point` defines a struct + constructor,
// the entry imports it and reads an imported-struct FIELD (`p.x + p.y`). Lowering
// the entry's `main` needs point.Point's field layout, which lives in the point
// module — present only in the whole-program struct view. Pre-fix, the entry unit
// reported "module not IR-eligible" and the build could not proceed; post-fix
// every unit is eligible, the units link, and the binary exits 3 + 4 = 7.
func TestSelfHostIRPerModuleCrossStruct(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")

	pointSrc := "pub struct Point { x: i32, y: i32 }\n" +
		"pub function mk(a: i32, b: i32): Point { return Point { x: a, y: b }; }\n"
	mainSrc := "import \"./point\";\n" +
		"function main(): i32 { var p: point.Point = point.mk(3, 4); return p.x + p.y; }\n"
	if err := os.WriteFile(filepath.Join(dir, "point.fern"), []byte(pointSrc), 0o644); err != nil {
		t.Fatalf("write point.fern: %v", err)
	}
	entryPath := filepath.Join(dir, "cs_main.fern")
	if err := os.WriteFile(entryPath, []byte(mainSrc), 0o644); err != nil {
		t.Fatalf("write cs_main.fern: %v", err)
	}

	drive := func(t *testing.T, args ...string) string {
		t.Helper()
		full := append([]string{entryPath}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		out, err := cmd.Output()
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
	for i := 0; i < n; i++ {
		emitArgs := append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)
		asm := drive(t, emitArgs...)
		p := filepath.Join(dir, "cs_unit_"+strconv.Itoa(i)+".s")
		if err := os.WriteFile(p, []byte(asm), 0o644); err != nil {
			t.Fatalf("write unit %d: %v", i, err)
		}
		objs = append(objs, p)
	}

	binPath := filepath.Join(dir, "cs_prog")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", binPath)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link per-module units failed: %v\n%s", err, lout)
	}

	var rcmd *exec.Cmd
	if len(runner) == 0 {
		rcmd = exec.Command(binPath)
	} else {
		rcmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = rcmd.CombinedOutput()
	if code := rcmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("per-module cross-struct binary exit = %d, want 7 (3 + 4)", code)
	}
}

// TestSelfHostIRPerModuleCrossEnum guards the per-module SHAPE-symbol fix (#3451):
// enum/struct values are discriminated by a shape POINTER (the interned
// variant-name string), compared by pointer equality. Per-module, each unit
// interns its strings in its own .rodata, so the SAME shape referenced in two
// units lands at two different addresses — a value CONSTRUCTED in module A and
// MATCHED in module B compares mismatched pointers and the match silently falls
// through. shape_ref emits shapes as `.weak` global symbols (shape_sym) the linker
// merges to one address, so cross-module discrimination holds.
//
// The program is the minimal trigger: `col` defines an enum + a constructor
// returning `Blue(7)`; the entry imports it and `match`es the value across the
// module boundary. Pre-fix the Blue arm never matches (shape mismatch) and the
// binary falls through to exit 0; post-fix it matches and exits 7. This is the
// exact pattern the self-hosted parser hits (matching the lexer's token variants),
// the root cause of the per-module self-build miscompile.
func TestSelfHostIRPerModuleCrossEnum(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")

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
		full := append([]string{entryPath}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		out, err := cmd.Output()
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
	for i := 0; i < n; i++ {
		emitArgs := append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)
		asm := drive(t, emitArgs...)
		p := filepath.Join(dir, "ce_unit_"+strconv.Itoa(i)+".s")
		if err := os.WriteFile(p, []byte(asm), 0o644); err != nil {
			t.Fatalf("write unit %d: %v", i, err)
		}
		objs = append(objs, p)
	}

	binPath := filepath.Join(dir, "ce_prog")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", binPath)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link per-module units failed: %v\n%s", err, lout)
	}

	var rcmd *exec.Cmd
	if len(runner) == 0 {
		rcmd = exec.Command(binPath)
	} else {
		rcmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = rcmd.CombinedOutput()
	if code := rcmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("per-module cross-enum binary exit = %d, want 7 (Blue(7) matched across the module boundary)", code)
	}
}
