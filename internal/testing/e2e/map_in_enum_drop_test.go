package e2e

import (
	"os/exec"
	"testing"
)

// Regression for #4425: dropping a value of a Map-transitively-containing enum
// (`J` below, whose `O` variant carries a Map[string, J]) must not emit a call
// to __map_drop_values in the generated drop glue. That helper lives in
// core/map.fern and is loaded ONLY when a program imports "core/map" (the
// checker requires the import for map *operations*) — but a program can use
// such an enum WITHOUT any map operation (e.g. a local / array of `S` values).
// The whole-enum drop glue still emits the `O` arm, so the reference was to an unloaded symbol: a hard wasm "unknown callee
// __map_drop_values" build error (and native "undefined label").
//
// Fix: the enum drop skips the Map payload reclaim (a documented safe leak —
// the enum's Map payload is not reclaimed), across both
// enum-drop paths (genEnumDropFn and emitEnumSlotDrop's inline variant plan).
// The map's buffer + values leak; nothing dangles. These pin that the affected
// shapes BUILD and run correctly on both the native x86-64 and wasm backends.
const mapEnum = "enum J { N, S(string), O(Map[string, J]) }\n"

var mapInEnumDropCases = []struct {
	name string
	src  string
	want int
}{
	// A local J bound to a payload variant, dropped at scope exit — the
	// inline emitEnumSlotDrop path. No `import "core/map"`.
	{"local-string",
		mapEnum + `function main(): i32 { let j: J = S("hi"); match (j) { S(s) => { return s.len(); }, _ => { return 0; } } }`, 2},
	// A J[] element drop — the genEnumDropFn path, reached through the
	// array-buffer deep drop. No `import "core/map"`.
	{"array-string",
		mapEnum + `function main(): i32 { let xs: J[] = []; xs = xs.append(S("a")); xs = xs.append(S("bb")); return match (xs[1]) { S(s) => s.len(), _ => 0 - 1 }; }`, 2},
	// A REAL O wrapping a populated map, dropped — exercises the map-payload
	// arm with an actual map present (import "core/map" IS loaded here, so
	// __map_drop_values exists). Must build and return the map length.
	{"real-map",
		"import \"core/map\";\n" + mapEnum + "function main(): i32 { let m: Map[string, J] = map_new(8); m = m.insert(\"k\", S(\"v\")); let o: J = O(m); match (o) { O(mm) => { return mm.len(); }, _ => { return 0; } } }", 1},
}

func TestX86_64MapInEnumDropBuilds(t *testing.T) {
	for _, tc := range mapInEnumDropCases {
		t.Run(tc.name, func(t *testing.T) {
			// A prior bug made this fail at assemble time ("undefined label
			// __map_drop_values"); compileAndRunX86_64 t.Fatal's on that, so
			// reaching the exit-code check means it built.
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
			// buildWasmCore (inside runWasm) t.Fatal's on the "unknown callee
			// __map_drop_values" wasmbin.Build error, so a returned code means the
			// module assembled.
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
