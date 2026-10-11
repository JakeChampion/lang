package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rccertifyCasesSource is a driver over compiler/rccertify.fern: each case
// is a hand-built function in the lifted SSA the certifier reads, with its
// own ownership contract, and the driver prints what the certifier finds.
// The cases are the certifier's contract in miniature: a balanced body
// certifies, and each way of being unbalanced is a finding naming the value.
func rccertifyCasesSource() string {
	return `import "./rccertify"; import "./rcsigs"; import "./ssa"; import "./ir"; import "./util";

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

struct Case { f: ssa.SFunc, modes: i32[], returns_unit: boolean }

function one_block(nparams: i32, nvals: i32, insts: ssa.SInst[], term: ssa.STerm): ssa.SFunc {
    return func(nparams, nvals, [block(0, [], insts, term)]);
}

function pick(name: string): Case {
    let value: i32 = rccertify.value_mode();
    let borrow: i32 = rccertify.borrow_mode();
    let counted: i32 = rccertify.counted_mode();
    if (name == "balanced") {
        return Case { f: one_block(0, 3, [fresh_box(0), call(1, "__fern_rc_dec", [0]), const_int(2, 0)], ret(2)), modes: [], returns_unit: false };
    }
    if (name == "leak") {
        return Case { f: one_block(0, 2, [fresh_box(0), const_int(1, 0)], ret(1)), modes: [], returns_unit: false };
    }
    if (name == "double-release") {
        return Case { f: one_block(0, 4, [fresh_box(0), call(1, "__fern_rc_dec", [0]), call(2, "__fern_rc_dec", [0]), const_int(3, 0)], ret(3)), modes: [], returns_unit: false };
    }
    if (name == "fresh-returned") {
        return Case { f: one_block(0, 1, [fresh_box(0)], ret(0)), modes: [], returns_unit: true };
    }
    if (name == "borrowed-param-released") {
        return Case { f: one_block(1, 3, [param(0), call(1, "__fern_rc_dec", [0]), const_int(2, 0)], ret(2)), modes: [borrow], returns_unit: false };
    }
    if (name == "counted-param-returned") {
        return Case { f: one_block(1, 1, [param(0)], ret(0)), modes: [counted], returns_unit: true };
    }
    if (name == "counted-param-kept") {
        return Case { f: one_block(1, 2, [param(0), const_int(1, 0)], ret(1)), modes: [counted], returns_unit: false };
    }
    if (name == "retained-into-box") {
        // A borrowed parameter retained once and stored into a fresh box the
        // frame then releases: the retain pays for the store.
        let insts: ssa.SInst[] = [param(0), fresh_box(1), call(2, "__fern_rc_inc", [0]), store(1, 8, 2), call(3, "__fern_rc_dec", [1]), const_int(4, 0)];
        return Case { f: one_block(1, 5, insts, ret(4)), modes: [borrow], returns_unit: false };
    }
    if (name == "stored-without-retain") {
        let insts: ssa.SInst[] = [param(0), fresh_box(1), store(1, 8, 0), call(2, "__fern_rc_dec", [1]), const_int(3, 0)];
        return Case { f: one_block(1, 4, insts, ret(3)), modes: [borrow], returns_unit: false };
    }
    if (name == "unique-child-released") {
        // The child read off a box its frame proved unique is the frame's to
        // release; the same read without the proof is a borrow.
        let b0: ssa.SBlock = block(0, [], [param(0), call(1, "__fern_rc_is_unique", [0])], brif(1, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [counted_load(2, 0, 8), call(3, "__fern_rc_dec", [2])], br(2));
        let b2: ssa.SBlock = block(2, [0, 1], [call(4, "__fern_rc_dec", [0]), const_int(5, 0)], ret(5));
        return Case { f: func(1, 6, [b0, b1, b2]), modes: [counted], returns_unit: false };
    }
    if (name == "shared-child-released") {
        let insts: ssa.SInst[] = [param(0), counted_load(1, 0, 8), call(2, "__fern_rc_dec", [1]), call(3, "__fern_rc_dec", [0]), const_int(4, 0)];
        return Case { f: one_block(1, 5, insts, ret(4)), modes: [counted], returns_unit: false };
    }
    if (name == "null-guarded-release") {
        // A counted parameter that may be null is released only where it is
        // not, and the null arm owes nothing.
        let b0: ssa.SBlock = block(0, [], [param(0), const_int(1, 0), inst(9, 2, [0, 1], 0, "eq")], brif(2, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [const_int(3, 0)], ret(3));
        let b2: ssa.SBlock = block(2, [0], [call(4, "__fern_rc_dec", [0]), const_int(5, 0)], ret(5));
        return Case { f: func(1, 6, [b0, b1, b2]), modes: [counted], returns_unit: false };
    }
    if (name == "resumed-frame-not-certified") {
        // A body that re-enters through the task stack is left alone, leak
        // and all: its tokens come back as popped words.
        let insts: ssa.SInst[] = [call(0, "__fern_task_mode", []), fresh_box(1), const_int(2, 0)];
        return Case { f: one_block(0, 3, insts, ret(2)), modes: [], returns_unit: false };
    }
    if (name == "released-on-one-arm") {
        let b0: ssa.SBlock = block(0, [], [param(0), fresh_box(1), const_int(2, 1)], brif(2, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [call(3, "__fern_rc_dec", [1])], br(2));
        let b2: ssa.SBlock = block(2, [0, 1], [const_int(4, 0)], ret(4));
        return Case { f: func(1, 5, [b0, b1, b2]), modes: [borrow], returns_unit: false };
    }
    if (name == "retained-on-one-arm-released-on-another") {
        let b0: ssa.SBlock = block(0, [], [param(0), const_int(1, 1)], brif(1, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [call(2, "__fern_rc_inc", [0])], br(2));
        let b2: ssa.SBlock = block(2, [0, 1], [], brif(1, 3, 4));
        let b3: ssa.SBlock = block(3, [2], [call(3, "__fern_rc_dec", [0])], br(4));
        let b4: ssa.SBlock = block(4, [2, 3], [const_int(4, 0)], ret(4));
        return Case { f: func(1, 5, [b0, b1, b2, b3, b4]), modes: [borrow], returns_unit: false };
    }
    eprint("rccertify_cases: no case named " + name);
    exit(2);
    return Case { f: one_block(0, 0, [], ret(0 - 1)), modes: [], returns_unit: false };
}

function main(): i32 {
    let av: string[] = args();
    let c: Case = pick(av[1]);
    let abis: rccertify.Abis = rccertify.abis_of(["case"], [c.modes], [c.returns_unit], [false], [0], [false]);
    let irops: ir.Op[] = [];
    let r: rccertify.Report = rccertify.certify(c.f, irops, abis.lookup("case"), abis, rcsigs.table(), rcsigs.op_table());
    for x in r.findings {
        print(rccertify.render("case", x));
    }
    print("findings=" + util.i32_to_string(r.findings.len()) + " tracked=" + util.i32_to_string(r.tracked) + " unproved=" + util.i32_to_string(r.unproved) + " skipped=" + r.skipped);
    return 0;
}
`
}

// TestSelfHostRcCertifyCases pins the certifier (compiler/rccertify.fern,
// #11908) on hand-built functions: the balanced shapes it must accept and
// the unbalanced ones it must name, each with the value and the kind of
// finding. The whole-corpus gates (the fixture legs under FERN_IR_VERIFY)
// only say the certifier found nothing; this says it finds what is there.
func TestSelfHostRcCertifyCases(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "rccertify_cases.fern"), []byte(rccertifyCasesSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "rccertify_cases.fern", "rccertify-driver")
	cases := []struct {
		name string
		// The findings expected, each as "<kind> of v<n>"; empty certifies.
		want []string
		// The values left unproved: a count the paths disagree on is not a
		// finding (the lowering pairs one arm's retain with a later arm's
		// consume) but it is not certified either, and the census says so.
		unproved int
		// The reason the body was not certified at all, "" when it was.
		skipped string
	}{
		{"balanced", nil, 0, ""},
		{"leak", []string{"leaked at return of v0"}, 0, ""},
		{"double-release", []string{"spent without a unit of v0"}, 0, ""},
		{"fresh-returned", nil, 0, ""},
		{"borrowed-param-released", []string{"spent without a unit of v0"}, 0, ""},
		{"counted-param-returned", nil, 0, ""},
		{"counted-param-kept", []string{"leaked at return of v0"}, 0, ""},
		{"retained-into-box", nil, 0, ""},
		{"stored-without-retain", []string{"spent without a unit of v0"}, 0, ""},
		{"unique-child-released", nil, 0, ""},
		{"shared-child-released", []string{"spent without a unit of v1"}, 0, ""},
		{"null-guarded-release", nil, 0, ""},
		{"released-on-one-arm", []string{"leaked where it dies of v1"}, 0, ""},
		{"retained-on-one-arm-released-on-another", nil, 1, ""},
		{"resumed-frame-not-certified", nil, 0, "resumes from a task frame"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runX86_64Bin(runner, driver, tc.name).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			text := string(out)
			lines := strings.Split(strings.TrimSpace(text), "\n")
			findings := lines[:len(lines)-1]
			if len(findings) != len(tc.want) {
				t.Fatalf("want %d finding(s) %q, got:\n%s", len(tc.want), tc.want, text)
			}
			for i, want := range tc.want {
				if !strings.Contains(findings[i], want) {
					t.Errorf("finding %d: want %q in %q", i, want, findings[i])
				}
			}
			if want := fmt.Sprintf(" unproved=%d skipped=%s", tc.unproved, tc.skipped); !strings.HasSuffix(lines[len(lines)-1], want) {
				t.Errorf("want%s, got %q", want, lines[len(lines)-1])
			}
		})
	}
}
