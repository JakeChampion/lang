package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var mapLiteralComputedKeyCases = []struct {
	name string
	src  string
}{
	{"two_computed_keys", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map { (1i32 + 1i32): 10i32, (2i32 + 2i32): 20i32 };
    return m.get_or(2i32, 0i32) + m.get_or(4i32, 0i32) + m.len();
}`},
	{"one_computed_key", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map { (1i32 + 1i32): 7i32 };
    return m.get_or(2i32, 0i32) + m.len();
}`},
	{"nested_shift_key", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map { (560i32 | ((372i32 & 865i32) <<| 422i32)): 5i32 };
    return m.get_or(2147483647i32, 0i32) + m.len();
}`},
	{"computed_string_key", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = Map { ("a" + "b"): 5i32 };
    return m.get_or("ab", 0i32) + m.len();
}`},
	{"cast_over_undecidable_operand", `import "core/map";
function k(): i32 { return 5i32; }
function j(): i32 { return 9i32; }
function main(): i32 {
    let m: Map[i32, i32] = Map { (k() as i32): 7i32, (j() as i32): 3i32 };
    return m.get_or(5i32, 0i32) + m.get_or(9i32, 0i32) + m.len();
}`},
	{"undecidable_key_string_annotation", `import "core/map";
function k(): string { return "z"; }
function main(): i32 {
    let m: Map[string, i32] = Map { k(): 4i32 };
    return m.get_or("z", 0i32) + m.len();
}`},
	{"undecidable_keys_from_annotation", `import "core/map";
function k(): i32 { return 3i32; }
function j(): i32 { return 8i32; }
function main(): i32 {
    let m: Map[i32, i32] = Map { k(): 6i32, j(): 4i32 };
    return m.get_or(3i32, 0i32) + m.get_or(8i32, 0i32) + m.len();
}`},
	{"if_expression_keys_from_annotation", `import "core/map";
function main(): i32 {
    let c: boolean = true;
    let m: Map[i32, i32] = Map {
        (if (c) { 11i32 } else { 12i32 }): 9i32,
        (if (c) { 21i32 } else { 22i32 }): 5i32
    };
    return m.get_or(11i32, 0i32) + m.get_or(21i32, 0i32) + m.len();
}`},
	{"neighbouring_literals_keep_own_kind", `import "core/map";
function k(): i32 { return 3i32; }
function main(): i32 {
    let outer: Map[i32, i32] = Map { k(): 5i32, (1i32 + 1i32): 6i32 };
    let inner: Map[string, i32] = Map { ("a" + "b"): 7i32 };
    return outer.get_or(3i32, 0i32) + outer.get_or(2i32, 0i32) + inner.get_or("ab", 0i32) + outer.len() + inner.len();
}`},
	{"unannotated_keeps_the_guess", `import "core/map";
function main(): i32 {
    let m = Map { (1i32 + 1i32): 7i32 };
    return m.get_or(2i32, 0i32) + m.len();
}`},

	{"sibling_literals_one_initialiser", `import "core/map";
function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 {
    let k0: i32 = 144i32;
    let k1: i32 = 145i32;
    let m: Map[i32, i32] = pick(false, Map { k0: 1i32 }, Map { k0: 6i32, k1: 4i32 });
    return m.get_or(144i32, 0i32) + m.get_or(145i32, 0i32) + m.len();
}`},
	{"ident_keys_in_return_position", `import "core/map";
function mk(k0: i32, k1: i32): Map[i32, i32] { return Map { k0: 6i32, k1: 4i32 }; }
function main(): i32 {
    let m: Map[i32, i32] = mk(144i32, 145i32);
    return m.get_or(144i32, 0i32) + m.get_or(145i32, 0i32) + m.len();
}`},
	{"ident_keys_as_call_argument", `import "core/map";
function take(m: Map[i32, i32]): i32 { return m.get_or(144i32, 0i32) + m.get_or(145i32, 0i32) + m.len(); }
function main(): i32 {
    let k0: i32 = 144i32;
    let k1: i32 = 145i32;
    return take(Map { k0: 6i32, k1: 4i32 });
}`},
	{"ident_keys_in_struct_field", `import "core/map";
struct S { m: Map[i32, i32] }
function main(): i32 {
    let k0: i32 = 144i32;
    let k1: i32 = 145i32;
    let s: S = S { m: Map { k0: 6i32, k1: 4i32 } };
    return s.m.get_or(144i32, 0i32) + s.m.get_or(145i32, 0i32) + s.m.len();
}`},
	{"param_keys_read_back_after_grow", `import "core/map";
function mk(a: i32, b: i32, c: i32): Map[i32, i32] { return Map { a: 1i32, b: 2i32, c: 3i32 }; }
function main(): i32 {
    let m: Map[i32, i32] = mk(10i32, 20i32, 30i32);
    return m.get_or(10i32, 0i32) + m.get_or(20i32, 0i32) + m.get_or(30i32, 0i32) + m.len();
}`},
	{"element_keys_from_int_array", `import "core/map";
function take(m: Map[i32, i32]): i32 { return m.get_or(3i32, 0i32) + m.get_or(4i32, 0i32) + m.len(); }
function main(): i32 {
    let xs: i32[] = [3i32, 4i32];
    return take(Map { xs[0i32]: 6i32, xs[1i32]: 4i32 });
}`},
	{"boxed_mutable_local_keys", `import "core/map";
struct S0 { n: i32, s: string }
function reads(m: Map[i32, i32]): (i32) => i32 { return ((x: i32) => (m.get_or(144i32, 0i32) + m.get_or(145i32, 0i32) + m.len())); }
function main(): i32 {
    let k0: i32 = 143i32;
    let k1: i32 = 144i32;
    k0 = k0 + 1i32;
    k1 = k1 + 1i32;
    let w: S0 = (if (true) { S0 { n: k0, s: "a" } } else { S0 { n: k1, s: "b" } });
    let f: (i32) => i32 = reads(Map { k0: 6i32, k1: 4i32 });
    return f(0i32) + w.n - 144i32;
}`},
	{"element_string_keys_stay_string", `import "core/map";
function take(m: Map[string, i32]): i32 { return m.get_or("ab", 0i32) + m.get_or("cd", 0i32) + m.len(); }
function main(): i32 {
    let ss: string[] = ["ab", "cd"];
    return take(Map { ss[0i32]: 6i32, ss[1i32]: 4i32 });
}`},
	{"arithmetic_keys_in_sibling_literal", `import "core/map";
function main(): i32 {
    let a: i32 = 5i32;
    let m: Map[i32, i32] = if (a < 0i32) { Map {} } else { Map { (a - (a ^ 726i32)): 7i32, -(a * 2i32): 3i32 } };
    return m.get_or(0i32 - 718i32, 0i32) + m.get_or(0i32 - 10i32, 0i32) + m.len();
}`},
	{"ident_string_keys_stay_string", `import "core/map";
function mk(k0: string, k1: string): Map[string, i32] { return Map { k0: 6i32, k1: 4i32 }; }
function main(): i32 {
    let m: Map[string, i32] = mk("ab", "cd");
    return m.get_or("ab", 0i32) + m.get_or("cd", 0i32) + m.len();
}`},
}

func TestSelfHostMapLiteralComputedKeyIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range mapLiteralComputedKeyCases {
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
			progBin := buildBin(t, gcc, dir, "maplitkey_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle) — a map literal with a "+
					"computed key built itself with the wrong key kind", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostMapLiteralComputedKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-literal key-kind wasm e2e")
	}
	interpBin := buildLangBinForInterp(t)
	l := newWasmStdlibLoader(t)

	for _, tc := range mapLiteralComputedKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			watFile := filepath.Join(t.TempDir(), "maplitkey_"+tc.name+".wat")
			if err := os.WriteFile(watFile, l.emit(t, tc.src), 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s exited %d, want %d (interp oracle) — a map literal built "+
					"itself with the wrong key kind", tc.name, got, want)
			}
		})
	}
}
