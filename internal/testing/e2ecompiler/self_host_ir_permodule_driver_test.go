package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostIRPerModuleDriver(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")

	greetSrc := "function greeting_len(): i32 { let s = \"hello\"; return s.len(); }\n"
	mainSrc := "import \"./greet\";\nfunction main(): i32 { return greet.greeting_len() + 1; }\n"
	if err := os.WriteFile(filepath.Join(dir, "greet.fern"), []byte(greetSrc), 0o644); err != nil {
		t.Fatalf("write greet.fern: %v", err)
	}
	entryPath := filepath.Join(dir, "prog_main.fern")
	if err := os.WriteFile(entryPath, []byte(mainSrc), 0o644); err != nil {
		t.Fatalf("write prog_main.fern: %v", err)
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
		t.Fatalf("-per-module-count = %q (n=%d err=%v), want >=2", drive(t, "-per-module-count"), n, err)
	}

	var needArgs []string
	for _, ln := range strings.Split(strings.TrimSpace(drive(t, "-per-module-needs")), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}
	if !strings.Contains(strings.Join(needArgs, " "), "heap") {
		t.Fatalf("need union missing heap: %v", needArgs)
	}

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

func TestSelfHostIRPerModuleCrossStruct(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")

	pointSrc := "pub struct Point { x: i32, y: i32 }\n" +
		"pub function mk(a: i32, b: i32): Point { return Point { x: a, y: b }; }\n"
	mainSrc := "import \"./point\";\n" +
		"function main(): i32 { let p: point.Point = point.mk(3, 4); return p.x + p.y; }\n"
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

func TestSelfHostIRPerModuleCrossEnum(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")

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
