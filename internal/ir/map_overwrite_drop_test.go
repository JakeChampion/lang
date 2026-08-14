package ir

import "testing"

// The Map reassignment-overwrite releases the columns the COW copy claimed
// (#6828), and only those.
//
// `__map_own_copied_cols` gives each COW copy its own claim on the key column
// and the array-value column (#6242). The overwrite that ends the OLD handle's
// ownership therefore owes their release — with the buf-and-handle free alone it
// leaks the whole claim per copy, which in a chain is quadratic. A string or
// struct VALUE is still shared with the copy, so the walks that reclaim those
// must stay out of this site.
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

// The string VALUE column stays SHARED with the copy, so the overwrite must not
// reclaim it — releasing it there frees what the handle being stored reads.
// (The loop-reinit and exit-sweep drops do walk it; that is the pre-existing
// Map[K, string] hazard #6242 recorded, and not this site.)
func TestMapOverwriteDropLeavesSharedValueColumn(t *testing.T) {
	src := `function strvals(n: i32): i32 {
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
function main(): i32 { return strvals(3); }`

	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		blocks := mapPtrCompareGuardCallees(p, "strvals")
		if len(blocks) == 0 {
			t.Fatalf("ptrW=%d: found no pointer-compare guard in strvals:\n%s", ptrW, p)
		}
		for _, callees := range blocks {
			if hasCallee(callees, "__drop_map_str_values") {
				t.Errorf("ptrW=%d: COW-overwrite release calls %v — the copy shares the string-value column, so releasing it here frees what the new handle reads:\n%s", ptrW, callees, p)
			}
		}
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
