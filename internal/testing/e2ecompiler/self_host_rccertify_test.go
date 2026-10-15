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

function pair_word_case(callee: string): ssa.SFunc {
    let b0: ssa.SBlock = block(0, [], [call(0, callee, []), inst(59, 1, [], 0, "call_word"), const_int(2, 0), inst(9, 3, [0, 2], 0, "eq")], brif(3, 1, 2));
    let b1: ssa.SBlock = block(1, [0], [call(4, "__fern_rc_dec", [1])], br(4));
    let b2: ssa.SBlock = block(2, [0], [const_int(5, 1), inst(9, 6, [0, 5], 0, "eq")], brif(6, 3, 4));
    let b3: ssa.SBlock = block(3, [2], [call(7, "__fern_rc_dec", [1])], br(4));
    let b4: ssa.SBlock = block(4, [1, 2, 3], [const_int(8, 0)], ret(8));
    return func(0, 9, [b0, b1, b2, b3, b4]);
}

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
    if (name == "shell-slot-read-twice") {
        // A box released as a shell (a call with imm 2) after two reads of
        // one slot: the release follows the second read, which takes the
        // payload; the first only borrowed it (#12189).
        let insts: ssa.SInst[] = [param(0), counted_load(1, 0, 8), inst(40, 2, [1], 8, ""), counted_load(3, 0, 8),
            inst(11, 4, [0], 2, "__fern_rc_dec"), call(5, "__fern_rc_dec", [3]), const_int(6, 0)];
        return Case { f: one_block(1, 7, insts, ret(6)), modes: [counted], returns_unit: false };
    }
    if (name == "shell-payload-kept") {
        let insts: ssa.SInst[] = [param(0), counted_load(1, 0, 8), inst(40, 2, [1], 8, ""), inst(11, 3, [0], 2, "__fern_rc_dec"), const_int(4, 0)];
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
    if (name == "take-from-a-dominating-load") {
        // A null store into the slot a load in a dominating block read takes
        // the slot's unit for that load, which the frame then releases.
        let b0: ssa.SBlock = block(0, [], [param(0), counted_load(1, 0, 8), const_int(2, 0)], br(1));
        let b1: ssa.SBlock = block(1, [0], [store(0, 8, 2), call(3, "__fern_rc_dec", [1]), call(4, "__fern_rc_dec", [0]), const_int(5, 0)], ret(5));
        return Case { f: func(1, 6, [b0, b1]), modes: [counted], returns_unit: false };
    }
    if (name == "take-for-the-load-read-after") {
        // Two loads of the slot before its null store: the unit goes to the
        // older, which a later block reads, not to the nearer, read nowhere.
        let b0: ssa.SBlock = block(0, [], [param(0), counted_load(1, 0, 8), counted_load(2, 0, 8), const_int(3, 0), store(0, 8, 3)], br(1));
        let b1: ssa.SBlock = block(1, [0], [call(4, "__fern_rc_dec", [1]), call(5, "__fern_rc_dec", [0]), const_int(6, 0)], ret(6));
        return Case { f: func(1, 7, [b0, b1]), modes: [counted], returns_unit: false };
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
    if (name == "pair-word-released-on-each-payload-tag") {
        // A paired callee whose payload is a reference under tags 0 and 1:
        // the word is released on each, and the arm the two compares leave
        // (tag 2) owes nothing.
        return Case { f: pair_word_case("pair3"), modes: [], returns_unit: false };
    }
    if (name == "pair-word-unreleased-on-a-payload-tag") {
        return Case { f: pair_word_case("pair7"), modes: [], returns_unit: false };
    }
    if (name == "token-retain-under-test-consumed-after-join") {
        // #12181: a field read off the token's box before any test of the
        // token, retained where the token is null (where the box is released
        // too), consumed after the join, and kept in place by the
        // construction the box is reused for. Where the token holds, the
        // frame holds no unit of it.
        let b0: ssa.SBlock = block(0, [], [param(0), call(1, "__fern_rc_is_unique", [0]), const_int(3, 0)], brif(1, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [], br(3));
        let b2: ssa.SBlock = block(2, [0], [], br(3));
        let b3: ssa.SBlock = block(3, [1, 2], [inst(8, 2, [0, 3], 0, ""), counted_load(4, 0, 8), inst(9, 5, [2, 3], 0, "eq")], brif(5, 4, 5));
        let b4: ssa.SBlock = block(4, [3], [call(6, "__fern_rc_inc", [4]), call(10, "__fern_rc_dec", [0])], br(5));
        let b5: ssa.SBlock = block(5, [3, 4], [call(7, "take", [4]), const_int(8, 2), call(9, "__fern_alloc_reuse", [2, 8])], ret(9));
        return Case { f: func(1, 11, [b0, b1, b2, b3, b4, b5]), modes: [counted], returns_unit: true };
    }
    if (name == "token-retain-under-test-released-under-test") {
        // The same read, retained where the token is null and released
        // where it is null again, the box released whole: balanced on both
        // values of the token.
        let b0: ssa.SBlock = block(0, [], [param(0), call(1, "__fern_rc_is_unique", [0]), const_int(3, 0)], brif(1, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [], br(3));
        let b2: ssa.SBlock = block(2, [0], [], br(3));
        let b3: ssa.SBlock = block(3, [1, 2], [inst(8, 2, [0, 3], 0, ""), counted_load(4, 0, 8), inst(9, 5, [2, 3], 0, "eq")], brif(5, 4, 5));
        let b4: ssa.SBlock = block(4, [3], [call(6, "__fern_rc_inc", [4])], br(5));
        let b5: ssa.SBlock = block(5, [3, 4], [inst(9, 7, [2, 3], 0, "eq")], brif(7, 6, 7));
        let b6: ssa.SBlock = block(6, [5], [call(8, "__fern_rc_dec", [4])], br(7));
        let b7: ssa.SBlock = block(7, [5, 6], [call(9, "__sem_release_T", [0]), const_int(10, 0)], ret(10));
        return Case { f: func(1, 11, [b0, b1, b2, b3, b4, b5, b6, b7]), modes: [counted], returns_unit: false };
    }
    if (name == "derived-token-test-skips-the-shell") {
        // #12181: the box's field read at the token's join and released
        // after it; a second phi of the box, null where the token is, tests
        // where the shell release sits. Where the token holds, that test
        // holds too, so every path from the read frees the shell: the field
        // was the frame's.
        let b0: ssa.SBlock = block(0, [], [param(0), call(1, "__fern_rc_is_unique", [0]), const_int(3, 0)], brif(1, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [], br(3));
        let b2: ssa.SBlock = block(2, [0], [], br(3));
        let b3: ssa.SBlock = block(3, [1, 2], [inst(8, 2, [0, 3], 0, ""), counted_load(4, 0, 8), inst(9, 5, [2, 3], 0, "eq")], brif(5, 4, 5));
        let b4: ssa.SBlock = block(4, [3], [call(6, "__fern_rc_inc", [4]), call(7, "__sem_release_T", [0])], br(6));
        let b5: ssa.SBlock = block(5, [3], [], br(6));
        let b6: ssa.SBlock = block(6, [4, 5], [inst(8, 8, [3, 0], 0, ""), inst(9, 9, [8, 3], 0, "ne")], brif(9, 7, 8));
        let b7: ssa.SBlock = block(7, [6], [call(10, "__fern_rc_dec", [8])], br(8));
        let b8: ssa.SBlock = block(8, [6, 7], [call(11, "__sem_release_U", [4]), const_int(12, 0)], ret(12));
        return Case { f: func(1, 13, [b0, b1, b2, b3, b4, b5, b6, b7, b8]), modes: [counted], returns_unit: false };
    }
    if (name == "reuse-through-a-carried-token-writes-the-slot") {
        // #12181: one arm reads a field off the box under its token and moves
        // it; the other drops the box's children under a token of its own.
        // The arms' tokens meet in a phi the construction reuses, writing the
        // slot the field was read from. Where the first arm's token holds,
        // the path from the read gives that phi the box.
        let b0: ssa.SBlock = block(0, [], [param(0), param(1)], brif(1, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [call(2, "__fern_rc_is_unique", [0]), const_int(3, 0)], brif(2, 3, 4));
        let b3: ssa.SBlock = block(3, [1], [], br(5));
        let b4: ssa.SBlock = block(4, [1], [], br(5));
        let b5: ssa.SBlock = block(5, [3, 4], [inst(8, 4, [0, 3], 0, ""), counted_load(5, 0, 8), inst(9, 6, [4, 3], 0, "eq")], brif(6, 6, 7));
        let b6: ssa.SBlock = block(6, [5], [call(7, "__fern_rc_inc", [5])], br(7));
        let b7: ssa.SBlock = block(7, [5, 6], [call(8, "take", [5]), inst(9, 9, [4, 3], 0, "ne")], brif(9, 8, 9));
        let b8: ssa.SBlock = block(8, [7], [], br(11));
        let b9: ssa.SBlock = block(9, [7], [call(10, "__sem_release_T", [0])], br(11));
        let b2: ssa.SBlock = block(2, [0], [call(11, "__fern_rc_is_unique", [0]), const_int(12, 0)], brif(11, 12, 13));
        let b12: ssa.SBlock = block(12, [2], [call(13, "__sem_drop_T", [0])], br(14));
        let b13: ssa.SBlock = block(13, [2], [call(14, "__fern_rc_dec", [0])], br(14));
        let b14: ssa.SBlock = block(14, [12, 13], [inst(8, 15, [0, 12], 0, "")], br(11));
        let b11: ssa.SBlock = block(11, [8, 9, 14], [inst(8, 16, [4, 4, 15], 0, ""), const_int(17, 2), call(18, "__fern_alloc_reuse", [16, 17]), fresh_box(19), store(18, 8, 19)], ret(18));
        return Case { f: func(2, 20, [b0, b1, b3, b4, b5, b6, b7, b8, b9, b2, b12, b13, b14, b11]), modes: [counted, value], returns_unit: true };
    }
    if (name == "token-reuse-through-a-phi-an-outside-arm-nulls") {
        // #12334: a match's arm reads the payload off the box under its
        // token and the next construction reuses the box through a phi the
        // other arm, outside the token's region, gives null. That null is
        // the base walk's, and the phi still carries the box where the
        // token holds, so the read was the frame's.
        let b0: ssa.SBlock = block(0, [], [param(0), param(1)], brif(1, 1, 10));
        let b1: ssa.SBlock = block(1, [0], [call(2, "__fern_rc_is_unique", [0]), const_int(3, 0)], brif(2, 2, 3));
        let b2: ssa.SBlock = block(2, [1], [], br(4));
        let b3: ssa.SBlock = block(3, [1], [], br(4));
        let b4: ssa.SBlock = block(4, [2, 3], [inst(8, 4, [0, 3], 0, ""), counted_load(5, 0, 8), inst(9, 6, [4, 3], 0, "eq")], brif(6, 5, 6));
        let b5: ssa.SBlock = block(5, [4], [call(7, "__fern_rc_inc", [5])], br(6));
        let b6: ssa.SBlock = block(6, [4, 5], [inst(9, 8, [4, 3], 0, "ne")], brif(8, 7, 8));
        let b7: ssa.SBlock = block(7, [6], [], br(9));
        let b8: ssa.SBlock = block(8, [6], [call(9, "__sem_release_T", [0])], br(9));
        let b9: ssa.SBlock = block(9, [7, 8], [inst(8, 10, [0, 3], 0, ""), call(11, "__fern_rc_dec", [5])], br(12));
        let b10: ssa.SBlock = block(10, [0], [call(12, "__fern_rc_dec", [0]), const_int(13, 0)], br(12));
        let b12: ssa.SBlock = block(12, [9, 10], [inst(8, 14, [10, 13], 0, ""), const_int(15, 2), call(16, "__fern_alloc_reuse", [14, 15]), fresh_box(17), store(16, 8, 17)], ret(16));
        return Case { f: func(2, 18, [b0, b1, b2, b3, b4, b5, b6, b7, b8, b9, b10, b12]), modes: [counted, value], returns_unit: true };
    }
    if (name == "token-reuse-writes-the-slot-read") {
        // token-retain-under-test-consumed-after-join with the construction
        // storing over the slot the field was read from: the field was the
        // frame's where the token holds, and the move spent it.
        let b0: ssa.SBlock = block(0, [], [param(0), call(1, "__fern_rc_is_unique", [0]), const_int(3, 0)], brif(1, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [], br(3));
        let b2: ssa.SBlock = block(2, [0], [], br(3));
        let b3: ssa.SBlock = block(3, [1, 2], [inst(8, 2, [0, 3], 0, ""), counted_load(4, 0, 8), inst(9, 5, [2, 3], 0, "eq")], brif(5, 4, 5));
        let b4: ssa.SBlock = block(4, [3], [call(6, "__fern_rc_inc", [4]), call(10, "__fern_rc_dec", [0])], br(5));
        let b5: ssa.SBlock = block(5, [3, 4], [call(7, "take", [4]), const_int(8, 2), call(9, "__fern_alloc_reuse", [2, 8]), fresh_box(11), store(9, 8, 11)], ret(9));
        return Case { f: func(1, 12, [b0, b1, b2, b3, b4, b5]), modes: [counted], returns_unit: true };
    }
    if (name == "token-reuse-puts-back-and-stores-elsewhere" || name == "token-reuse-puts-back-and-stores-elsewhere-unretained") {
        // A field read off the token's box is stored back into its own slot
        // of the reused box and into a second slot: the first store keeps
        // the slot's unit, the second needs the retain.
        let second: ssa.SInst[] = [call(8, "__fern_rc_inc", [4]), inst(9, 9, [3, 5], 0, "eq")];
        if (name == "token-reuse-puts-back-and-stores-elsewhere-unretained") {
            second = [inst(9, 9, [3, 5], 0, "eq")];
        }
        let b0: ssa.SBlock = block(0, [], [param(0), call(1, "__fern_rc_is_unique", [0])], brif(1, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [], br(3));
        let b2: ssa.SBlock = block(2, [0], [const_int(2, 0)], br(3));
        let b3: ssa.SBlock = block(3, [1, 2], [inst(8, 3, [0, 2], 0, ""), counted_load(4, 0, 8), const_int(5, 0), inst(9, 6, [3, 5], 0, "eq")], brif(6, 4, 5));
        let b4: ssa.SBlock = block(4, [3], [call(7, "__fern_rc_inc", [4])], br(5));
        let b5: ssa.SBlock = block(5, [3, 4], second, brif(9, 6, 7));
        let b6: ssa.SBlock = block(6, [5], [const_int(10, 3), call(11, "__fern_alloc_reuse", [3, 10])], br(8));
        let b7: ssa.SBlock = block(7, [5], [], br(8));
        let b8: ssa.SBlock = block(8, [6, 7], [inst(8, 12, [11, 3], 0, ""), inst(9, 13, [3, 5], 0, "ne")], brif(13, 9, 10));
        let b9: ssa.SBlock = block(9, [8], [counted_load(14, 0, 16), call(15, "__fern_rc_dec", [14])], br(11));
        let b10: ssa.SBlock = block(10, [8], [call(16, "__sem_release_T", [0])], br(11));
        let b11: ssa.SBlock = block(11, [9, 10], [store(12, 8, 4), store(12, 16, 4)], ret(12));
        return Case { f: func(1, 17, [b0, b1, b2, b3, b4, b5, b6, b7, b8, b9, b10, b11]), modes: [counted], returns_unit: true };
    }
    if (name == "reuse-of-a-loop-box-through-its-token") {
        // #12326: the box is a loop's phi (the message read each time round),
        // its token is tested, the field read off it is moved, and the box
        // is reused through a phi of the token for the next time round.
        let b0: ssa.SBlock = block(0, [], [param(0), param(1)], br(1));
        let b1: ssa.SBlock = block(1, [0, 9], [inst(8, 2, [0, 12], 0, ""), call(3, "__fern_rc_is_unique", [2]), const_int(4, 0)], brif(3, 2, 3));
        let b2: ssa.SBlock = block(2, [1], [], br(4));
        let b3: ssa.SBlock = block(3, [1], [], br(4));
        let b4: ssa.SBlock = block(4, [2, 3], [inst(8, 5, [2, 4], 0, ""), counted_load(6, 2, 8), inst(9, 7, [5, 4], 0, "eq")], brif(7, 5, 6));
        let b5: ssa.SBlock = block(5, [4], [call(8, "__fern_rc_inc", [6])], br(6));
        let b6: ssa.SBlock = block(6, [4, 5], [call(9, "take", [6]), inst(9, 10, [5, 4], 0, "ne")], brif(10, 7, 8));
        let b7: ssa.SBlock = block(7, [6], [], br(9));
        let b8: ssa.SBlock = block(8, [6], [call(11, "__sem_release_T", [2])], br(9));
        let b9: ssa.SBlock = block(9, [7, 8], [inst(8, 13, [2, 4], 0, ""), const_int(14, 2), call(12, "__fern_alloc_reuse", [13, 14]), fresh_box(15), store(12, 8, 15)], brif(1, 1, 10));
        let b10: ssa.SBlock = block(10, [9], [], ret(12));
        return Case { f: func(2, 16, [b0, b1, b2, b3, b4, b5, b6, b7, b8, b9, b10]), modes: [counted, value], returns_unit: true };
    }
    if (name == "pair-word-settled-past-its-token-test") {
        // #12326: a paired call's second word, made before the token's
        // block, is settled on the first word's tag inside the walk under
        // the token's value: the walk reads the word's tag off the base.
        let b0: ssa.SBlock = block(0, [], [param(0), call(1, "pair1", []), inst(59, 2, [], 0, "call_word"), call(3, "__fern_rc_is_unique", [0])], brif(3, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [], br(3));
        let b2: ssa.SBlock = block(2, [0], [call(4, "__fern_rc_dec", [0]), const_int(5, 0)], br(3));
        let b3: ssa.SBlock = block(3, [1, 2], [inst(8, 6, [0, 5], 0, ""), const_int(7, 0), inst(9, 8, [1, 7], 0, "eq")], brif(8, 4, 5));
        let b4: ssa.SBlock = block(4, [3], [call(9, "__fern_rc_dec", [2])], br(6));
        let b5: ssa.SBlock = block(5, [3], [], br(6));
        let b6: ssa.SBlock = block(6, [4, 5], [inst(9, 10, [6, 7], 0, "ne")], brif(10, 7, 8));
        let b7: ssa.SBlock = block(7, [6], [], br(9));
        let b8: ssa.SBlock = block(8, [6], [], br(9));
        let b9: ssa.SBlock = block(9, [7, 8], [const_int(11, 2), call(12, "__fern_alloc_reuse", [6, 11]), fresh_box(13), store(12, 8, 13)], ret(12));
        return Case { f: func(1, 14, [b0, b1, b2, b3, b4, b5, b6, b7, b8, b9]), modes: [counted], returns_unit: true };
    }
    if (name == "loop-box-copied-under-its-null-token") {
        // #12326: a box made each time round a loop, tested unique, is
        // reused in place through its token, or copied where the token is
        // null: its fields are read and retained into the copy and its own
        // unit released. The loop never leaves: round it the box is made
        // again, and the construction's store of the field is not an
        // overwrite of the slot the copy read.
        let b0: ssa.SBlock = block(0, [], [param(0)], br(1));
        let b1: ssa.SBlock = block(1, [0, 9], [fresh_box(1), fresh_box(2), store(1, 8, 2), call(3, "__fern_rc_is_unique", [1])], brif(3, 2, 3));
        let b2: ssa.SBlock = block(2, [1], [], br(4));
        let b3: ssa.SBlock = block(3, [1], [const_int(4, 0)], br(4));
        let b4: ssa.SBlock = block(4, [2, 3], [inst(8, 5, [1, 4], 0, ""), const_int(6, 0), inst(9, 7, [5, 6], 0, "eq")], brif(7, 5, 6));
        let b5: ssa.SBlock = block(5, [4], [const_int(8, 2), call(9, "__fern_alloc_reuse", [5, 8]), counted_load(10, 1, 8), call(11, "__fern_rc_inc", [10]), store(9, 8, 11)], br(7));
        let b6: ssa.SBlock = block(6, [4], [], br(7));
        let b7: ssa.SBlock = block(7, [5, 6], [inst(8, 12, [9, 5], 0, ""), inst(9, 13, [5, 6], 0, "ne")], brif(13, 9, 8));
        let b8: ssa.SBlock = block(8, [7], [call(14, "__sem_release_T", [1])], br(9));
        let b9: ssa.SBlock = block(9, [7, 8], [fresh_box(15), store(12, 16, 15), call(16, "take", [12])], br(1));
        return Case { f: func(1, 17, [b0, b1, b2, b3, b4, b5, b6, b7, b8, b9]), modes: [value], returns_unit: false };
    }
    if (name == "token-box-copied-before-its-test") {
        // The box is reached through a copy made before its test, and the
        // token's walk inherits the copy: under the token holding the box,
        // the token phi is that copy, so the unit the base moved into the
        // phi is the box's again and the shell release spends it.
        let b0: ssa.SBlock = block(0, [], [fresh_box(0), fresh_box(1), store(0, 8, 1), inst(7, 2, [0], 0, ""), call(3, "__fern_rc_is_unique", [2])], brif(3, 1, 2));
        let b1: ssa.SBlock = block(1, [0], [], br(3));
        let b2: ssa.SBlock = block(2, [0], [const_int(4, 0)], br(3));
        let b3: ssa.SBlock = block(3, [1, 2], [inst(8, 5, [2, 4], 0, ""), counted_load(6, 2, 8), const_int(7, 0), inst(9, 8, [5, 7], 0, "eq")], brif(8, 4, 5));
        let b4: ssa.SBlock = block(4, [3], [call(9, "__fern_rc_inc", [6])], br(5));
        let b5: ssa.SBlock = block(5, [3, 4], [inst(9, 10, [5, 7], 0, "ne")], brif(10, 6, 7));
        let b6: ssa.SBlock = block(6, [5], [call(11, "__fern_rc_dec", [2])], br(8));
        let b7: ssa.SBlock = block(7, [5], [call(12, "__sem_release_T", [2])], br(8));
        let b8: ssa.SBlock = block(8, [6, 7], [call(13, "__fern_rc_dec", [6]), const_int(14, 0)], ret(14));
        return Case { f: func(0, 15, [b0, b1, b2, b3, b4, b5, b6, b7, b8]), modes: [], returns_unit: false };
    }
    if (name == "reuse-past-a-join-null-first") {
        // token-reuse-through-a-phi-an-outside-arm-nulls with the phi's inputs
        // the other way round: the other arm's null, a constant the token's
        // region does not define, comes first, so the boxes a phi can carry
        // are found past it.
        let b0: ssa.SBlock = block(0, [], [param(0), param(1)], brif(1, 1, 5));
        let b1: ssa.SBlock = block(1, [0], [call(2, "__fern_rc_is_unique", [0]), const_int(3, 0)], brif(2, 2, 3));
        let b2: ssa.SBlock = block(2, [1], [], br(4));
        let b3: ssa.SBlock = block(3, [1], [], br(4));
        let b4: ssa.SBlock = block(4, [2, 3], [inst(8, 4, [0, 3], 0, ""), counted_load(5, 0, 8), inst(9, 6, [4, 3], 0, "eq")], brif(6, 6, 7));
        let b6: ssa.SBlock = block(6, [4], [call(7, "__fern_rc_inc", [5])], br(7));
        let b7: ssa.SBlock = block(7, [4, 6], [inst(9, 8, [4, 3], 0, "ne")], brif(8, 8, 9));
        let b8: ssa.SBlock = block(8, [7], [], br(10));
        let b9: ssa.SBlock = block(9, [7], [call(9, "__sem_release_T", [0]), const_int(10, 0)], br(10));
        let b10: ssa.SBlock = block(10, [8, 9], [inst(8, 11, [0, 10], 0, ""), call(12, "take", [5])], br(12));
        let b5: ssa.SBlock = block(5, [0], [call(13, "__fern_rc_dec", [0]), const_int(14, 0)], br(12));
        let b12: ssa.SBlock = block(12, [5, 10], [inst(8, 15, [14, 11], 0, ""), const_int(16, 2), call(17, "__fern_alloc_reuse", [15, 16]), fresh_box(18), store(17, 8, 18)], ret(17));
        return Case { f: func(2, 19, [b0, b1, b2, b3, b4, b6, b7, b8, b9, b10, b5, b12]), modes: [counted, value], returns_unit: true };
    }
    if (name == "leak-in-a-loop-body") {
        // The body's inputs are the same in the second round as the first,
        // so its finding there comes from replaying its first run.
        let b0: ssa.SBlock = block(0, [], [param(0)], br(1));
        let b1: ssa.SBlock = block(1, [0, 2], [], brif(0, 2, 3));
        let b2: ssa.SBlock = block(2, [1], [fresh_box(1)], br(1));
        let b3: ssa.SBlock = block(3, [1], [const_int(2, 0)], ret(2));
        return Case { f: func(1, 3, [b0, b1, b2, b3]), modes: [value], returns_unit: false };
    }
    if (name == "null-tested-against-an-outside-null") {
        // reuse-past-a-join-null-first with a second phi in the token's
        // block, a fresh box or the outside arm's null, released only on
        // the edge where it is not null.
        let b0: ssa.SBlock = block(0, [], [param(0), param(1)], brif(1, 1, 5));
        let b1: ssa.SBlock = block(1, [0], [call(2, "__fern_rc_is_unique", [0]), const_int(3, 0)], brif(2, 2, 3));
        let b2: ssa.SBlock = block(2, [1], [fresh_box(19)], br(4));
        let b3: ssa.SBlock = block(3, [1], [], br(4));
        let b4: ssa.SBlock = block(4, [2, 3], [inst(8, 4, [0, 3], 0, ""), inst(8, 20, [19, 3], 0, ""), counted_load(5, 0, 8), inst(9, 6, [4, 3], 0, "eq")], brif(6, 6, 7));
        let b6: ssa.SBlock = block(6, [4], [call(7, "__fern_rc_inc", [5])], br(7));
        let b7: ssa.SBlock = block(7, [4, 6], [inst(9, 8, [4, 3], 0, "ne")], brif(8, 8, 9));
        let b8: ssa.SBlock = block(8, [7], [], br(10));
        let b9: ssa.SBlock = block(9, [7], [call(9, "__sem_release_T", [0]), const_int(10, 0)], br(10));
        let b10: ssa.SBlock = block(10, [8, 9], [inst(8, 11, [0, 10], 0, ""), call(12, "take", [5]), inst(9, 21, [20, 3], 0, "eq")], brif(21, 13, 14));
        let b13: ssa.SBlock = block(13, [10], [], br(12));
        let b14: ssa.SBlock = block(14, [10], [call(22, "__fern_rc_dec", [20])], br(12));
        let b5: ssa.SBlock = block(5, [0], [call(13, "__fern_rc_dec", [0]), const_int(14, 0)], br(12));
        let b12: ssa.SBlock = block(12, [5, 13, 14], [inst(8, 15, [14, 11, 11], 0, ""), const_int(16, 2), call(17, "__fern_alloc_reuse", [15, 16]), fresh_box(18), store(17, 8, 18)], ret(17));
        return Case { f: func(2, 23, [b0, b1, b2, b3, b4, b6, b7, b8, b9, b10, b13, b14, b5, b12]), modes: [counted, value], returns_unit: true };
    }
    eprint("rccertify_cases: no case named " + name);
    exit(2);
    return Case { f: one_block(0, 0, [], ret(0 - 1)), modes: [], returns_unit: false };
}

function main(): i32 {
    let av: string[] = args();
    let c: Case = pick(av[1]);
    // pair1, pair3 and pair7 return a variant in two words whose payload is
    // a reference under tag 0, under tags 0 and 1, and under every tag of
    // 0..2.
    let abis: rccertify.Abis = rccertify.abis_of(["case", "pair1", "pair3", "pair7", "take"], [c.modes, [], [], [], [rccertify.counted_mode()]], [c.returns_unit, false, false, false, false], [false, false, false, false, false], [0, 1, 3, 7, 0], [false, false, false, false, false]);
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
		{"shell-slot-read-twice", nil, 0, ""},
		{"shell-payload-kept", []string{"of v1"}, 0, ""},
		{"null-guarded-release", nil, 0, ""},
		{"take-from-a-dominating-load", nil, 0, ""},
		{"take-for-the-load-read-after", nil, 0, ""},
		{"released-on-one-arm", []string{"leaked where it dies of v1"}, 0, ""},
		{"retained-on-one-arm-released-on-another", nil, 1, ""},
		{"resumed-frame-not-certified", nil, 0, "resumes from a task frame"},
		{"pair-word-released-on-each-payload-tag", nil, 0, ""},
		{"pair-word-unreleased-on-a-payload-tag", []string{"leaked where it dies of v1"}, 0, ""},
		{"token-retain-under-test-consumed-after-join", []string{"spent without a unit of v4"}, 0, ""},
		{"token-retain-under-test-released-under-test", nil, 0, ""},
		{"derived-token-test-skips-the-shell", nil, 0, ""},
		// The two joins the other arm's token reaches are tested by nothing,
		// so they stay unproved.
		{"reuse-through-a-carried-token-writes-the-slot", nil, 2, ""},
		{"token-reuse-writes-the-slot-read", nil, 0, ""},
		{"token-reuse-puts-back-and-stores-elsewhere", nil, 0, ""},
		{"token-reuse-puts-back-and-stores-elsewhere-unretained", []string{"spent without a unit of v4"}, 0, ""},
		// The reuse's phi meets the other arm, which no tested token
		// decides.
		{"token-reuse-through-a-phi-an-outside-arm-nulls", nil, 1, ""},
		// The loop's phi meets the parameter and the reused box, which no
		// test decides.
		{"reuse-of-a-loop-box-through-its-token", nil, 1, ""},
		// The word is made before the token's block, so the walk under the
		// token's value inherits its tag.
		{"pair-word-settled-past-its-token-test", nil, 0, ""},
		// The copy's reads are of the box this time round; the construction
		// next time round stores into another box.
		{"loop-box-copied-under-its-null-token", nil, 0, ""},
		// The token phi's input is a copy of the box made before the test.
		{"token-box-copied-before-its-test", nil, 0, ""},
		// The null comes first in the phi; the join past the match, which no
		// token decides, stays unproved.
		{"reuse-past-a-join-null-first", nil, 1, ""},
		// The phi's null edge is settled against the outside arm's null.
		{"null-tested-against-an-outside-null", nil, 1, ""},
		{"leak-in-a-loop-body", []string{"leaked where it dies of v1"}, 0, ""},
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
