package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const staticVariantProgram = `enum List { Nil, Cons(i32, List) }
enum Shape { Dot, Line(i32), Blank }

@noinline function length(xs: List, n: i32): i32 {
    return match (xs) { Cons(h, t) => length(t, n + 1), Nil => n };
}

@noinline function build(n: i32): List {
    let acc: List = Nil;
    let i: i32 = 0;
    while (i < n) { acc = Cons(i, acc); i = i + 1; }
    return acc;
}

@noinline function bump(xs: List): List {
    return match (xs) { Cons(h, t) => Cons(h + 1, bump(t)), Nil => Nil };
}

@noinline function weight(s: Shape): i32 {
    return match (s) { Dot => 1, Line(n) => n, Blank => 100 };
}

@noinline function pick(i: i32): Shape {
    if (i == 0) { return Dot; }
    if (i == 1) { return Line(20); }
    return Blank;
}

function main(): i32 {
    let total: i32 = length(bump(build(7)), 0) + length(Nil, 0);
    let i: i32 = 0;
    while (i < 3) { total = total + weight(pick(i)); i = i + 1; }
    return total;
}
`

// The test of a variant with no field compares the value with the address of
// the static box of the variant, so the walk of a list loads no tag word.
func TestSelfHostStaticVariantTestComparesAddresses(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "static_variant.fern")
	if err := os.WriteFile(src, []byte(staticVariantProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	tagLoads := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`-4\(%r\w+\)`),
		"arm64-linux":  regexp.MustCompile(`ldr w\d+, \[x\d+, #-4\]`),
		"arm64-darwin": regexp.MustCompile(`ldr w\d+, \[x\d+, #-4\]`),
	}
	for _, tg := range h.targets {
		tagLoad, ok := tagLoads[tg.target]
		if !ok {
			t.Fatalf("no tag load shape for %s", tg.target)
		}
		asmPath := filepath.Join(dir, "static_variant-"+tg.target+".s")
		h.compileWith(t, tg, src, asmPath, "-emit", "asm")
		asm, err := os.ReadFile(asmPath)
		if err != nil {
			t.Fatal(err)
		}
		fn := functionListing(string(asm), "__fn_length")
		if fn == "" {
			t.Fatalf("%s: no __fn_length in the listing:\n%s", tg.target, asm)
		}
		if tagLoad.MatchString(fn) {
			t.Errorf("%s: length loads a tag word:\n%s", tg.target, fn)
		}
		if !strings.Contains(fn, ".Kv") {
			t.Errorf("%s: length does not compare with a static box:\n%s", tg.target, fn)
		}
		// A value that is not Nil is a Cons, so the cell that bump takes over
		// needs no test of its shape and no call of the allocator.
		if bump := functionListing(string(asm), "__fn_bump"); bump == "" || strings.Contains(bump, "__fern_alloc_reuse") {
			t.Errorf("%s: bump tests the shape of the cell that it takes over:\n%s", tg.target, bump)
		}
		bin := filepath.Join(dir, "static_variant-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 128 {
			t.Errorf("%s: exit = %d, want 128 (7 + 0 from the lists, 1 + 20 + 100 from the shapes)", tg.target, code)
		}
	}
}

const staticVariantUnitLib = `pub enum List { Nil, Cons(i32, List) }

@noinline pub function empty(): List {
    return Nil;
}

@noinline pub function length(xs: List, n: i32): i32 {
    return match (xs) { Cons(h, t) => length(t, n + 1), Nil => n };
}
`

const staticVariantUnitMain = `import "./lib";

@noinline function local_empty(): lib.List {
    return lib.List.Nil;
}

@noinline function local_length(xs: lib.List, n: i32): i32 {
    return match (xs) { lib.List.Cons(h, t) => local_length(t, n + 1), lib.List.Nil => n };
}

function main(): i32 {
    let made_here: lib.List = lib.List.Cons(1, lib.List.Cons(2, local_empty()));
    let made_there: lib.List = lib.List.Cons(3, lib.empty());
    return lib.length(made_here, 0) * 10 + local_length(made_there, 0);
}
`

// Each unit emits the static box of a variant with no field under one name.
// The link of the unit files keeps one box, and so does the merge of the unit
// texts. A value that one unit makes and another unit tests then compares
// equal to that one box.
func TestSelfHostStaticVariantAcrossUnits(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	copySelfHostDriver(t, dir, "drivers/weak_dedupe_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "static-variant-units")
	merger := buildSelfHostBin(t, gcc, dir, "drivers/weak_dedupe_run.fern", "static-variant-merge")

	project := t.TempDir()
	for name, src := range map[string]string{
		"lib.fern":  staticVariantUnitLib,
		"main.fern": staticVariantUnitMain,
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(project, "main.fern")
	weakBox := regexp.MustCompile(`(?m)^\.weak \.Kv\d+$`)
	boxLabel := regexp.MustCompile(`(?m)^\.Kv\d+:$`)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			linker, qemu := gcc, ""
			if target == "arm64-linux" {
				linker, qemu = arm64Tooling(t)
			}
			drive := func(args ...string) []byte {
				t.Helper()
				cmd := runX86_64Bin(runner, driver, append([]string{entry, "-target", target}, args...)...)
				var stderr strings.Builder
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("driver %v: %v\n%s", args, err, stderr.String())
				}
				return out
			}
			run := func(bin string) int {
				t.Helper()
				cmd := runX86_64Bin(runner, bin)
				if target == "arm64-linux" {
					cmd = runArm64Bin(qemu, bin)
				}
				_, _ = cmd.CombinedOutput()
				return cmd.ProcessState.ExitCode()
			}
			n, err := strconv.Atoi(strings.TrimSpace(string(drive("-per-module-count"))))
			if err != nil || n != 2 {
				t.Fatalf("module count = %d: %v", n, err)
			}
			var needs []string
			for _, line := range strings.Split(string(drive("-per-module-needs")), "\n") {
				if root := strings.TrimSpace(line); root != "" {
					needs = append(needs, "-extra-need", root)
				}
			}
			var units []string
			withBox := 0
			for i := range n {
				unit := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needs...)...)
				if weakBox.Match(unit) {
					withBox++
				}
				path := filepath.Join(t.TempDir(), "unit.s")
				if err := os.WriteFile(path, unit, 0o644); err != nil {
					t.Fatal(err)
				}
				units = append(units, path)
			}
			if withBox != 2 {
				t.Fatalf("%d units emit a static box of a variant with no field, want 2", withBox)
			}

			linked := filepath.Join(t.TempDir(), "linked")
			args := append([]string{"-static", "-nostdlib", "-no-pie", "-o", linked}, units...)
			if out, err := exec.Command(linker, args...).CombinedOutput(); err != nil {
				t.Fatalf("link of the unit files: %v\n%s", err, out)
			}
			if code := run(linked); code != 21 {
				t.Errorf("linked unit files: exit = %d, want 21 (2 nodes counted in lib, 1 node counted in main)", code)
			}

			merged, err := runX86_64Bin(runner, merger, units...).Output()
			if err != nil {
				t.Fatalf("merge of the unit texts: %v", err)
			}
			defined := map[string]int{}
			for _, label := range boxLabel.FindAll(merged, -1) {
				defined[string(label)]++
			}
			if len(defined) == 0 {
				t.Errorf("the merged text defines no static box")
			}
			for label, count := range defined {
				if count != 1 {
					t.Errorf("the merged text defines %s %d times, want 1", label, count)
				}
			}
			stream := filepath.Join(t.TempDir(), "stream.s")
			if err := os.WriteFile(stream, merged, 0o644); err != nil {
				t.Fatal(err)
			}
			one := filepath.Join(t.TempDir(), "one")
			if out, err := exec.Command(linker, "-static", "-nostdlib", "-no-pie", "-o", one, stream).CombinedOutput(); err != nil {
				t.Fatalf("link of the merged text: %v\n%s", err, out)
			}
			if code := run(one); code != 21 {
				t.Errorf("merged text: exit = %d, want 21", code)
			}
		})
	}
}
