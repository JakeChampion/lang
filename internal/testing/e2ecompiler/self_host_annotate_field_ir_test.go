package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateFieldCases = []struct {
	name string
	src  string
}{
	{"unsigned_fields", `struct Bits { hi: u64, lo: u32 }
function main(): i32 {
    let b: Bits = Bits { hi: 18446744073709551615, lo: 4000000000 };
    if ((b.hi >> 1) < 9000000000000000000) { return 1; }
    if ((b.lo >> 1) < 1000000000) { return 2; }
    return 42;
}`},
	{"i64_field_width", `struct Wide { n: i64 }
function main(): i32 {
    let w: Wide = Wide { n: 5000000000 };
    let v: i64 = w.n + 1;
    if (v == 5000000001) { return 42; }
    return 7;
}`},
	{"f64_fields", `struct Pt { x: f64, y: f64 }
function main(): i32 {
    let p: Pt = Pt { x: 1.5, y: 2.5 };
    let s: f64 = p.x * p.y;
    if (s == 3.75) { return 42; }
    return 7;
}`},
	{"nested_struct_field", `struct Inner { n: i64 }
struct Outer { inn: Inner }
function main(): i32 {
    let o: Outer = Outer { inn: Inner { n: 5000000000 } };
    if (o.inn.n == 5000000000) { return 42; }
    return 7;
}`},
	{"map_field", `import "core/map";

struct Reg { caps: Map[string, i32] }

function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("a", 40);
    let r: Reg = Reg { caps: m };
    return r.caps.get_or("a", 0) + 2;
}`},
	{"tuple_elem_struct_field", `struct P { v: u64 }
function main(): i32 {
    let t: (P, i32) = (P { v: 18446744073709551615 }, 3);
    if ((t.0.v >> 1) > 9000000000000000000) { return 42; }
    return 7;
}`},
}

func TestSelfHostAnnotateFieldIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)
	for _, tc := range annotateFieldCases {
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
			progBin := buildBin(t, gcc, dir, "annfield_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
