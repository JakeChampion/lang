package ir

import "testing"

// The Map reassignment-overwrite releases the columns the COW copy claimed
// (#6828) — all of them.
//
// `__map_own_copied_cols` gives each COW copy its own claim on the key column
// and on every value column (#6242, then #7114 / #8390 / #8420). The overwrite
// that ends the OLD handle's ownership therefore owes their release — with the
// buf-and-handle free alone it leaks the whole claim per copy, which in a chain
// is quadratic.
//
// The string (kind 5) and struct (kind 4) value walks used to stay out of this
// site, which is what #8431 closed: the release is now the shared map-drop
// chain, so the columns it walks are exactly the ones the copy claims, by
// construction rather than by a list kept in step here.
// TestMapOverwriteDropWalksTheValueColumn below pins the two that were missing.
//
// Both ptrW legs run. The two-word ABI is where the claim allocates a cell per
// entry per copy and the leak is immediate, but the release is emitted the same
// way on both, and the e2e legs that cover the single-word side need cross
// tooling this layer does not.
//
// The array-value half is NOT observable here: `__map_drop_values` lives in
// core/map.fern, this harness lowers a bare source with no module loading, and
// the dead-map-reclamation cull (~ir.go:3126) strips calls to it when it is
// absent. The generated key walk is what this layer can see.
func TestMapOverwriteDropReleasesClaimedColumns(t *testing.T) {
	// `a = b`, where b is a COW copy of a: the loop's `var b` reinit drop, the
	// overwrite, and the exit sweep each owe the key column.
	chain := `function chain(n: i32): i32 {
    var a: Map[string, i32] = map_new(16);
    a = a.insert("k" + "ey", 1);
    var i: i32 = 0;
    while (i < n) {
        var b = a;
        b = b.insert("c" + "hain", i);
        a = b;
        i = i + 1;
    }
    return a.len();
}
function main(): i32 { return chain(3); }`

	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, chain, ptrW)
		keys := countDirectCalls(p, "chain", "__drop_map_str_keys")
		bufs := countDirectCalls(p, "chain", "__fern_map_drop")
		if keys != bufs {
			t.Errorf("ptrW=%d: chain emits %d key-column walks against %d buf-and-handle frees — every release of a string-keyed map owes both, and the overwrite used to take the buf alone:\n%s", ptrW, keys, bufs, p)
		}
		// Several pointer-compare guards lower in this body (the self-map
		// mutation `a = a.insert(…)` is another), so the claim is that ONE of
		// them releases both — not that all of them do.
		blocks := mapPtrCompareGuardCallees(p, "chain")
		found := false
		for _, callees := range blocks {
			if hasCallee(callees, "__drop_map_str_keys") && hasCallee(callees, "__fern_map_drop") {
				found = true
			}
		}
		if !found {
			t.Errorf("ptrW=%d: no pointer-compare guard releases both the key column and the buf+handle; guards found: %v\n%s", ptrW, blocks, p)
		}
	}
}

// The COW-overwrite release walks the VALUE column through the same dispatch
// every other map-drop site uses, so a string or struct column is reclaimed
// there rather than stranded.
//
// This asserted the OPPOSITE until #8431, on the premise that the copy shared
// the value column — true when the site was written (#6227: releasing a shared
// column frees what the handle being stored reads), false since every column
// gained its claim (#7114, #8390, #8420). What the stale exclusion cost was
// the whole chain leak, quadratic on a boxing ABI: 100 rounds of a
// Map[string, string] chain stranded 83648 bytes on arm64 and 83968 on wasm,
// and 0 after.
func TestMapOverwriteDropWalksTheValueColumn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fn     string
		src    string
		callee string
	}{
		{"string values", "strvals", `function strvals(n: i32): i32 {
    var a: Map[string, string] = map_new(16);
    a = a.insert("k", "v");
    var i: i32 = 0;
    while (i < n) {
        var b = a;
        b = b.insert("c" + "hain", "w");
        a = b;
        i = i + 1;
    }
    return a.len();
}
function main(): i32 { return strvals(3); }`, "__drop_map_str_values"},
		{"struct values", "structvals", `struct Rec { name: string }
function structvals(n: i32): i32 {
    var a: Map[string, Rec] = map_new(16);
    a = a.insert("k", Rec { name: "v" });
    var i: i32 = 0;
    while (i < n) {
        var b = a;
        b = b.insert("c" + "hain", Rec { name: "w" });
        a = b;
        i = i + 1;
    }
    return a.len();
}
function main(): i32 { return structvals(3); }`, "__drop_map_via___drop_struct_Rec"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, ptrW := range []int{4, 8} {
				p := lowerSourceWith(t, tc.src, ptrW)
				blocks := mapPtrCompareGuardCallees(p, tc.fn)
				if len(blocks) == 0 {
					t.Fatalf("ptrW=%d: found no pointer-compare guard in %s:\n%s", ptrW, tc.fn, p)
				}
				found := false
				for _, callees := range blocks {
					if hasCallee(callees, tc.callee) {
						found = true
					}
				}
				if !found {
					t.Errorf("ptrW=%d: no COW-overwrite release calls %s — the copy claims that column, so leaving it unwalked strands it (#8431); guards found: %v\n%s", ptrW, tc.callee, blocks, p)
				}
			}
		})
	}
}

// mapPtrCompareGuardCallees returns the direct callees of each `OpNe; OpIf`
// then-arm in fnName. The COW-overwrite release is emitted behind exactly that
// guard (`old != new`), and so are the other COW-aware rebind shapes, so a
// caller asks whether ONE arm matches rather than all of them. Scans to the
// arm's OpElse or OpEnd.
func mapPtrCompareGuardCallees(p *Program, fnName string) [][]string {
	var out [][]string
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for i := 0; i+1 < len(fn.Ops); i++ {
			if fn.Ops[i].Kind != OpNe || fn.Ops[i+1].Kind != OpIf {
				continue
			}
			var callees []string
			depth := 0
			for j := i + 2; j < len(fn.Ops); j++ {
				op := fn.Ops[j]
				switch op.Kind {
				case OpIf, OpBlock, OpLoop:
					depth++
				case OpElse:
					if depth == 0 {
						j = len(fn.Ops)
					}
				case OpEnd:
					if depth == 0 {
						j = len(fn.Ops)
					} else {
						depth--
					}
				default:
					if isNamedCallKind(op.Kind) {
						callees = append(callees, op.Str)
					}
				}
			}
			out = append(out, callees)
		}
	}
	return out
}

func hasCallee(callees []string, want string) bool {
	for _, c := range callees {
		if c == want {
			return true
		}
	}
	return false
}

func countDirectCalls(p *Program, fnName, callee string) int {
	n := 0
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for _, op := range fn.Ops {
			if isNamedCallKind(op.Kind) && op.Str == callee {
				n++
			}
		}
	}
	return n
}
