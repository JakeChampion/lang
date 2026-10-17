package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The memo's key must change with every op field the lift reads: a width,
// a decl index, the signedness flag and the float immediate shape the
// lifted SSA without changing the op's kind, immediates or name, and a key
// blind to one of them would answer a changed function from an old verdict.
func rcmemoKeyCasesSource() string {
	return `import "./ir"; import "./rccertify"; import "./rcmemo"; import "./ssa"; import "./util";

function base(): ir.Op {
    return ir.Op { source: 0,  kind_tag: 7, i32_imm: 3, i64_imm: 9i64, f64_imm: 1.5, width: 4, unsigned: false, str: "x", decl: 2 };
}

function key_of(op: ir.Op): string {
    let f: ssa.SFunc = ssa.SFunc { name: "k", nparams: 0, nvals: 2, blocks: [], entry: 0, takes_env: false };
    let abis: rccertify.Abis = rccertify.abis_of(["k"], [[]], [false], [false], [0], [false]);
    return rcmemo.key(rcmemo.off(), "k", f, [ir.op_const_i32(1), op], abis);
}

function main(): i32 {
    let same: string = key_of(base());
    if (key_of(base()) != same) { print("unstable"); return 1; }
    let variants: ir.Op[] = [
        ir.Op { ...base(), width: 8 },
        ir.Op { ...base(), decl: 3 },
        ir.Op { ...base(), unsigned: true },
        ir.Op { ...base(), f64_imm: 2.5 },
        ir.Op { ...base(), i32_imm: 4 },
        ir.Op { ...base(), i64_imm: 10i64 },
        ir.Op { ...base(), i64_imm: 9i64 + 4611686018427387904i64 },
        ir.Op { ...base(), str: "y" },
        ir.Op { ...base(), kind_tag: 8 },
    ];
    let i: i32 = 0;
    while (i < variants.len()) {
        if (key_of(variants[i]) == same) { print("variant " + util.i32_to_string(i) + " shares the key"); return 2; }
        i = i + 1;
    }
    print("distinct");
    return 0;
}
`
}

func TestSelfHostRcCertifyMemoKey(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "rcmemo_key_cases.fern"), []byte(rcmemoKeyCasesSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "rcmemo_key_cases.fern", "rcmemo-key-driver")
	out, err := runX86_64Bin(runner, driver).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "distinct" {
		t.Fatalf("memo key driver: %v\n%s", err, out)
	}
}
