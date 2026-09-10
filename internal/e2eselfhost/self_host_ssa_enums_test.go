package e2eselfhost

// An enum schema beside the record table: a union identity and its variants'
// field shapes. A variant's field is read only where a test of the same value
// for that variant has held.
const semanticEnum = `
var shape: typeinfo.Type = typeinfo.TypeUnion { name: "Shape", args: [] };
var dot = semrecords.Variant { name: "Dot", fields: [] };
var full = semrecords.Variant { name: "Full", fields: [semrecords.Field { name: "__ev", ty: ia }] };
var line = semrecords.Variant { name: "Line", fields: [semrecords.Field { name: "__ev", ty: i32t }] };
var shapeEnum = semrecords.Enum { ty: shape, variants: [dot, full, line] };
enums = [shapeEnum];
params = [shape]; types = [shape, bt, ia, i32t, i32t, i32t, shape]; result = i32t;
graph = ssa.SFunc { name: "measure", nparams: 1, nvals: 7, entry: 7, takes_env: false, blocks: [
    ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), variant_test(1, 0, "Full")], term: branch_on(1) },
    ssa.SBlock { id: 17, preds: [7], insts: [
        variant_field(2, 0, 0, "Full"), inst(1, 3, [], 0), inst(ssasem.array_get(), 4, [2, 3], 0)
    ], term: ret(4) },
    ssa.SBlock { id: 27, preds: [7], insts: [inst(1, 5, [], 0), variant_make(6, "Dot", [])], term: ret(5) }
] };
`

func semanticEnumCases() []struct{ name, change, want string } {
	cases := []struct{ name, change, want string }{
		{"enum-guarded-projection", "", ""},
		{"enum-unguarded-projection", `var b = graph.blocks[0]; graph = ssa.SFunc { ...graph, blocks: graph.blocks.with(0, ssa.SBlock { ...b, term: ssa.STerm { ...b.term, t: 27, f: 17 } }) };`, "unguarded variant projection"},
		{"enum-projection-other-test", `graph = change(graph, 1, variant_test(1, 0, "Line"));`, "unguarded variant projection"},
		{"enum-missing-schema", "enums = [];", "missing variant schema"},
		{"enum-unknown-variant", `graph = change(graph, 1, variant_test(1, 0, "Blob"));`, "unknown variant"},
		{"enum-test-type", "types = types.with(1, i32t);", "variant test type"},
		{"enum-test-arity", `graph = change(graph, 1, ssa.SInst { kind_tag: ssasem.variant_is(), result: 1, args: [0, 0], imm: 0, str: "Full" });`, "variant test arity"},
		{"enum-projection-index", `var b = graph.blocks[1]; graph = ssa.SFunc { ...graph, blocks: graph.blocks.with(1, ssa.SBlock { ...b, insts: b.insts.with(0, variant_field(2, 0, 1, "Full")) }) };`, "variant projection index"},
		{"enum-projection-type", "types = types.with(2, sa);", "variant projection type"},
		{"enum-construction-arity", `var b = graph.blocks[2]; graph = ssa.SFunc { ...graph, blocks: graph.blocks.with(2, ssa.SBlock { ...b, insts: b.insts.with(1, variant_make(6, "Dot", [5])) }) };`, "variant construction arity"},
		{"enum-construction-field-type", `var b = graph.blocks[2]; graph = ssa.SFunc { ...graph, blocks: graph.blocks.with(2, ssa.SBlock { ...b, insts: b.insts.with(1, variant_make(6, "Full", [5])) }) };`, "variant field type"},
		{"enum-construction-nonunion", "types = types.with(6, ia);", "missing variant schema"},
		{"enum-duplicate-schema", "enums = [shapeEnum, shapeEnum];", "duplicate enum schema"},
		{"enum-duplicate-variant", `enums = [semrecords.Enum { ...shapeEnum, variants: [dot, dot] }];`, "duplicate variant name"},
		{"enum-empty-variant-name", `enums = [semrecords.Enum { ...shapeEnum, variants: [semrecords.Variant { ...dot, name: "" }] }];`, "empty variant name"},
		{"enum-nonunion-identity", `enums = [semrecords.Enum { ...shapeEnum, ty: ia }];`, "non-union enum identity"},
		{"enum-unresolved-identity", `enums = [semrecords.Enum { ...shapeEnum, ty: typeinfo.unchecked() }];`, "unresolved enum identity"},
		{"enum-variant-field-missing-schema", `enums = [semrecords.Enum { ...shapeEnum, variants: [semrecords.Variant { name: "Boxed", fields: [semrecords.Field { name: "__ev", ty: typeinfo.TypeStruct { name: "Box", args: [] } }] }] }];`, "missing nested record schema"},
		{"enum-record-field-missing-enum", `records = [semrecords.Record { ty: typeinfo.TypeStruct { name: "Holder", args: [] }, fields: [semrecords.Field { name: "s", ty: shape }] }]; enums = [];`, "missing nested record schema"},
	}
	for i := range cases {
		cases[i].change = semanticEnum + cases[i].change
	}
	return cases
}

func unitEnumCases() []unitCase {
	return []unitCase{
		{"enum-borrowed-match", semanticEnum + "modes = [2];", `
if (!drops(find(p, 27, 1, 0 - 1), [6])) { return 50; }
if (!drops(find(p, 17, ssaunits.return_point(), 0 - 1), [])) { return 51; }
`, "", ""},
		// The projection borrows the counted scrutinee, so its last read
		// through the projection is where the scrutinee's unit is released.
		{"enum-counted-match", semanticEnum + "modes = [3];", `
if (!drops(find(p, 17, 2, 0 - 1), [0])) { return 52; }
if (!drops(find(p, 7, ssaunits.edge_point(), 27), [0])) { return 53; }
`, "", ""},
		{"enum-leaked-counted-match", semanticEnum + "modes = [3];", "", `var s = find(p, 17, 2, 0 - 1); p = replace(p, ssaunits.Step { ...s, drops: [] });`, "counted unit leaks at return"},
		{"enum-construction-supply", semanticEnum + `modes = [2];
types = types.with(5, ia).append(i32t);
var b = graph.blocks[2];
graph = ssa.SFunc { ...graph, nvals: 8, blocks: graph.blocks.with(2, ssa.SBlock { ...b, insts: [inst(ssasem.array_new(), 5, [], 0), variant_make(6, "Full", [5]), inst(1, 7, [], 0)], term: ret(7) }) };
`, `
var made = find(p, 27, 1, 0 - 1);
if (!supply(made, 0, 5, 0, ssaunits.move_unit()) || !drops(made, [6])) { return 54; }
`, "", ""},
	}
}
