package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each case is a branch on a comparison of two constants, or of a value with
// itself. The driver prints which arm `irverifygate.decided` keeps: "t", "f",
// or "both" when the comparison is not decided.
func rccertifyDecidedSource() string {
	return `import "./irverifygate"; import "./ir"; import "./ssa";

function inst(kind: i32, result: i32, args: i32[], imm: i32, str: string): ssa.SInst {
    return ssa.SInst { ir_kind: if (kind == ssa.BINARY) { ir.kind_id(str) } else { 0 }, kind_tag: kind, result: result, args: args, imm: imm, str: str };
}
function block(id: i32, preds: i32[], insts: ssa.SInst[], term: ssa.STerm): ssa.SBlock {
    return ssa.SBlock { id: id, insts: insts, term: term, preds: preds };
}
function ret(v: i32): ssa.STerm { return ssa.STerm { kind_tag: 1, cond: 0, target: 0, t: 0, f: 0, value: v }; }
function brif(cond: i32, t: i32, f: i32): ssa.STerm { return ssa.STerm { kind_tag: 3, cond: cond, target: 0, t: t, f: f, value: 0 - 1 }; }

function kept(op: string, width: i32, a: i32, b: i32, same: boolean): string {
    let rhs: i32 = 2;
    if (same) { rhs = 1; }
    let b0: ssa.SBlock = block(0, [], [inst(1, 1, [], a, ""), inst(1, 2, [], b, ""), inst(9, 3, [1, rhs], width, op)], brif(3, 1, 2));
    let b1: ssa.SBlock = block(1, [0], [], ret(1));
    let b2: ssa.SBlock = block(2, [0], [], ret(2));
    let f: ssa.SFunc = ssa.SFunc { name: "case", nparams: 0, nvals: 4, blocks: [b0, b1, b2], entry: 0, takes_env: false };
    let d: ssa.SFunc = irverifygate.decided(f);
    if (d.blocks.len() == 3) { return "both"; }
    for x in d.blocks {
        if (x.id == 1) { return "t"; }
    }
    return "f";
}

function main(): i32 {
    let av: string[] = args();
    let width: i32 = 16;
    if (av[2] == "32") { width = 32; }
    if (av[2] == "64") { width = 64; }
    let a: i32 = 0;
    let b: i32 = 0;
    for i in 0..av[3].len() {
        if (av[3][i] != b'-') { a = a * 10 + (av[3][i] - b'0') as i32; }
    }
    if (av[3][0] == b'-') { a = 0 - a; }
    for i in 0..av[4].len() {
        if (av[4][i] != b'-') { b = b * 10 + (av[4][i] - b'0') as i32; }
    }
    if (av[4][0] == b'-') { b = 0 - b; }
    print(kept(av[1], width, a, b, av.len() > 5));
    return 0;
}
`
}

func TestSelfHostRcCertifyDecidedBranches(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "rccertify_decided.fern"), []byte(rccertifyDecidedSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "rccertify_decided.fern", "rccertify-decided-driver")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"eq", "64", "3", "3"}, "t"},
		{[]string{"ne", "64", "3", "3"}, "f"},
		{[]string{"lt_s", "64", "-1", "2"}, "t"},
		{[]string{"lt_u", "64", "-1", "2"}, "f"},
		{[]string{"lt_u", "32", "-1", "2"}, "f"},
		{[]string{"le_s", "32", "2", "2"}, "t"},
		{[]string{"gt_s", "64", "2", "-5"}, "t"},
		{[]string{"gt_u", "64", "2", "-5"}, "f"},
		{[]string{"ge_u", "32", "0", "1"}, "f"},
		{[]string{"lt_s", "16", "1", "2"}, "both"},
		{[]string{"add", "64", "1", "2"}, "both"},
		{[]string{"lt_s", "64", "7", "7", "same"}, "f"},
		{[]string{"ge_u", "64", "7", "7", "same"}, "t"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			out, err := runX86_64Bin(runner, driver, tc.args...).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}
