package e2ecompiler

const semanticEnum = `
let shape: typeinfo.Type = typeinfo.TypeUnion { name: "Shape", args: [] };
let dot = semrecords.Variant { name: "Dot", fields: [] };
let full = semrecords.Variant { name: "Full", fields: [semrecords.Field { name: "__ev", ty: ia }] };
let line = semrecords.Variant { name: "Line", fields: [semrecords.Field { name: "__ev", ty: i32t }] };
let shapeEnum = semrecords.Enum { views: false, nests_func: false, ty: shape, variants: [dot, full, line], layout: semrecords.layout_variant() };
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

// The test reads xs[0] with a checked read. The projection reads the element
// at args again with an unchecked read, whose imm differs from the first.
func semanticTwinReads(args string) string {
	return `
let shapes: typeinfo.Type = typeinfo.TypeArray { elem: shape, view: false };
params = [shapes]; types = [shapes, i32t, shape, bt, shape, ia, i32t, i32t, i32t]; result = i32t;
graph = ssa.SFunc { name: "first", nparams: 1, nvals: 9, entry: 7, takes_env: false, blocks: [
    ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(1, 1, [], 0), inst(1, 8, [], 1), inst(ssasem.array_get(), 2, [0, 1], 0), variant_test(3, 2, "Full")], term: branch_on(3) },
    ssa.SBlock { id: 17, preds: [7], insts: [inst(ssasem.array_get(), 4, ` + args + `, 1), variant_field(5, 4, 0, "Full"), inst(1, 6, [], 0), inst(ssasem.array_get(), 7, [5, 6], 0)], term: ret(7) },
    ssa.SBlock { id: 27, preds: [7], insts: [], term: ret(1) }
] };
`
}

// Each side tests the element at its own index, and the join reads the
// element at the phi of the two indices again, as loop versioning leaves a
// read after the loop. `test` is the body of the second side.
func semanticJoinedReads(test string) string {
	return `
let shapes: typeinfo.Type = typeinfo.TypeArray { elem: shape, view: false };
params = [shapes, i32t, i32t, bt];
types = [shapes, i32t, i32t, bt, shape, bt, shape, bt, i32t, shape, ia, i32t, i32t, i32t]; result = i32t;
graph = ssa.SFunc { name: "joined", nparams: 4, nvals: 14, entry: 7, takes_env: false, blocks: [
    ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1), inst(6, 2, [], 2), inst(6, 3, [], 3)], term: ssa.STerm { ...branch_on(3), t: 17, f: 27 } },
    ssa.SBlock { id: 17, preds: [7], insts: [inst(ssasem.array_get(), 4, [0, 1], 0), variant_test(5, 4, "Full")], term: ssa.STerm { ...branch_on(5), t: 37, f: 47 } },
    ssa.SBlock { id: 27, preds: [7], insts: ` + test + `},
    ssa.SBlock { id: 37, preds: [17], insts: [], term: br(67) },
    ssa.SBlock { id: 57, preds: [27], insts: [], term: br(67) },
    ssa.SBlock { id: 67, preds: [37, 57], insts: [
        inst(ssa.PHI, 8, [1, 2], 0), inst(ssasem.array_get(), 9, [0, 8], 1), variant_field(10, 9, 0, "Full"), inst(1, 11, [], 0), inst(ssasem.array_get(), 12, [10, 11], 0)
    ], term: ret(12) },
    ssa.SBlock { id: 47, preds: [17, 27], insts: [inst(1, 13, [], 0)], term: ret(13) }
] };
`
}

const semanticSingleVariant = `
let absent = semrecords.Variant { ...line, fields: [semrecords.Field { name: "__ev", ty: typeinfo.TypeNever { tag: 0 } }] };
enums = [semrecords.Enum { ...shapeEnum, variants: [full, absent] }];
if (semrecords.find_variant(enums[0], "Line") != 1) { return 80; }
types = [shape, ia, i32t, i32t];
graph = ssa.SFunc { ...graph, nvals: 4, blocks: [ssa.SBlock { id: 7, preds: [], insts: [
  inst(6, 0, [], 0), variant_field(1, 0, 0, "Full"), inst(1, 2, [], 0), inst(ssasem.array_get(), 3, [1, 2], 0)
], term: ret(3) }] };
`

func semanticEnumCases() []struct{ name, change, want string } {
	cases := []struct{ name, change, want string }{
		{"enum-guarded-projection", "", ""},
		{"enum-never-proves-projection", semanticSingleVariant, ""},
		{"enum-inhabited-needs-guard", semanticSingleVariant + `enums = [semrecords.Enum { ...shapeEnum, variants: [full, dot] }];`, "unguarded variant projection"},
		{"enum-never-field-schema", `enums = [semrecords.Enum { ...shapeEnum, variants: [dot, full, semrecords.Variant { ...line, fields: [semrecords.Field { name: "__ev", ty: typeinfo.TypeNever { tag: 0 } }] }] }];`, ""},
		{"enum-unknown-field-schema", `enums = [semrecords.Enum { ...shapeEnum, variants: [dot, full, semrecords.Variant { ...line, fields: [semrecords.Field { name: "__ev", ty: typeinfo.unchecked() }] }] }];`, "unresolved record field type"},
		{"enum-never-construction", `enums = [semrecords.Enum { ...shapeEnum, variants: [dot, full, semrecords.Variant { ...line, fields: [semrecords.Field { name: "__ev", ty: typeinfo.TypeNever { tag: 0 } }] }] }]; let b = graph.blocks[2]; graph = ssa.SFunc { ...graph, blocks: graph.blocks.with(2, ssa.SBlock { ...b, insts: b.insts.with(1, variant_make(6, "Line", [5])) }) };`, "variant field type"},
		{"enum-unguarded-projection", `let b = graph.blocks[0]; graph = ssa.SFunc { ...graph, blocks: graph.blocks.with(0, ssa.SBlock { ...b, term: ssa.STerm { ...b.term, t: 27, f: 17 } }) };`, "unguarded variant projection"},
		{"enum-projection-other-test", `graph = change(graph, 1, variant_test(1, 0, "Line"));`, "unguarded variant projection"},
		{"enum-proven-twin-read", semanticTwinReads("[0, 1]"), ""},
		{"enum-other-index-read", semanticTwinReads("[0, 8]"), "unguarded variant projection"},
		{"enum-joined-tested-reads", semanticJoinedReads(`[inst(ssasem.array_get(), 6, [0, 2], 0), variant_test(7, 6, "Full")], term: ssa.STerm { ...branch_on(7), t: 57, f: 47 } `), ""},
		{"enum-joined-untested-edge", semanticJoinedReads(`[inst(ssasem.array_get(), 6, [0, 2], 0), variant_test(7, 6, "Full")], term: ssa.STerm { ...branch_on(7), t: 47, f: 57 } `), "unguarded variant projection"},
		{"enum-joined-other-read", semanticJoinedReads(`[inst(ssasem.array_get(), 6, [0, 1], 0), variant_test(7, 6, "Full")], term: ssa.STerm { ...branch_on(7), t: 57, f: 47 } `), "unguarded variant projection"},
		{"enum-missing-schema", "enums = [];", "missing variant schema"},
		{"enum-unknown-variant", `graph = change(graph, 1, variant_test(1, 0, "Blob"));`, "unknown variant"},
		{"enum-test-type", "types = types.with(1, i32t);", "variant test type"},
		{"enum-test-arity", `graph = change(graph, 1, ssa.SInst { ir_kind: 0, kind_tag: ssasem.variant_is(), result: 1, args: [0, 0], imm: 0, str: "Full" });`, "variant test arity"},
		{"enum-projection-index", `let b = graph.blocks[1]; graph = ssa.SFunc { ...graph, blocks: graph.blocks.with(1, ssa.SBlock { ...b, insts: b.insts.with(0, variant_field(2, 0, 1, "Full")) }) };`, "variant projection index"},
		{"enum-projection-type", "types = types.with(2, sa);", "variant projection type"},
		{"enum-construction-arity", `let b = graph.blocks[2]; graph = ssa.SFunc { ...graph, blocks: graph.blocks.with(2, ssa.SBlock { ...b, insts: b.insts.with(1, variant_make(6, "Dot", [5])) }) };`, "variant construction arity"},
		{"enum-construction-field-type", `let b = graph.blocks[2]; graph = ssa.SFunc { ...graph, blocks: graph.blocks.with(2, ssa.SBlock { ...b, insts: b.insts.with(1, variant_make(6, "Full", [5])) }) };`, "variant field type"},
		{"enum-construction-nonunion", "types = types.with(6, ia);", "missing variant schema"},
		{"enum-duplicate-schema", "enums = [shapeEnum, shapeEnum];", "duplicate enum schema"},
		{"enum-duplicate-variant", `enums = [semrecords.Enum { ...shapeEnum, variants: [dot, dot] }];`, "duplicate variant name"},
		{"enum-nests-function", `enums = [semrecords.Enum { ...shapeEnum, nests_func: true }];`, "function value is not a field"},
		{"enum-empty-variant-name", `enums = [semrecords.Enum { ...shapeEnum, variants: [semrecords.Variant { ...dot, name: "" }] }];`, "empty variant name"},
		{"enum-nonunion-identity", `enums = [semrecords.Enum { ...shapeEnum, ty: ia }];`, "non-union enum identity"},
		{"enum-unresolved-identity", `enums = [semrecords.Enum { ...shapeEnum, ty: typeinfo.unchecked() }];`, "unresolved enum identity"},
		{"enum-variant-field-missing-schema", `enums = [semrecords.Enum { ...shapeEnum, variants: [semrecords.Variant { name: "Boxed", fields: [semrecords.Field { name: "__ev", ty: typeinfo.TypeStruct { name: "Box", args: [] } }] }] }];`, "missing nested record schema"},
		{"enum-record-field-missing-enum", `records = [semrecords.Record { declared: [], secret: false, views: false, nests_func: false, ty: typeinfo.TypeStruct { name: "Holder", args: [] }, fields: [semrecords.Field { name: "s", ty: shape }] }]; enums = [];`, "missing nested record schema"},
	}
	for i := range cases {
		cases[i].change = semanticEnum + cases[i].change
	}
	return cases
}

func unitEnumCases() []unitCase {
	return []unitCase{
		{"enum-borrowed-match", semanticEnum + "modes = [2];", `
if (p.payloads[2]) { return 49; }
if (!drops(find(p, 27, 1, 0 - 1), [6])) { return 50; }
if (!drops(find(p, 17, ssaunits.return_point(), 0 - 1), [])) { return 51; }
`, "", ""},
		{"enum-counted-match", semanticEnum + "modes = [3];", `
if (!p.payloads[2]) { return 52; }
if (!drops(find(p, 17, 0, 0 - 1), [0])) { return 53; }
if (!drops(find(p, 17, 2, 0 - 1), [2])) { return 54; }
if (!drops(find(p, 7, ssaunits.edge_point(), 27), [0])) { return 55; }
`, "", ""},
		{"enum-leaked-counted-match", semanticEnum + "modes = [3];", "", `let s = find(p, 17, 0, 0 - 1); p = replace(p, ssaunits.Step { ...s, drops: [] });`, "counted unit leaks at return"},
		{"changed-payload-take", semanticEnum + "modes = [3];", "", `p = ssaunits.Plan { ...p, payloads: p.payloads.with(2, false) };`, "payload take disagrees with the plan"},
		{"enum-construction-supply", semanticEnum + `modes = [2];
types = types.with(5, ia).append(i32t);
let b = graph.blocks[2];
graph = ssa.SFunc { ...graph, nvals: 8, blocks: graph.blocks.with(2, ssa.SBlock { ...b, insts: [inst(ssasem.array_new(), 5, [], 0), variant_make(6, "Full", [5]), inst(1, 7, [], 0)], term: ret(7) }) };
`, `
let made = find(p, 27, 1, 0 - 1);
if (!supply(made, 0, 5, 0, ssaunits.move_unit()) || !drops(made, [6])) { return 54; }
`, "", ""},
	}
}
