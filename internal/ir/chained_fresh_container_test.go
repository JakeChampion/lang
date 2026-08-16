package ir

import (
	"strings"
	"testing"
)

// A read out of a FRESH owned container reclaims that container and hands the
// value on owning itself. That held only while the read was the whole
// expression: CHAINED, the value it produced had no owner at all — no local
// for the exit sweep to reclaim, and the predicates that answer "did this
// arrive owning itself" were consulted at the binding sites but not at the
// sites that consume a transient (#6401). `mk().f[0].a` cost 64 B a round and
// `sink(mk().f)` another 64, both unbounded, where the same reads bound to a
// local first were flat.
//
// The assertions key on the drop-helper NAMES because each one identifies
// which container was reclaimed: __drop_struct_QBox is the outer box,
// __drop_arr_struct_Q the element array, __fern_arr_dec the innermost buffer.

const chainedFreshSrc = `struct Q { vals: i32[], k: i32 }
struct QBox { qs: Q[], tag: i32 }
function mk_qbox(k: i32): QBox {
    return QBox { qs: [Q { vals: [k, k + 1], k: k }, Q { vals: [k], k: k }], tag: k };
}
function passthru(b: QBox): QBox { return b; }
function sinkv(v: i32[]): i32 { return v.len(); }
function chained_arg(k: i32): i32 { return sinkv(mk_qbox(k).qs[0].vals); }
function chained_index(k: i32): i32 { return mk_qbox(k).qs[0].vals[1]; }
function frombound(k: i32): i32 { var b: QBox = mk_qbox(k); return b.qs[0].vals[1]; }
function boundctl(k: i32): i32 { var b: QBox = mk_qbox(k); return b.tag; }
function fromparam(b: QBox): i32 { return b.qs[0].vals[1]; }
function aliased(b: QBox): i32 { return passthru(b).qs[0].vals[1]; }
function main(): i32 { return 0; }`

// countReleases counts every op that can release a reference, whatever form
// the release takes: an OpRcDec, or a call to a generated drop fn / a runtime
// dec / the box free. Counting the FORMS rather than one helper name is what
// keeps an assertion about release COUNT meaningful when a release moves
// between forms — outlining the exit sweep's struct drops (#6888) turned an
// inline body into a call to the same generated fn, which changed every
// name-keyed count in the tree without changing what runs.
func countReleases(fn *Func) int {
	n := 0
	for _, op := range fn.Ops {
		switch op.Kind {
		case OpRcDec:
			n++
		case OpCallDirect:
			if strings.Contains(op.Str, "_drop_") || strings.HasSuffix(op.Str, "_dec") ||
				op.Str == "__fern_box_free" {
				n++
			}
		}
	}
	return n
}

// Every container in the chain is reclaimed, in both consuming positions.
func TestChainedFreshContainerReadDropsEveryContainer(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, chainedFreshSrc, ptrW)
		for _, name := range []string{"chained_arg", "chained_index"} {
			fn := findFunc(p, name)
			for _, helper := range []string{"__drop_struct_QBox", "__drop_arr_struct_Q"} {
				if countCallDirect(fn.Ops, helper) == 0 {
					t.Errorf("ptrW=%d: %s never calls %s — the container it read through "+
						"leaks once per call", ptrW, name, helper)
				}
			}
		}
		// The innermost buffer is the argument temp's own reclaim, and only
		// the argument position owns it: the index position reads a scalar
		// out of it and hands the buffer to the element drop above.
		if countCallDirect(findFunc(p, "chained_arg").Ops, "__fern_arr_dec") == 0 {
			t.Errorf("ptrW=%d: chained_arg never dec's the i32[] it passed to sinkv — "+
				"the field read handed it the only reference and nothing releases it", ptrW)
		}
	}
}

// The other direction, and the one that would be a miscompile rather than a
// leak: a container reached from a PARAM belongs to the caller, and no link of
// the chain is this expression's to free.
//
// `aliased` is deliberately not asserted here. A callee returning its own
// parameter carries the return-transfer inc, so its result arrives at rc >= 2
// and every drop in the chain is is_unique-gated down to a plain dec — that
// shape emits drops by design, and always did.
func TestChainedReadOfABorrowedContainerDropsNothing(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, chainedFreshSrc, ptrW)
		fn := findFunc(p, "fromparam")
		for _, helper := range []string{"__drop_struct_QBox", "__drop_arr_struct_Q", "__fern_arr_dec"} {
			if n := countCallDirect(fn.Ops, helper); n != 0 {
				t.Errorf("ptrW=%d: fromparam called %s %d times on a container it borrows "+
					"from its caller — use-after-free", ptrW, helper, n)
			}
		}
	}
}

// The binding spelling was already flat, and must stay so: the local owns the
// container through its own two release sites — the zero-init-guarded re-init
// drop before the initialising store and the exit sweep — and a reclaim the
// READ added would be a further release of a box those two already cover.
//
// `boundctl` is the measure: the same local, bound the same way, differing only
// in that it reads a scalar field instead of chaining through the container. A
// count taken against a fixed number would say nothing here — the local's two
// sites are an implementation detail that has already been re-spelled once —
// so the assertion is that the chained read adds NO release over the control.
func TestChainedReadFromALocalReclaimsOnlyViaTheLocal(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, chainedFreshSrc, ptrW)
		got := countReleases(findFunc(p, "frombound"))
		want := countReleases(findFunc(p, "boundctl"))
		if got != want {
			t.Errorf("ptrW=%d: frombound emitted %d releases against the control's %d — "+
				"the chained read added %d release(s) to a local the exit sweep already "+
				"reclaims", ptrW, got, want, got-want)
		}
	}
}
