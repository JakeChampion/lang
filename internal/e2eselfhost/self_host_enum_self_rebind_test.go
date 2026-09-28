package e2eselfhost

import "testing"

// An enum local rebound through a counted handback — `cur = f(cur)` where f is
// an "ENUM:" member returning its parameter — keeps its release on the AST
// lowering (#10447): the result is the local's own chain with one more count,
// and the rebind gives the old count back.

const enumRebindHead = `enum Sc { SA(i32), SB }
enum Rc { RA(i32[]), RB }
function k_of(r: i32): i32 { return r % 7; }
function (e: Sc) val(): i32 { match (e) { SA(v) => { return v; }, SB => { return 100; } } }
function rval(e: Rc): i32 { match (e) { RA(v) => { return v[0]; }, RB => { return 100; } } }
function hb_sc_param(e: Sc): Sc { return e; }
function hb_rc_param(e: Rc): Rc { return e; }
function hb_rc_pick(e: Rc, f: Rc, first: boolean): Rc { if (first) { return e; } return f; }
function mk_sc(r: i32): Sc { var m: Sc = SA(k_of(r)); return m; }
function mk_rc(r: i32): Rc { var m: Rc = RA([k_of(r), 1]); return m; }
`

func enumRebindLoop(body string) string {
	return enumRebindHead + "function main(): i32 {\n    var t: i32 = 0;\n    var r: i32 = 0;\n    while (r < 100) {\n" + body + "        r = r + 1;\n    }\n    return t % 101;\n}\n"
}

var enumSelfRebindRows = []leakRow{
	{"rc_self", enumRebindLoop(`        var cur: Rc = mk_rc(r);
        cur = hb_rc_param(cur);
        t = t + rval(cur);
`), true, [2]int64{}},
	{"sc_self", enumRebindLoop(`        var cs: Sc = mk_sc(r);
        cs = hb_sc_param(cs);
        t = t + cs.val();
`), true, [2]int64{}},
	{"rc_self_twice", enumRebindLoop(`        var cur: Rc = mk_rc(r);
        cur = hb_rc_param(cur);
        cur = hb_rc_param(cur);
        t = t + rval(cur);
`), true, [2]int64{}},
	{"rc_self_then_fresh", enumRebindLoop(`        var cur: Rc = mk_rc(r);
        cur = hb_rc_param(cur);
        t = t + rval(cur);
        cur = mk_rc(r + 2);
        t = t + rval(cur);
`), true, [2]int64{}},
	{"sc_other", enumRebindLoop(`        var cs: Sc = mk_sc(r);
        var o: Sc = mk_sc(r + 1);
        cs = hb_sc_param(o);
        t = t + cs.val() + o.val();
`), true, [2]int64{}},
	// Rebinds inside a branch are admitted when each is an "ENUM:" call.
	{"sc_cond", enumRebindLoop(`        var cs: Sc = mk_sc(r);
        var o: Sc = mk_sc(r + 1);
        if (r % 2 == 0) {
            cs = hb_sc_param(o);
        } else {
            cs = mk_sc(r + 3);
        }
        t = t + cs.val() + o.val();
`), true, [2]int64{}},
	{"sc_loop_call", enumRebindLoop(`        var cs: Sc = mk_sc(r);
        var j: i32 = 0;
        while (j < 2) {
            cs = mk_sc(r + j);
            j = j + 1;
        }
        t = t + cs.val();
`), true, [2]int64{}},
	// A rebind to another local inside a branch is not a counted call, so the
	// local keeps no credit rather than being released alongside the lender.
	{"sc_cond_alias", enumRebindLoop(`        var cs: Sc = mk_sc(r);
        var o: Sc = mk_sc(r + 1);
        if (r % 2 == 0) {
            cs = o;
        }
        t = t + cs.val() + o.val();
`), false, [2]int64{200, 100}},
	// Handing back another local's chain keeps the rc local out of the fresh
	// family: the result is not its own chain.
	{"rc_other", enumRebindLoop(`        var cur: Rc = mk_rc(r);
        var o: Rc = mk_rc(r + 1);
        cur = hb_rc_param(o);
        t = t + rval(cur) + rval(o);
`), false, [2]int64{400, 0}},
	// Two handback positions, only one of them the local itself.
	{"rc_pick_mixed", enumRebindLoop(`        var cur: Rc = mk_rc(r);
        var o: Rc = mk_rc(r + 1);
        cur = hb_rc_pick(cur, o, r % 2 == 0);
        t = t + rval(cur) + rval(o);
`), false, [2]int64{400, 100}},
}

func TestSelfHostEnumSelfRebindX86_64(t *testing.T) {
	runLeakRowsX86_64(t, enumSelfRebindRows)
}

func TestSelfHostEnumSelfRebindArm64(t *testing.T) {
	runLeakRowsArm64(t, enumSelfRebindRows)
}

func TestSelfHostEnumSelfRebindWasm(t *testing.T) {
	runLeakRowsWasm(t, enumSelfRebindRows)
}
