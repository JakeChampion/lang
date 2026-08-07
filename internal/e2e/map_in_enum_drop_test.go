package e2e

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Regression for #4425: dropping a value of a Map-transitively-containing enum
// (the built-in JsonValue, whose JObject variant carries a Map[string,
// JsonValue]) must not emit a call to __map_drop_values in the generated
// drop glue. That helper lives in core/map.fern and is loaded ONLY when a program
// imports "core/map" (the checker requires the import for map *operations*) —
// but a program can use JsonValue WITHOUT any map operation (e.g. a local /
// array of JString values). The whole-enum drop glue still emits the JObject
// arm, so the reference was to an unloaded symbol: a hard wasm "unknown callee
// __map_drop_values" build error (and native "undefined label").
//
// Fix: the enum drop skips the Map payload reclaim (a documented safe leak —
// the enum is already excluded from EnumRcPayloads, ir.go ~9085), across both
// enum-drop paths (genEnumDropFn and emitEnumSlotDrop's inline variant plan).
// The map's buffer + values leak; nothing dangles. These pin that the affected
// shapes BUILD and run correctly on both the native x86-64 and wasm backends.
var mapInEnumDropCases = []struct {
	name string
	src  string
	want int
}{
	// A local JsonValue bound to a payload variant, dropped at scope exit — the
	// inline emitEnumSlotDrop path. No `import "core/map"`.
	{"local-jstring",
		`function main(): i32 { var j: JsonValue = JString("hi"); match (j) { JString(s) => { return s.len(); }, _ => { return 0; } } }`, 2},
	// A JsonValue[] element drop — the genEnumDropFn (__drop_enum_JsonValue)
	// path, reached through the array-buffer deep drop. No `import "core/map"`.
	// (This is the TestWASMArrayPushEnum shape; pinned here on x86-64 too.)
	{"array-jstring",
		`function main(): i32 { var xs: JsonValue[] = []; xs = xs.append(JString("a")); xs = xs.append(JString("bb")); return match (xs[1]) { JString(s) => s.len(), _ => 0 - 1 }; }`, 2},
	// A REAL JObject wrapping a populated map, dropped — exercises the map-payload
	// arm with an actual map present (import "core/map" IS loaded here, so
	// __map_drop_values exists, but the enum drop still safe-leaks it). Must build
	// and return the map length.
	{"real-jobject-map",
		"import \"core/map\";\nfunction main(): i32 { var m: Map[string, JsonValue] = map_new(8); m = m.insert(\"k\", JString(\"v\")); var o: JsonValue = JObject(m); match (o) { JObject(mm) => { return mm.len(); }, _ => { return 0; } } }", 1},
}

func TestX86_64MapInEnumDropBuilds(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, tc := range mapInEnumDropCases {
		t.Run(tc.name, func(t *testing.T) {
			// buildComponent (inside runWasm) t.Fatal's on the "unknown callee
			// __map_drop_values" wasmbin.Build error, so a returned code means the
			// module assembled.
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
