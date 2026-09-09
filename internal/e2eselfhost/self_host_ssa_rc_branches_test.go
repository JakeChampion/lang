package e2eselfhost

const physicalBranchValues = `
var bt: typeinfo.Type = typeinfo.TypeBool { tag: 0 };
params = [bt]; types = [bt, i, i, row, row, row];
ops = [inst(6, 0, [], 0), inst(1, 1, [], 7), inst(1, 2, [], 8),
    inst(ssasem.array_new(), 3, [1], 0), inst(ssasem.array_new(), 4, [2], 0)];
var entry = ssa.SBlock { id: 7, preds: [], insts: ops, term: ssa.STerm { kind_tag: 3, cond: 0, t: 17, f: 27, value: 0, target: 0 } };
var left = ssa.SBlock { id: 17, preds: [7], insts: [], term: ssa.STerm { kind_tag: 2, target: 37, value: 0, cond: 0, t: 0, f: 0 } };
var right = ssa.SBlock { ...left, id: 27 };
var join = ssa.SBlock { id: 37, preds: [17, 27], insts: [inst(8, 5, [3, 4], 0)], term: ret(5) };
blocks = [join, right, entry, left];
`

const physicalBranchProjections = physicalBranchValues + `
var owner: typeinfo.Type = typeinfo.TypeTuple { elements: [row, row] };
types = [bt, i, i, row, row, owner, row, row, row];
entry = ssa.SBlock { ...entry, insts: ops.append(inst(ssasem.tuple_new(), 5, [3, 4], 0)) };
left = ssa.SBlock { ...left, insts: [inst(ssasem.tuple_get(), 6, [5], 0)] };
right = ssa.SBlock { ...right, insts: [inst(ssasem.tuple_get(), 7, [5], 1)] };
join = ssa.SBlock { ...join, insts: [inst(8, 8, [6, 7], 0)], term: ret(8) };
blocks = [join, left, right, entry];
`

func physicalRCBranchCases() []physicalRCCase {
	return []physicalRCCase{
		{"branch-phi", physicalBranchValues, "[1]"},
		{"edge-parent-drop", physicalBranchProjections, "[1]"},
		{"duplicate-phis", physicalBranchProjections + `
types = types.append(row); join = ssa.SBlock { ...join, insts: [inst(8, 8, [6, 7], 0), inst(8, 9, [6, 7], 0)] };
blocks = [right, join, left, entry];`, "[1]"},
		{"scalar-phis", physicalBranchValues + `
types = [bt, i, i, i, bt, row];
entry = ssa.SBlock { ...entry, insts: [inst(6, 0, [], 0), inst(1, 1, [], 7), inst(1, 2, [], 8)] };
join = ssa.SBlock { ...join, insts: [inst(8, 3, [1, 2], 0), inst(8, 4, [0, 0], 0), inst(ssasem.array_new(), 5, [3], 0)] };
blocks = [join, right, entry, left];`, "[1]"},
		{"early-returns", physicalBranchValues + `
left = ssa.SBlock { ...left, term: ret(3) }; right = ssa.SBlock { ...right, term: ret(4) };
blocks = [right, entry, left];`, "[1]"},
		{"same-target", `
var bt: typeinfo.Type = typeinfo.TypeBool { tag: 0 };
types = [i, row, bt, row]; ops = [inst(1, 0, [], 7), inst(ssasem.array_new(), 1, [0], 0), inst(2, 2, [], 1)];
blocks = [ssa.SBlock { id: 27, preds: [7], insts: [inst(8, 3, [1], 0)], term: ret(3) },
    ssa.SBlock { id: 7, preds: [], insts: ops, term: ssa.STerm { kind_tag: 3, cond: 2, t: 27, f: 27, value: 0, target: 0 } }];`, ""},
		{"unreachable-predecessor", physicalBranchValues + `
types = types.append(i); types = types.append(row);
var dead = ssa.SBlock { id: 47, preds: [], insts: [inst(1, 6, [], 99), inst(ssasem.array_new(), 7, [6], 0)], term: left.term };
join = ssa.SBlock { ...join, preds: [47, 27, 17], insts: [inst(8, 5, [7, 4, 3], 0)] };
blocks = [dead, join, right, entry, left];`, "[1]"},
		{"nested-joins", `
var bt: typeinfo.Type = typeinfo.TypeBool { tag: 0 };
params = [bt, bt]; types = [bt, bt, i, i, i, i, row, row, row, row, row];
ops = [inst(6, 0, [], 0), inst(6, 1, [], 1), inst(1, 2, [], 7), inst(1, 3, [], 8), inst(1, 4, [], 9), inst(1, 5, [], 10),
    inst(ssasem.array_new(), 6, [2], 0), inst(ssasem.array_new(), 7, [3], 0), inst(ssasem.array_new(), 8, [4], 0), inst(ssasem.array_new(), 9, [5], 0)];
var leaf = ssa.SBlock { id: 37, preds: [17], insts: [], term: ssa.STerm { kind_tag: 2, target: 77, value: 0, cond: 0, t: 0, f: 0 } };
blocks = [ssa.SBlock { id: 77, preds: [37, 47, 57, 67], insts: [inst(8, 10, [6, 8, 7, 9], 0)], term: ret(10) },
    ssa.SBlock { ...leaf, id: 67, preds: [27] }, ssa.SBlock { ...leaf, id: 47 },
    ssa.SBlock { id: 27, preds: [7], insts: [], term: ssa.STerm { kind_tag: 3, cond: 1, t: 57, f: 67, value: 0, target: 0 } },
    ssa.SBlock { id: 7, preds: [], insts: ops, term: ssa.STerm { kind_tag: 3, cond: 0, t: 17, f: 27, value: 0, target: 0 } },
    ssa.SBlock { id: 17, preds: [7], insts: [], term: ssa.STerm { kind_tag: 3, cond: 1, t: 37, f: 47, value: 0, target: 0 } },
    ssa.SBlock { ...leaf, id: 57, preds: [27] }, leaf];`, "[1, 1]"},
	}
}
