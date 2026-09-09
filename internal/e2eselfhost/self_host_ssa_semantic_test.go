package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const semanticFixture = `
var records: semrecords.Record[] = [];
var i32t: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false };
var i64t: typeinfo.Type = typeinfo.TypeI32 { width: 64, unsigned: false, is_char: false };
var bt: typeinfo.Type = typeinfo.TypeBool { tag: 0 };
var st: typeinfo.Type = typeinfo.TypeString { tag: 0 };
var view: typeinfo.Type = typeinfo.TypeString { tag: 1 };
var sa: typeinfo.Type = typeinfo.TypeArray { elem: st };
var saa: typeinfo.Type = typeinfo.TypeArray { elem: sa };
var root: typeinfo.Type = typeinfo.TypeTuple { elements: [saa, bt] };
var ia: typeinfo.Type = typeinfo.TypeArray { elem: i32t };
var pair: typeinfo.Type = typeinfo.TypeTuple { elements: [st, i32t] };
var types: typeinfo.Type[] = [root, i32t, saa, sa, st, st, sa, bt, ia, i32t, pair];
var params: typeinfo.Type[] = [root, i32t];
var result: typeinfo.Type = st;
var insts: ssa.SInst[] = [
    inst(6, 0, [], 0), inst(6, 1, [], 1),
    inst(ssasem.tuple_get(), 2, [0], 0),
    inst(ssasem.array_get(), 3, [2, 1], 0),
    inst(ssasem.array_get(), 4, [3, 1], 0),
    inst(7, 5, [4], 0), inst(ssasem.array_new(), 6, [5], 0),
    inst(ssasem.tuple_get(), 7, [0], 1),
    inst(ssasem.array_new(), 8, [1], 0),
    inst(ssasem.array_get(), 9, [8, 1], 0),
    inst(ssasem.tuple_new(), 10, [5, 9], 0)
];
var graph = ssa.SFunc { name: "semantic", nparams: 2, nvals: 11, entry: 7, takes_env: false,
    blocks: [ssa.SBlock { id: 7, insts: insts, preds: [], term: ret(5) }] };
`

const semanticHelpers = `
function field(value: i32, parent: i32, index: i32, name: string): ssa.SInst {
    return ssa.SInst { kind_tag: ssasem.record_get(), result: value, args: [parent], imm: index, str: name };
}
function inst(kind: i32, value: i32, args: i32[], imm: i32): ssa.SInst {
    return ssa.SInst { kind_tag: kind, result: value, args: args, imm: imm, str: "" };
}
function ret(value: i32): ssa.STerm { return ssa.STerm { kind_tag: 1, value: value, cond: 0, target: 0, t: 0, f: 0 }; }
function br(target: i32): ssa.STerm { return ssa.STerm { kind_tag: 2, value: 0, cond: 0, target: target, t: 0, f: 0 }; }
function branch(): ssa.STerm { return ssa.STerm { kind_tag: 3, value: 0, cond: 0, target: 0, t: 17, f: 27 }; }
function change(g: ssa.SFunc, at: i32, ins: ssa.SInst): ssa.SFunc {
    var b = g.blocks[0];
    b = ssa.SBlock { ...b, insts: b.insts.with(at, ins) };
    return ssa.SFunc { ...g, blocks: g.blocks.with(0, b) };
}
function type_checks(): i32 {
    var i: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false };
    var wide: typeinfo.Type = typeinfo.TypeI32 { width: 64, unsigned: false, is_char: false };
    var u: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: true, is_char: false };
    var c: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: true };
    var s: typeinfo.Type = typeinfo.TypeString { tag: 0 };
    var v: typeinfo.Type = typeinfo.TypeString { tag: 1 };
    var f: typeinfo.Type = typeinfo.TypeFloat { width: 64, polymorphic: false };
    var p: typeinfo.Type = typeinfo.TypeFloat { width: 64, polymorphic: true };
    var n: typeinfo.Type = typeinfo.TypeStruct { name: "Box", args: [i] };
    var nw: typeinfo.Type = typeinfo.TypeStruct { name: "Box", args: [wide] };
    var un: typeinfo.Type = typeinfo.TypeUnion { name: "Option", args: [i] };
    var unw: typeinfo.Type = typeinfo.TypeUnion { name: "Option", args: [wide] };
    var sig: typeinfo.Type = typeinfo.TypeFunc { param_types: [s], ret_type: f, params_known: true };
    var opaque: typeinfo.Type = typeinfo.TypeFunc { param_types: [s], ret_type: f, params_known: false };
    var sv: typeinfo.Type = typeinfo.TypeFunc { param_types: [v], ret_type: f, params_known: true };
    var tuple: typeinfo.Type = typeinfo.TypeTuple { elements: [n, sig] };
    var tuple2: typeinfo.Type = typeinfo.TypeTuple { elements: [nw, sig] };
    var map: typeinfo.Type = typeinfo.TypeMap { key: s, value: un };
    var map2: typeinfo.Type = typeinfo.TypeMap { key: v, value: un };
    if (semtypes.equal(i, wide) || semtypes.equal(i, u) || semtypes.equal(i, c)) { return 1; }
    if (semtypes.equal(s, v) || semtypes.equal(f, p) || typeinfo.spelling(f) != typeinfo.spelling(p)) { return 2; }
    if (semtypes.equal(n, nw) || semtypes.equal(un, unw) || semtypes.equal(n, un)) { return 3; }
    if (semtypes.equal(sig, opaque) || semtypes.equal(sig, sv) || semtypes.equal(tuple, tuple2) || semtypes.equal(map, map2)) { return 4; }
    var array: typeinfo.Type = typeinfo.TypeArray { elem: tuple };
    if (!semtypes.equal(array, array) || !semtypes.equal(map, map) || !semtypes.concrete(array, false)) { return 5; }
    if (semtypes.concrete(p, false) || semtypes.concrete(opaque, false) || semtypes.equal(typeinfo.unchecked(), typeinfo.unchecked())) { return 6; }
    return 0;
}
`

const semanticPhi = `
params = [bt, st, st]; types = [bt, st, st, st]; result = st;
graph = ssa.SFunc { name: "phi", nparams: 3, nvals: 4, entry: 7, takes_env: false, blocks: [
    ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1), inst(6, 2, [], 2)], term: branch() },
    ssa.SBlock { id: 27, preds: [7], insts: [], term: br(37) },
    ssa.SBlock { id: 17, preds: [7], insts: [], term: br(37) },
    ssa.SBlock { id: 37, preds: [17, 27], insts: [inst(8, 3, [1, 2], 0)], term: ret(3) }
] };
`

func semanticCases() []struct{ name, change, want string } {
	base := []struct{ name, change, want string }{
		{"nested-projections", "", ""},
		{"phi", semanticPhi, ""},
		{"phi-view-mismatch", semanticPhi + "types = types.with(2, view); params = params.with(2, view);", "copy or phi type"},
		{"branch-needs-bool", semanticPhi + "types = types.with(0, i32t); params = params.with(0, i32t);", "condition type"},
		{"string-view-mismatch", "types = types.with(5, view);", "copy or phi type"},
		{"projection-result", "types = types.with(4, view);", "array projection type"},
		{"projection-arity", "graph = change(graph, 3, inst(ssasem.array_get(), 3, [2], 0));", "array projection arity"},
		{"projection-container", "graph = change(graph, 3, inst(ssasem.array_get(), 3, [0, 1], 0));", "array container type"},
		{"projection-index", "types = types.with(1, i64t); params = params.with(1, i64t);", "array index type"},
		{"tuple-index-negative", "graph = change(graph, 2, inst(ssasem.tuple_get(), 2, [0], 0 - 1));", "tuple projection index"},
		{"tuple-index-large", "graph = change(graph, 2, inst(ssasem.tuple_get(), 2, [0], 2));", "tuple projection index"},
		{"array-element", "graph = change(graph, 6, inst(ssasem.array_new(), 6, [1], 0));", "array element type"},
		{"tuple-element", "graph = change(graph, 10, inst(ssasem.tuple_new(), 10, [9, 5], 0));", "tuple element type"},
		{"tuple-arity", "graph = change(graph, 10, inst(ssasem.tuple_new(), 10, [5], 0));", "tuple construction arity"},
		{"parameter-type", "params = params.with(1, i64t);", "parameter type or identity"},
		{"missing-parameter", "graph = change(graph, 1, inst(1, 1, [], 0));", "missing parameter definition"},
		{"return-type", "result = view;", "return type"},
		{"missing-return", "var b = graph.blocks[0]; b = ssa.SBlock { ...b, term: ret(0 - 1) }; graph = ssa.SFunc { ...graph, blocks: [b] };", "missing return value"},
		{"unknown-type", "types = types.with(5, typeinfo.unchecked());", "unresolved value type"},
		{"polymorphic-type", "types = types.with(5, typeinfo.TypeFloat { width: 64, polymorphic: true });", "unresolved value type"},
		{"opaque-signature", "types = types.with(5, typeinfo.TypeFunc { param_types: [], ret_type: st, params_known: false });", "unresolved value type"},
		{"physical-allocation", "graph = change(graph, 6, inst(14, 6, [5], 0));", "unsupported semantic operation"},
		{"physical-load", "graph = change(graph, 4, inst(15, 4, [3, 1], 32));", "unsupported semantic operation"},
	}
	return append(base, semanticRecordCases()...)
}

func semanticSource(indices []int) (string, string) {
	var source, main, want strings.Builder
	source.WriteString("import \"./ssa\";\nimport \"./ssasem\";\nimport \"./semtypes\";\nimport \"./semrecords\";\nimport \"./typeinfo\";\n")
	source.WriteString(semanticHelpers)
	main.WriteString("function main(): i32 { if (type_checks() != 0) { return 90; }\n")
	for _, i := range indices {
		tc := semanticCases()[i]
		fmt.Fprintf(&source, "function semantic_case_%d(): i32 {\n%s\n%s\n", i, semanticFixture, tc.change)
		source.WriteString(`
var before = ssa.print_func(graph);
var checked = ssasem.analyze(ssasem.Func { graph: graph, values: types, params: params, result: result, records: records });
if (checked.ok != (checked.why == "") || checked.flow.ok != checked.ok) { return 2; }
if (before != ssa.print_func(graph)) { return 3; }
if (!checked.ok && (checked.dependencies.len() != 0 || checked.flow.live_in.len() != 0 || checked.flow.live_out.len() != 0)) { return 4; }
`)
		if i == 0 {
			source.WriteString(`
if (!checked.ok) { print(checked.why); return 5; }
var expected: i32[][] = [[], [], [0], [2, 0], [3, 2, 0], [4, 3, 2, 0], [], [], [], [], []];
var at: i32 = 0;
while (at < expected.len()) {
    if (checked.dependencies[at].len() != expected[at].len()) { return 6; }
    var dep: i32 = 0;
    while (dep < expected[at].len()) {
        if (checked.dependencies[at][dep] != expected[at][dep]) { return 7; }
        dep = dep + 1;
    }
    at = at + 1;
}
`)
		}
		if tc.name == "record-replacement" {
			source.WriteString(`
if (!checked.ok) { print(checked.why); return 8; }
if (checked.dependencies[1].len() != 1 || checked.dependencies[1][0] != 0) { return 9; }
if (checked.dependencies[3].len() != 0 || checked.dependencies[4].len() != 1 || checked.dependencies[4][0] != 3) { return 10; }
if (checked.dependencies[6].len() != 0) { return 11; }
`)
		}
		source.WriteString("print(checked.why); return 0; }\n")
		fmt.Fprintf(&main, "if (semantic_case_%d() != 0) { return %d; }\n", i, i+1)
		want.WriteString(tc.want + "\n")
	}
	main.WriteString("return 0; }\n")
	source.WriteString(main.String())
	return source.String(), want.String()
}

func TestSelfHostSSASemantic(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	for i, tc := range semanticCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			copySelfHostDriver(t, dir, "ssasem.fern")
			source, want := semanticSource([]int{i})
			if err := os.WriteFile(filepath.Join(dir, "semantic.fern"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := buildSelfHostBin(t, gcc, dir, "semantic.fern", "semantic")
			got, err := runX86_64Bin(runner, bin).CombinedOutput()
			if err != nil || string(got) != want {
				t.Fatalf("semantic verification: %v\ngot %q\nwant %q", err, got, want)
			}
		})
	}
}

func TestSelfHostSSASemanticIRArm64(t *testing.T)  { testSemanticIR(t, "arm64-linux") }
func TestSelfHostSSASemanticIRX86_64(t *testing.T) { testSemanticIR(t, "x86-64-linux") }
func TestSelfHostSSASemanticIRWasm(t *testing.T)   { testSemanticIR(t, "wasm32-wasi") }

func testSemanticIR(t *testing.T, target string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	var armGCC, armRunner, wasmtime string
	if target == "arm64-linux" {
		armGCC, armRunner = arm64Tooling(t)
	}
	if target == "wasm32-wasi" {
		var err error
		wasmtime, err = exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH")
		}
	}
	dir := copySelfHostTree(t)
	copySelfHostDriver(t, dir, "ssasem.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	var indices []int
	for i := range semanticCases() {
		indices = append(indices, i)
	}
	source, want := semanticSource(indices)
	entry := filepath.Join(dir, "semantic.fern")
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(runner, driver, "-target", target, "-emit", "asm", entry, root)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	output, err := cmd.Output()
	if err != nil || len(output) == 0 {
		t.Fatalf("self-host compile: %v\n%s", err, diagnostics.String())
	}
	var run *exec.Cmd
	switch target {
	case "x86-64-linux":
		run = runX86_64Bin(runner, buildBin(t, gcc, dir, "semantic", string(output)))
	case "arm64-linux":
		run = runArm64Bin(armRunner, buildBinArm64(t, armGCC, dir, "semantic", string(output)))
	case "wasm32-wasi":
		wat := filepath.Join(dir, "semantic.wat")
		if err := os.WriteFile(wat, output, 0o644); err != nil {
			t.Fatal(err)
		}
		run = exec.Command(wasmtime, "run", wat)
	}
	got, err := run.CombinedOutput()
	if err != nil || string(got) != want {
		t.Fatalf("semantic verification: %v\ngot %q\nwant %q", err, got, want)
	}
}
