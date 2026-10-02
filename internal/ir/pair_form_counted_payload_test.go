package ir

import "testing"

// The pair-form return ABI hands the caller one count of every pointer
// payload: a fresh construction or call result at rc 1, a moved local's own
// reference, or an alias retained through emitPairFormPayloadRetain. The
// reclaims that consume such a payload used to demand the callee proven
// fresh, so `Some(h.values[i])`, the shape every HeaderMap lookup returns,
// stranded its count at every consumer.

const pairFormAliasSrc = `struct H { names: string[], values: string[] }
function get(h: H, name: string): Option[string] {
    let i: i32 = 0;
    while (i < h.names.len()) { if (h.names[i] == name) { return Some(h.values[i]); } i = i + 1; }
    return None;
}
function via_match(h: H): i32 { match (get(h, "connection")) { Some(v) => { return v.len(); }, None => {} } return 0; }
function via_expr(h: H): i32 { return match (get(h, "connection")) { Some(v) => v.len(), None => 0 }; }
function via_local(h: H): i32 { let o: Option[string] = get(h, "connection"); match (o) { Some(v) => { return v.len(); }, None => {} } return 0; }
function via_try(h: H): Option[i32] { let v: string = get(h, "connection")?; return Some(v.len()); }
function main(): i32 { let h: H = H { names: ["connection"], values: ["keep-alive"] }; return via_match(h) + via_expr(h) + via_local(h); }
`

func lowerPairFormAlias(t *testing.T) *Program {
	t.Helper()
	p := lowerSourceWith(t, pairFormAliasSrc, 8)
	if !p.PairForm["get"] {
		t.Fatal("get is not pair-form; this test no longer covers the pair-form path")
	}
	return p
}

// The statement form's register fast path releases the payload at the arm's
// end and on the return out of it.
func TestPairFormAliasPayloadReleasedByMatch(t *testing.T) {
	p := lowerPairFormAlias(t)
	if n := countCallDirect(findFunc(p, "via_match").Ops, "__fern_str_dec"); n == 0 {
		t.Errorf("via_match never releases the payload get retained for it; ops:\n%s", p)
	}
}

// The expression form lowers the call through the heap rebox and reclaims
// that box deep, payload included.
func TestPairFormAliasPayloadReleasedByMatchExpr(t *testing.T) {
	p := lowerPairFormAlias(t)
	if countCallPrefix(p, "via_expr", "__drop_enum_") == 0 && countCallDirect(findFunc(p, "via_expr").Ops, "__fern_str_dec") == 0 {
		t.Errorf("via_expr never reclaims the reboxed result; ops:\n%s", p)
	}
}

// A local initialised from the call owns the rebox and is swept at exit.
func TestPairFormAliasPayloadReleasedThroughLocal(t *testing.T) {
	p := lowerPairFormAlias(t)
	if countCallPrefix(p, "via_local", "__drop_enum_") == 0 && countCallDirect(findFunc(p, "via_local").Ops, "__fern_str_dec") == 0 {
		t.Errorf("via_local never reclaims the box its local holds; ops:\n%s", p)
	}
}

// A `?` binding owns the payload the try site moved out of the rebox.
func TestPairFormAliasPayloadReleasedThroughTry(t *testing.T) {
	p := lowerPairFormAlias(t)
	if n := countCallDirect(findFunc(p, "via_try").Ops, "__fern_str_dec"); n == 0 {
		t.Errorf("via_try never releases the payload it took out of the rebox; ops:\n%s", p)
	}
}
