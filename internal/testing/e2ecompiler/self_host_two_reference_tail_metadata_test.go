package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostTwoReferenceTailMetadata(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "two_ref_metadata.fern"), []byte(twoRefMetadataProbe), 0600); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "two_ref_metadata.fern", "two-ref-metadata")
	for _, tc := range []struct{ name, want string }{
		{"quad-metadata", "findings=0 unproved=0 skipped="},
		{"call-balanced", "findings=0 unproved=0 skipped="},
		{"call-leak-primary", "leaked at return of v0"},
		{"call-leak-secondary", "leaked at return of v2"},
		{"call-return-primary", "findings=0 unproved=0 skipped="},
		{"call-return-secondary", "findings=0 unproved=0 skipped="},
		{"callee-balanced", "findings=0 unproved=0 skipped="},
		{"callee-scalar-cannot-own-secondary", "leaked at return of v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runX86_64Bin(runner, driver, tc.name).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			t.Log(string(out))
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("want %q in %s", tc.want, out)
			}
			wantSummary := "findings=0 unproved=0 skipped="
			if !strings.HasPrefix(tc.want, "findings=0") {
				wantSummary = "findings=1 unproved=0 skipped="
			}
			if !strings.HasSuffix(strings.TrimSpace(string(out)), wantSummary) {
				t.Errorf("want final summary %q in %s", wantSummary, out)
			}
		})
	}
}

const twoRefMetadataProbe = `import "./physicalbody"; import "./ssasem"; import "./semrecords"; import "./typeinfo";
import "./rccertify"; import "./rcsigs"; import "./ssa"; import "./ir"; import "./util";

function inst(kind: i32, result: i32, args: i32[], imm: i32, str: string): ssa.SInst {
    return ssa.SInst { kind_tag: kind, result: result, args: args, imm: imm, str: str };
}
function call(result: i32, name: string, args: i32[]): ssa.SInst { return inst(11, result, args, 0, name); }
function param(i: i32): ssa.SInst { return inst(6, i, [], i, ""); }
function const_int(result: i32, v: i32): ssa.SInst { return inst(1, result, [], v, ""); }
function fresh_box(result: i32): ssa.SInst { return inst(48, result, [], 48, ""); }
function counted_load(result: i32, base: i32, off: i32): ssa.SInst { return inst(40, result, [base], off, "rc"); }
function store(base: i32, off: i32, value: i32): ssa.SInst { return inst(41, 0 - 1, [base, value], off, ""); }
function ret(v: i32): ssa.STerm { return ssa.STerm { kind_tag: 1, cond: 0, target: 0, t: 0, f: 0, value: v }; }
function br(target: i32): ssa.STerm { return ssa.STerm { kind_tag: 2, cond: 0, target: target, t: 0, f: 0, value: 0 - 1 }; }
function brif(cond: i32, t: i32, f: i32): ssa.STerm { return ssa.STerm { kind_tag: 3, cond: cond, target: 0, t: t, f: f, value: 0 - 1 }; }
function block(id: i32, preds: i32[], insts: ssa.SInst[], term: ssa.STerm): ssa.SBlock {
    return ssa.SBlock { id: id, insts: insts, term: term, preds: preds };
}
function func(nparams: i32, nvals: i32, blocks: ssa.SBlock[]): ssa.SFunc {
    return ssa.SFunc { name: "case", nparams: nparams, nvals: nvals, blocks: blocks, entry: 0, takes_env: false };
}


function array_type(): typeinfo.Type { return typeinfo.TypeArray { elem: typeinfo.TypeString { tag: 0 }, view: false }; }
function triple_type(): typeinfo.Type { return typeinfo.TypeTuple { elements: [array_type(), array_type(), typeinfo.TypeBool { tag: 0 }] }; }
function quad_type(): typeinfo.Type { return typeinfo.TypeTuple { elements: [array_type(), array_type(), typeinfo.TypeBool { tag: 0 }, typeinfo.TypeBool { tag: 0 }] }; }
function main(): i32 {
 let name = args()[1]; let in_callee = util.str_has_prefix(name, "callee-");
 let params: typeinfo.Type[] = []; let modes: i32[] = []; let result: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false };
 if (in_callee) { params = [array_type(), array_type(), typeinfo.TypeBool { tag: 0 }]; modes = [ssasem.counted_mode(), ssasem.counted_mode(), ssasem.value_mode()]; result = triple_type(); }
 if (name == "call-return-primary" || name == "call-return-secondary") { result = array_type(); }
 let m = physicalbody.Metadata { contracts: [ssasem.Contract { name: "triple", params: [], modes: [], result: triple_type() }, ssasem.Contract { name: "quad", params: [], modes: [], result: quad_type() }, ssasem.Contract { name: "case", params: params, modes: modes, result: result }], records: semrecords.no_records(), enums: [], finalizers: [] };
 let abis = physicalbody.abis(physicalbody.Collection { producers: [m], rows: [] });
 let abi = abis.lookup("triple");
 if (!abi.known || !abi.pair_first || abi.pair_second != -1 || !abi.result_unit) { print("invalid metadata"); return 91; }
 let qa = abis.lookup("quad");
 if (!qa.known || !qa.pair_first || qa.pair_second != -1 || !qa.result_unit) { print("invalid quad metadata"); return 92; }
 if (name == "quad-metadata") { print("findings=0 unproved=0 skipped="); return 0; }
 let ops: ir.Op[] = [ir.op_call_scalar_word(1), ir.op_call_pair_word(0), ir.op_ret_pair_word(0), ir.op_ret_scalar_word(1)];
 let ins: ssa.SInst[] = []; let term = ret(7); let np: i32 = 0;
 if (in_callee) {
  np = 3; ins = [param(0), param(1), param(2)];
  if (name == "callee-balanced") { ins = ins.append(inst(59, 3, [1], 2, "ret_word")); ins = ins.append(inst(59, 4, [2], 3, "ret_word")); }
  else { ins = ins.append(inst(59, 3, [1], 3, "ret_word")); }
  term = ret(0);
 } else {
  ins = [call(0, "triple", []), inst(59, 1, [], 0, "call_word"), inst(59, 2, [], 1, "call_word")];
  if (name != "call-leak-primary" && name != "call-return-primary") { ins = ins.append(call(3, "__fern_rc_dec", [0])); }
  if (name != "call-leak-secondary" && name != "call-return-secondary") { ins = ins.append(call(4, "__fern_rc_dec", [2])); }
  ins = ins.append(const_int(7, 0));
  if (name == "call-return-primary") { term = ret(0); }
  if (name == "call-return-secondary") { term = ret(2); }
 }
 let f = func(np, 8, [block(0, [], ins, term)]);
 let r = rccertify.certify(f, ops, abis.lookup("case"), abis, rcsigs.table(), rcsigs.op_table());
 for x in r.findings { print(rccertify.render("case", x)); }
 print("findings=" + util.i32_to_string(r.findings.len()) + " unproved=" + util.i32_to_string(r.unproved) + " skipped=" + r.skipped);
 return 0;
}
`
