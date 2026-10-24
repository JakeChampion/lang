package e2ecompiler

import (
	"testing"
)

var mapW64RecvIRCases = []struct {
	name string
	main string
}{
	{"structfield-u64-getor-shr", `import "core/map";
struct C { m: Map[i32, u64] }
function main(): i32 { let c: C = C { m: Map { 1: 18000000000000000000 as u64 } }; return (c.m.get_or(1, 0 as u64) >> 58) as i32; }`},
	{"unannot-u64-getor-shr", `import "core/map";
function main(): i32 { let m = Map { 1: 18000000000000000000 as u64 }; return (m.get_or(1, 0 as u64) >> 58) as i32; }`},
	{"u64-getor-cmp", `import "core/map";
function main(): i32 { let m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; if (m.get_or(1, 0 as u64) > (100 as u64)) { return 7; } return 9; }`},
	{"strkey-u64-overwrite", `import "core/map";
function main(): i32 { let m: Map[string, u64] = Map { "a": 1 as u64 }; m = m.insert("k" + "ey", 18000000000000000000 as u64); m = m.insert("k" + "ey", 18000000000000000000 as u64); return (m.get_or("key", 0 as u64) >> 58) as i32; }`},
	{"i64-negative-value", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 1: 0 }; m = m.insert(1, 0 - 3); let g: i64 = m.get_or(1, 0); if (g == (0 - 3)) { return 5; } return 6; }`},
	{"i32-regress", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = Map { 1: 10, 2: 20 }; m = m.insert(3, 30); let ks = m.keys(); let vs = m.values(); return m.get_or(1, 0) + m.get_or(9, 5) + ks.len() + vs.len(); }`},
	{"strval-regress", `import "core/map";
function main(): i32 { let m: Map[string, string] = Map { "a": "hello" }; return m.get_or("a", "x").len() + m.get_or("z", "yy").len(); }`},
	{"f64-getor-mul", `import "core/map";
function main(): i32 { let m: Map[i32, f64] = Map { 1: 2.5 }; return (m.get_or(1, 0.0) * 2.0) as i32; }`},
	{"f64-unannot-getor", `import "core/map";
function main(): i32 { let m = Map { 1: 2.5 }; return (m.get_or(1, 0.0) * 2.0) as i32; }`},
	{"f64-structfield-getor", `import "core/map";
struct C { m: Map[i32, f64] }
function main(): i32 { let c: C = C { m: Map { 1: 2.5 } }; return (c.m.get_or(1, 0.0) * 2.0) as i32; }`},
	{"f64-default-miss", `import "core/map";
function main(): i32 { let m: Map[i32, f64] = Map { 9: 1.0 }; return (m.get_or(1, 3.25) * 2.0) as i32; }`},
}

func TestSelfHostMapW64RecvIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range mapW64RecvIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				src := []byte(tc.main + "\n")
				want := interpExit(t, interpBin, string(src))
				if stderr, code := cli.exitOf(t, string(src), target); code != want {
					t.Errorf("%s exited %d, want %d (interp oracle)\n%s", tc.name, code, want, stderr)
				}
			})
		}
	}
}
