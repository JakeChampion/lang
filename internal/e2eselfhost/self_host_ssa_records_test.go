package e2eselfhost

const semanticRecord = `
var recordType: typeinfo.Type = typeinfo.TypeStruct { name: "Box", args: [i32t] };
var wideRecord: typeinfo.Type = typeinfo.TypeStruct { name: "Box", args: [i64t] };
var record = semrecords.Record { ty: recordType, fields: [
    semrecords.Field { name: "xs", ty: ia }, semrecords.Field { name: "n", ty: i32t }
] };
records = [record];
params = [recordType]; types = [recordType, ia, i32t, recordType, ia, i32t, i32t]; result = i32t;
graph = ssa.SFunc { name: "replacement", nparams: 1, nvals: 7, entry: 7, takes_env: false, blocks: [
    ssa.SBlock { id: 7, preds: [], insts: [
        inst(6, 0, [], 0), field(1, 0, 0, "xs"), inst(1, 2, [], 1),
        inst(ssasem.record_new(), 3, [1, 2], 0), field(4, 3, 0, "xs"),
        inst(1, 5, [], 0), inst(ssasem.array_get(), 6, [4, 5], 0)
    ], term: ret(6) }
] };
`

func semanticRecordCases() []struct{ name, change, want string } {
	cases := []struct{ name, change, want string }{
		{"record-replacement", "", ""},
		{"record-generic-instances", `records = records.append(semrecords.Record { ty: wideRecord, fields: [semrecords.Field { name: "xs", ty: typeinfo.TypeArray { elem: i64t } }, semrecords.Field { name: "n", ty: i64t }] });`, ""},
		{"record-recursive-schema", `var node: typeinfo.Type = typeinfo.TypeStruct { name: "Node", args: [] }; records = records.append(semrecords.Record { ty: node, fields: [semrecords.Field { name: "children", ty: typeinfo.TypeArray { elem: node } }] });`, ""},
		{"record-missing-schema", "records = [];", "missing record projection schema"},
		{"record-duplicate-schema", "records = records.append(record);", "duplicate record schema"},
		{"record-unresolved-identity", `records = [semrecords.Record { ...record, ty: typeinfo.unchecked() }];`, "unresolved record identity"},
		{"record-nonnominal-identity", `records = [semrecords.Record { ...record, ty: ia }];`, "non-nominal record identity"},
		{"record-duplicate-field", `records = [semrecords.Record { ...record, fields: [record.fields[0], record.fields[0]] }];`, "duplicate record field name"},
		{"record-empty-field", `records = [semrecords.Record { ...record, fields: [semrecords.Field { name: "", ty: ia }] }];`, "empty record field name"},
		{"record-unresolved-field", `records = [semrecords.Record { ...record, fields: [semrecords.Field { name: "xs", ty: typeinfo.unchecked() }] }];`, "unresolved record field type"},
		{"record-void-field", `records = [semrecords.Record { ...record, fields: [semrecords.Field { name: "xs", ty: typeinfo.TypeVoid { tag: 0 } }] }];`, "unresolved record field type"},
		{"record-missing-nested-schema", `records = [semrecords.Record { ...record, fields: [semrecords.Field { name: "xs", ty: typeinfo.TypeArray { elem: wideRecord } }] }];`, "missing nested record schema"},
		{"record-inconsistent-instance-fields", `records = records.append(semrecords.Record { ty: wideRecord, fields: [record.fields[1], record.fields[0]] });`, "inconsistent record instance schema"},
		{"record-inconsistent-instance-arity", `records = records.append(semrecords.Record { ty: typeinfo.TypeStruct { name: "Box", args: [] }, fields: record.fields });`, "inconsistent record instance schema"},
		{"record-projection-arity", `graph = change(graph, 1, inst(ssasem.record_get(), 1, [], 0));`, "record projection arity"},
		{"record-negative-field-index", `graph = change(graph, 1, field(1, 0, 0 - 1, "xs"));`, "record projection index"},
		{"record-large-field-index", `graph = change(graph, 1, field(1, 0, 2, "xs"));`, "record projection index"},
		{"record-wrong-field-name", `graph = change(graph, 1, field(1, 0, 0, "n"));`, "record projection field identity"},
		{"record-wrong-field-type", "types = types.with(1, sa);", "record projection type"},
		{"record-scalar-field", `types = types.with(4, i32t); graph = change(graph, 4, field(4, 3, 1, "n")); graph = change(graph, 6, inst(7, 6, [4], 0));`, ""},
		{"record-construction-arity", "graph = change(graph, 3, inst(ssasem.record_new(), 3, [1], 0));", "record construction arity"},
		{"record-construction-field-type", "graph = change(graph, 3, inst(ssasem.record_new(), 3, [2, 1], 0));", "record field type"},
		{"record-construction-missing-instance", "types = types.with(3, wideRecord);", "missing record construction schema"},
		{"record-construction-nonnominal", "types = types.with(3, ia);", "missing record construction schema"},
	}
	for i := range cases {
		cases[i].change = semanticRecord + cases[i].change
	}
	return cases
}

const unitRecordDuplicate = `
var recordType: typeinfo.Type = typeinfo.TypeStruct { name: "Pair", args: [] };
records = [semrecords.Record { ty: recordType, fields: [
    semrecords.Field { name: "left", ty: sa }, semrecords.Field { name: "right", ty: sa }
] }];
params = [sa]; types = [sa, recordType]; result = recordType; modes = [3];
graph = ssa.SFunc { name: "duplicate", nparams: 1, nvals: 2, entry: 7, takes_env: false, blocks: [
    ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(ssasem.record_new(), 1, [0, 0], 0)], term: ret(1) }
] };
`

func unitRecordCases() []unitCase {
	return []unitCase{
		{"record-borrowed-replacement", semanticRecord + "modes = [2];", `
var created = find(p, 7, 3, 0 - 1);
if (!supply(created, 0, 1, 0, ssaunits.retain_unit()) || !drops(created, [])) { return 40; }
if (!drops(find(p, 7, 6, 0 - 1), [3])) { return 41; }
`, "", ""},
		{"record-counted-replacement", semanticRecord + "modes = [3];", `
var created = find(p, 7, 3, 0 - 1);
if (!supply(created, 0, 1, 0, ssaunits.retain_unit()) || !drops(created, [0])) { return 42; }
if (!drops(find(p, 7, 6, 0 - 1), [3])) { return 43; }
`, "", ""},
		{"record-loop-phi", unitRecordDuplicate + "sa = recordType;\n" + unitLoop, `
if (!supply(find(p, 7, ssaunits.edge_point(), 17), 0, 0, 0, ssaunits.move_unit())) { return 46; }
if (!supply(find(p, 27, ssaunits.edge_point(), 17), 0, 2, 0, ssaunits.move_unit())) { return 47; }
`, "", ""},
		{"record-recursive-return", unitRecordDuplicate + `
records = [semrecords.Record { ty: recordType, fields: [semrecords.Field { name: "children", ty: typeinfo.TypeArray { elem: recordType } }] }];
params = [recordType]; types = [recordType]; modes = [2];
graph = ssa.SFunc { ...graph, nvals: 1, blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)], term: ret(0) }] };
`, `if (!supply(find(p, 7, ssaunits.return_point(), 0 - 1), 0, 0, 0, ssaunits.retain_unit())) { return 48; }`, "", ""},
		{"record-empty-construction", unitRecordDuplicate + `
records = [semrecords.Record { ty: recordType, fields: [] }];
params = []; types = [recordType]; modes = [];
graph = ssa.SFunc { ...graph, nparams: 0, nvals: 1, blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(ssasem.record_new(), 0, [], 0)], term: ret(0) }] };
`, `if (!supply(find(p, 7, ssaunits.return_point(), 0 - 1), 0, 0, 0, ssaunits.move_unit())) { return 49; }`, "", ""},
		{"record-leaked-replacement", semanticRecord + "modes = [2];", "", `var s = find(p, 7, 6, 0 - 1); p = replace(p, ssaunits.Step { ...s, drops: [] });`, "counted unit leaks at return"},
		{"record-missing-borrowed-supply", semanticRecord + "modes = [2];", "", `var s = find(p, 7, 3, 0 - 1); p = replace(p, ssaunits.Step { ...s, supplies: [] });`, "unit supply arity"},
		{"record-duplicate-stores", unitRecordDuplicate, `
var s = find(p, 7, 1, 0 - 1);
if (!supply(s, 0, 0, 0, ssaunits.retain_unit()) || !supply(s, 1, 0, 1, ssaunits.move_unit())) { return 44; }
`, "", ""},
		{"record-duplicate-borrowed-stores", unitRecordDuplicate + "modes = [2];", `
var s = find(p, 7, 1, 0 - 1);
if (!supply(s, 0, 0, 0, ssaunits.retain_unit()) || !supply(s, 1, 0, 1, ssaunits.retain_unit())) { return 45; }
`, "", ""},
		{"record-double-move", unitRecordDuplicate, "", `var s = find(p, 7, 1, 0 - 1); var a = s.supplies[0]; p = replace(p, ssaunits.Step { ...s, supplies: [ssaunits.Supply { ...a, mode: 2 }, s.supplies[1]] });`, "move without counted unit"},
		{"record-borrowed-move", unitRecordDuplicate + "modes = [2];", "", `var s = find(p, 7, 1, 0 - 1); var a = s.supplies[0]; p = replace(p, ssaunits.Step { ...s, supplies: [ssaunits.Supply { ...a, mode: 2 }, s.supplies[1]] });`, "move without counted unit"},
		{"record-changed-schema", unitRecordDuplicate, "", `var r = f.records[0]; f = ssasem.Func { ...f, records: [semrecords.Record { ...r, fields: [r.fields[0]] }] };`, "record construction arity"},
	}
}
