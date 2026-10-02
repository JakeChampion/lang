package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// TestSelfHostPerModuleDynDispatchCrossModule: a `dyn` call in one unit
// dispatches to impls declared in other units, with the trait in a fourth. A
// unit used to build its dispatch arms from its own functions only, so the
// call had no arms and aborted with 134 (#10891).
func TestSelfHostPerModuleDynDispatchCrossModule(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_modload_run.fern", "dyndispatchdriver")

	proj := t.TempDir()
	mustWrite(t, proj, "shape.fern", `pub trait Area {
    function area(self: Self): i32;
}
`)
	mustWrite(t, proj, "square.fern", `import "./shape";

pub struct Square { side: i32 }

impl shape.Area for Square {
    function area(self: Self): i32 { return self.side * self.side; }
}

pub function make(n: i32): Square { return Square { side: n }; }
`)
	mustWrite(t, proj, "measure.fern", `import "./shape";

pub function total(a: dyn shape.Area, b: dyn shape.Area): i32 {
    return a.area() + b.area();
}
`)
	mustWrite(t, proj, "main.fern", `import "./shape";
import "./square";
import "./measure";

struct Rect { w: i32, h: i32 }

impl shape.Area for Rect {
    function area(self: Self): i32 { return self.w * self.h; }
}

function main(): i32 {
    let a: dyn shape.Area = square.make(3);
    let b: dyn shape.Area = Rect { w: 2, h: 5 };
    return measure.total(a, b);
}
`)
	entry := filepath.Join(proj, "main.fern")

	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			gcc := x86gcc
			var qemu string
			if target == "arm64-linux" {
				gcc, qemu = arm64Tooling(t)
			}
			outDir := filepath.Join(proj, target)
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if out, err := runX86_64Bin(x86runner, driverBin, entry, "-target", target, "-per-module-emit-all", "-out-dir", outDir).CombinedOutput(); err != nil {
				t.Fatalf("emit-all: %v\n%s", err, out)
			}
			ents, err := os.ReadDir(outDir)
			if err != nil {
				t.Fatal(err)
			}
			var objs []string
			for _, e := range ents {
				objs = append(objs, filepath.Join(outDir, e.Name()))
			}
			sort.Strings(objs)
			bin := filepath.Join(proj, target+"_prog")
			linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
			if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
				t.Fatalf("per-module link: %v\n%s", err, lout)
			}
			var cmd *exec.Cmd
			if target == "arm64-linux" {
				cmd = runArm64Bin(qemu, bin)
			} else {
				cmd = runX86_64Bin(x86runner, bin)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 19 {
				t.Errorf("per-module %s program exited %d, want 19 (9 + 10)", target, code)
			}
		})
	}
}
