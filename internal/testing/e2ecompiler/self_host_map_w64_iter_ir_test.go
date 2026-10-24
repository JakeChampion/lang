package e2ecompiler

import (
	"testing"
)

var mapW64IterIRCases = []struct {
	name string
	main string
}{
	{"forin-u64-shr", `import "core/map";
function main(): i32 { let m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; let acc: i32 = 0; for (k, v) in m { acc = acc + ((v >> 58) as i32); } return acc; }`},
	{"forin-i64-sum", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 1: 5000000007, 2: 6000000011 }; let acc: i64 = 0; for (k, v) in m { acc = acc + (v % 1000); } return acc as i32; }`},
	{"forin-values-i64", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 1: 5000000007 }; let acc: i32 = 0; for v in m.values() { acc = acc + ((v % 1000) as i32); } return acc; }`},
	{"forin-f64", `import "core/map";
function main(): i32 { let m: Map[i32, f64] = Map { 1: 2.5 }; let acc: f64 = 0.0; for (k, v) in m { acc = acc + v; } return (acc * 2.0) as i32; }`},
	{"forin-str-regress", `import "core/map";
function main(): i32 { let m: Map[i32, string] = Map { 1: "hello", 2: "xy" }; let acc: i32 = 0; for (k, v) in m { acc = acc + v.len() + k; } return acc; }`},
	{"forin-i32-regress", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = Map { 1: 10, 2: 20 }; let acc: i32 = 0; for (k, v) in m { acc = acc + v + k; } for k2 in m.keys() { acc = acc + k2; } return acc; }`},
}

func TestSelfHostMapW64IterIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range mapW64IterIRCases {
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
