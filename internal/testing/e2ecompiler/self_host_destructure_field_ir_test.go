package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var destructureFieldCases = []struct {
	name string
	src  string
}{
	{"destructure_field_f64", `struct P { t: (i32, f64) }
function main(): i32 {
    let p: P = P { t: (7, 2.5) };
    let (a, b) = p.t;
    return a + (b * 10.0) as i32;
}`},
	{"destructure_field_i64", `struct P { w: (i64, i32) }
function main(): i32 {
    let p: P = P { w: (5000000000, 7) };
    let (a, b) = p.w;
    return (a / 1000000000) as i32 + b;
}`},
	{"destructure_nested_field_f64", `struct Inner { t: (i32, f64) }
struct P { q: Inner }
function main(): i32 {
    let p: P = P { q: Inner { t: (9, 4.5) } };
    let (a, b) = p.q.t;
    return a + (b * 10.0) as i32;
}`},
	{"destructure_receiver_field_f64", `struct P { t: (i32, f64) }
function (p: P) sum(): i32 {
    let (a, b) = p.t;
    return a + (b * 10.0) as i32;
}
function main(): i32 { let p: P = P { t: (7, 2.5) }; return p.sum(); }`},
	{"destructure_field_string", `struct P { t: (i32, string) }
function main(): i32 {
    let p: P = P { t: (7, "abcd") };
    let (a, b) = p.t;
    return a + b.len();
}`},
	{"destructure_field_i32_narrow", `struct P { t: (i32, i32) }
function main(): i32 {
    let p: P = P { t: (30, 12) };
    let (a, b) = p.t;
    return a + b;
}`},
	{"destructure_call_f64", `function mk(): (i32, f64) { return (7, 2.5); }
function main(): i32 { let (a, b) = mk(); return a + (b * 10.0) as i32; }`},
	{"destructure_local_f64", `function main(): i32 {
    let t: (i32, f64) = (7, 2.5);
    let (a, b) = t;
    return a + (b * 10.0) as i32;
}`},
}

func TestSelfHostDestructureFieldIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range destructureFieldCases {
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
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR path)", tc.name, got)
			}

			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "destrfield_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
