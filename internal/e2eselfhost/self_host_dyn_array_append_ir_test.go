package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostDynArrayAppendIR pins `(dyn Trait)[]` element dispatch after an
// append REASSIGN (`ds = ds.append(x)`) on the self-host x86-64 IR path.
//
// A dyn-array slot (`var ds: (dyn Shape)[]`) is marked with struct_type
// "dyn Shape" — the ELEMENT type, the `[]` stripped at its init/param bind — so
// it is indistinguishable from a SCALAR dyn slot (`var d: dyn Shape`) by the
// type string alone. lower_stmt_assign's scalar-dyn coercion keyed only on
// `struct_type[0:4]=="dyn " && !is_array_type_name`, so `ds = ds.append(x)`
// coerced the whole grown ARRAY into a single dyn cell [shape, array] and stored
// THAT into ds. Every later `ds[i].method()` then read a garbage self: the
// single-element case returned 0 (self.field read the shape word), the
// multi-element case SIGSEGV'd. Array literals (`[Sq{..}]`) were unaffected — the
// bug was specific to the append reassign. Native x86-64 codegen + the
// interpreter were always correct.
//
// Fix: gate the scalar-dyn coercion on `!is_arr_slot(slot)` so a dyn-array
// reassign falls through to the array append path (which already coerces each
// appended element to a dyn cell). The append value coercion was never the bug.
//
// Value probe (no crash reliance): area(Sq{3})=9, area(Rect{2,5})=10; the loop
// sums 19. The empty-init + two heterogeneous appends + index dispatch is the
// exact shape that returned 0 / SIGSEGV'd before the fix.
func TestSelfHostDynArrayAppendIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		// Empty-init + append + single-element index dispatch (returned 0).
		{"emptyinit-append-index",
			`trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
function main(): i32 { var ds: (dyn Shape)[] = []; ds = ds.append(Sq { s: 3 }); return ds[0].area(); }`,
			9},
		// Literal-init + append; reading the ORIGINAL element [0] broke too
		// (the whole ds was replaced by a dyn cell wrapping the array).
		{"litinit-append-read-original",
			`trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
function main(): i32 { var ds: (dyn Shape)[] = [Sq { s: 3 }]; ds = ds.append(Sq { s: 4 }); return ds[0].area() + ds[1].area(); }`,
			25},
		// Heterogeneous two-impl appends + index-loop dispatch (SIGSEGV'd).
		{"heterogeneous-append-loop",
			`trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
struct Rect { w: i32, h: i32 }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
function main(): i32 { var ds: (dyn Shape)[] = []; ds = ds.append(Sq { s: 3 }); ds = ds.append(Rect { w: 2, h: 5 }); var s: i32 = 0; var i: i32 = 0; while (i < ds.len()) { s = s + ds[i].area(); i = i + 1; } return s; }`,
			19},
		// for-in over an appended dyn array.
		{"append-forin",
			`trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
function main(): i32 { var ds: (dyn Shape)[] = []; ds = ds.append(Sq { s: 3 }); ds = ds.append(Sq { s: 4 }); var s: i32 = 0; for d in ds { s = s + d.area(); } return s; }`,
			25},
		// Regression guard: a SCALAR dyn reassign must STILL coerce.
		{"scalar-dyn-reassign-still-coerces",
			`trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
struct Rect { w: i32, h: i32 }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
function main(): i32 { var d: dyn Shape = Sq { s: 3 }; d = Rect { w: 2, h: 6 }; return d.area(); }`,
			12},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("%s: driver failed: %v", tc.name, err)
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s: fell back to the AST path (no .Lir_ labels)", tc.name)
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
