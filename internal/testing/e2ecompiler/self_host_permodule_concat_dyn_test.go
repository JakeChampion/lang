package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeConcatDynFixture(t *testing.T, dir string) string {
	t.Helper()
	const nMod, nFn = 7, 100
	proj := filepath.Join(dir, "concatdynproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var imports, calls strings.Builder
	want := 0
	for m := 0; m < nMod; m++ {
		var lib strings.Builder
		for f := 0; f < nFn; f++ {
			fmt.Fprintf(&lib, "pub function m%d_f%d(x: i32): i32 { return x + %d; }\n", m, f, m*nFn+f)
		}
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("lib%d.fern", m)), []byte(lib.String()), 0o644); err != nil {
			t.Fatalf("write lib%d: %v", m, err)
		}
		fmt.Fprintf(&imports, "import \"./lib%d\";\n", m)
		if m > 0 {
			calls.WriteString(" + ")
		}
		fmt.Fprintf(&calls, "lib%d.m%d_f0(1)", m, m)
		want += 1 + m*nFn
	}
	shapes := "pub trait Shape {\n" +
		"    function area(self: Self): i32;\n" +
		"    function sides(self: Self): i32;\n" +
		"}\n" +
		"pub function describe(s: dyn Shape): i32 { return s.area() * 10 + s.sides(); }\n"
	if err := os.WriteFile(filepath.Join(proj, "shapes.fern"), []byte(shapes), 0o644); err != nil {
		t.Fatalf("write shapes: %v", err)
	}
	entry := imports.String() + "import \"./shapes\";\n" +
		"struct Square { side: i32 }\n" +
		"impl shapes.Shape for Square {\n" +
		"    function area(self: Self): i32 { return self.side * self.side; }\n" +
		"    function sides(self: Self): i32 { return 4; }\n" +
		"}\n" +
		"struct Triangle { base: i32, height: i32 }\n" +
		"impl shapes.Shape for Triangle {\n" +
		"    function area(self: Self): i32 { return (self.base * self.height) / 2; }\n" +
		"    function sides(self: Self): i32 { return 3; }\n" +
		"}\n" +
		"function main(): i32 {\n" +
		fmt.Sprintf("    let t: i32 = %s;\n", calls.String()) +
		"    let sq: dyn shapes.Shape = Square { side: 3 };\n" +
		"    let tr: dyn shapes.Shape = Triangle { base: 4, height: 5 };\n" +
		"    // 9*10+4 = 94 and 10*10+3 = 103: both arms, both slots.\n" +
		"    let d: i32 = shapes.describe(sq) + shapes.describe(tr);\n" +
		fmt.Sprintf("    if (t == %d && d == 197) { return 0; }\n", want) +
		"    if (d != 197) { return 2; }\n" +
		"    return 1;\n" +
		"}\n"
	entryPath := filepath.Join(proj, "entry.fern")
	if err := os.WriteFile(entryPath, []byte(entry), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	return entryPath
}

func TestSelfHostPerModuleConcatDynX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir, mmr := buildConcatDriver(t, gcc)
	entryPath := writeConcatDynFixture(t, dir)
	asm, err := exec.Command(mmr, entryPath).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("over-budget concat emit failed: %v (len=%d)", err, len(asm))
	}
	assertConcatProduced(t, asm)
	bin := buildBin(t, gcc, dir, "concat_dyn_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("per-module concat dyn program exited %d, want 0 (134 is the dispatch chain with no arm)", code)
	}
}

func TestSelfHostPerModuleConcatDynArm64(t *testing.T) {
	hostGcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host driver is built for x86-64-linux, so it must run on a native x86-64 host")
	}
	armGcc, qemu := arm64Tooling(t)
	dir, mmr := buildConcatDriver(t, hostGcc)
	entryPath := writeConcatDynFixture(t, dir)
	asm, err := exec.Command(mmr, entryPath, "-target", "arm64-linux").Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("arm64 over-budget concat emit failed: %v (len=%d)", err, len(asm))
	}
	assertConcatProduced(t, asm)
	bin := buildBin(t, armGcc, dir, "concat_dyn_prog_arm64", string(asm))
	rc := runArm64Bin(qemu, bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("arm64 per-module concat dyn program exited %d, want 0 (134 is the dispatch chain with no arm)", code)
	}
}
