package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var tupleArrayFieldCases = []struct {
	name string
	src  string
}{
	{"tuple_array_field_f64", `struct P { ts: (i32, f64)[] }
function main(): i32 {
    let p: P = P { ts: [(2, 4.5)] };
    return p.ts[0].0 + (p.ts[0].1 * 10.0) as i32;
}`},
	{"tuple_array_callfield_f64", `struct P { ts: (i32, f64)[] }
function getp(): P { return P { ts: [(2, 4.5)] }; }
function main(): i32 {
    return getp().ts[0].0 + (getp().ts[0].1 * 10.0) as i32;
}`},
	{"tuple_array_field_i64", `struct P { ts: (i32, i64)[] }
function main(): i32 {
    let p: P = P { ts: [(7, 5000000000)] };
    return p.ts[0].0 + (p.ts[0].1 / 1000000000) as i32;
}`},
	{"tuple_array_field_nested", `struct P { ts: (i32, (f64, i32))[] }
function main(): i32 {
    let p: P = P { ts: [(2, (4.5, 3))] };
    return p.ts[0].0 + (p.ts[0].1.0 * 10.0) as i32 + p.ts[0].1.1;
}`},
	{"tuple_array_field_i32_narrow", `struct P { ts: (i32, i32)[] }
function main(): i32 {
    let p: P = P { ts: [(30, 12)] };
    return p.ts[0].0 + p.ts[0].1;
}`},
	{"tuple_array_local_f64", `function mk(): (i32, f64)[] { return [(2, 4.5)]; }
function main(): i32 {
    let ps: (i32, f64)[] = mk();
    return ps[0].0 + (ps[0].1 * 10.0) as i32;
}`},
	{"tuple_array_annotated_bind_f64", `struct P { ts: (i32, f64)[] }
function main(): i32 {
    let p: P = P { ts: [(2, 4.5)] };
    let f: f64 = p.ts[0].1;
    return p.ts[0].0 + (f * 10.0) as i32;
}`},
}

func TestSelfHostTupleArrayFieldIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range tupleArrayFieldCases {
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
			progBin := buildBin(t, gcc, dir, "tuparrfield_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
