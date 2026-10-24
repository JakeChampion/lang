package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var annotateEnumCases = []struct {
	name string
	src  string
}{
	{"enum_array_ret_index_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function mk(): Color[] { return [Color.Green]; }
function main(): i32 { return mk()[0].rank(); }`},
	{"enum_slice_index_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function main(): i32 {
    let cs: Color[] = [Color.Red, Color.Green];
    return cs[1:2][0].rank();
}`},
	{"enum_struct_field_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
struct R { c: Color }
function main(): i32 { let r: R = R { c: Color.Green }; return r.c.rank(); }`},
	{"enum_tuple_elem_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function main(): i32 { let t: (Color, i32) = (Color.Green, 1); return t.0.rank(); }`},
	{"enum_fn_value_call_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function get(): Color { return Color.Green; }
function mkf(): () => Color { return get; }
function main(): i32 { return mkf()().rank(); }`},
	{"qualified_unit_variant_array_index", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function main(): i32 { return [Color.Red, Color.Green][1].rank(); }`},
	{"enum_iife_array_index_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function main(): i32 {
    let b: boolean = true;
    return (if (b) { [Color.Green] } else { [Color.Red] })[0].rank();
}`},

	{"struct_array_ret_index_method", `struct P { v: i32 }
function (p: P) rank(): i32 { return p.v; }
function mk(): P[] { return [P { v: 42 }]; }
function main(): i32 { return mk()[0].rank(); }`},
	{"scalar_ret_call_method", `function w(): u64 { return 4294967338u64; }
function main(): i32 { return (w() % 100u64) as i32; }`},
	{"dyn_struct_field_method", `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
struct Holder { sh: dyn Shape }
function main(): i32 { let h: Holder = Holder { sh: Sq { s: 6 } }; return h.sh.area() + 6; }`},
	{"scalar_struct_field_method", `struct B { v: f64 }
function main(): i32 { let b: B = B { v: 4.2 }; return (b.v * 10.0) as i32; }`},
	{"shared_variant_name_value", `enum A { Zed, Xx(i32) }
enum B { Yy(i32), Zed }
function (a: A) rank(): i32 {
    match (a) { A.Zed => { return 40; }, A.Xx(n) => { return n; } }
    return 0;
}
function (b: B) rank(): i32 {
    match (b) { B.Zed => { return 42; }, B.Yy(n) => { return n; } }
    return 0;
}
function main(): i32 { let v: B = B.Zed; return v.rank(); }`},
	{"bare_variant_payload_method", `enum Color { Red, Green(i32), Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green(v) => { return v; }, _ => { return 3; } }
    return 0;
}
function main(): i32 {
    let b: boolean = true;
    return (if (b) { [Green(42)] } else { [Red] })[0].rank();
}`},
}

func TestSelfHostAnnotateEnumIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateEnumCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}

			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "annenum_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostAnnotateEnumWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping annotate-enum wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateEnumCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot)
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
