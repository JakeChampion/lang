package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostDynArrayAppendIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"emptyinit-append-index",
			`trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
function main(): i32 { let ds: (dyn Shape)[] = []; ds = ds.append(Sq { s: 3 }); return ds[0].area(); }`,
			9},
		{"litinit-append-read-original",
			`trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
function main(): i32 { let ds: (dyn Shape)[] = [Sq { s: 3 }]; ds = ds.append(Sq { s: 4 }); return ds[0].area() + ds[1].area(); }`,
			25},
		{"heterogeneous-append-loop",
			`trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
struct Rect { w: i32, h: i32 }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
function main(): i32 { let ds: (dyn Shape)[] = []; ds = ds.append(Sq { s: 3 }); ds = ds.append(Rect { w: 2, h: 5 }); let s: i32 = 0; let i: i32 = 0; while (i < ds.len()) { s = s + ds[i].area(); i = i + 1; } return s; }`,
			19},
		{"append-forin",
			`trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
function main(): i32 { let ds: (dyn Shape)[] = []; ds = ds.append(Sq { s: 3 }); ds = ds.append(Sq { s: 4 }); let s: i32 = 0; for d in ds { s = s + d.area(); } return s; }`,
			25},
		{"scalar-dyn-reassign-still-coerces",
			`trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
struct Rect { w: i32, h: i32 }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
function main(): i32 { let d: dyn Shape = Sq { s: 3 }; d = Rect { w: 2, h: 6 }; return d.area(); }`,
			12},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("%s: driver failed: %v", tc.name, err)
			}
			if !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s: did not lower through the IR (no .Lssa_ labels)", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var run *exec.Cmd
			if len(runner) == 0 {
				run = exec.Command(bin)
			} else {
				run = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
