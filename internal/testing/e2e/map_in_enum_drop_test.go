package e2e

import (
	"os/exec"
	"testing"
)

const mapEnum = "enum J { N, S(string), O(Map[string, J]) }\n"

var mapInEnumDropCases = []struct {
	name string
	src  string
	want int
}{
	{"local-string",
		mapEnum + `function main(): i32 { let j: J = S("hi"); match (j) { S(s) => { return s.len(); }, _ => { return 0; } } }`, 2},
	{"array-string",
		mapEnum + `function main(): i32 { let xs: J[] = []; xs = xs.append(S("a")); xs = xs.append(S("bb")); return match (xs[1]) { S(s) => s.len(), _ => 0 - 1 }; }`, 2},
	{"real-map",
		"import \"core/map\";\n" + mapEnum + "function main(): i32 { let m: Map[string, J] = map_new(8); m = m.insert(\"k\", S(\"v\")); let o: J = O(m); match (o) { O(mm) => { return mm.len(); }, _ => { return 0; } } }", 1},
}

func TestX86_64MapInEnumDropBuilds(t *testing.T) {
	for _, tc := range mapInEnumDropCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestWASMMapInEnumDropBuilds(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm map-in-enum drop e2e")
	}
	for _, tc := range mapInEnumDropCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
