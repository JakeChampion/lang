package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
)

// E073 — a `core/mem.Drop` impl's `drop` is a FINALIZER, called by the
// generated drop glue when the value's refcount reaches zero. An explicit
// `v.drop()` runs the body a second time on a value the runtime is still going
// to finalize, so it is rejected at check time.
//
// The impl is written against a locally declared `trait Drop` rather than an
// imported `core/mem.Drop` so the test needs no module loader. That is the same
// trait as far as every consumer is concerned: info.Impls keys are
// module-mangled, so ir.userDropFnName, treeshake.DropImplMethods and this rule
// all match on the SIMPLE name and all three treat a bare `Drop` as the
// finalizer trait. (The imported spelling is covered end-to-end by
// `fern -check` over a program importing core/mem.)
func TestExplicitDropCallRejected(t *testing.T) {
	const dropImpl = `trait Drop { function drop(self: Self): void; }
struct R { n: i32 }
impl Drop for R { function drop(self: Self): void { } }
`
	for _, tc := range []struct{ name, src string }{
		{"statement call", dropImpl +
			`function main(): i32 { var r: R = R { n: 1 }; r.drop(); return r.n; }`},
		{"call inside a nested block", dropImpl +
			`function main(): i32 { var r: R = R { n: 1 }; if (r.n > 0) { r.drop(); } return 0; }`},
		{"call on a parameter", dropImpl +
			`function eat(r: R): i32 { r.drop(); return r.n; }
			 function main(): i32 { return eat(R { n: 1 }); }`},
		{"call from inside the finalizer's own type", dropImpl +
			`function again(a: R, b: R): i32 { a.drop(); return b.n; }
			 function main(): i32 { return again(R { n: 1 }, R { n: 2 }); }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Check(prog)
			if err == nil {
				t.Fatalf("checked clean, want E073")
			}
			es, ok := err.(diag.Errors)
			if !ok {
				t.Fatalf("expected diag.Errors, got %T: %v", err, err)
			}
			found := false
			for _, e := range es {
				if ce, ok := e.(*Error); ok && ce.ErrCode == "E073" {
					found = true
				}
			}
			if !found {
				t.Errorf("no E073 among %v", err)
			}
		})
	}
}

// A `drop` method on a type with NO Drop impl is an ordinary method: nothing
// generates glue for it, so nothing calls it twice and the rule must not fire.
// Neither must an unrelated method on a type that DOES implement Drop — the
// gate is the method name plus the impl, not the impl alone.
func TestOrdinaryDropMethodAccepted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"drop method, no Drop impl",
			`struct Q { n: i32 }
			 function (q: Q) drop(): i32 { return q.n; }
			 function main(): i32 { var q: Q = Q { n: 3 }; return q.drop(); }`},
		{"other method on a Drop type",
			`trait Drop { function drop(self: Self): void; }
			 struct R { n: i32 }
			 impl Drop for R { function drop(self: Self): void { } }
			 function (r: R) size(): i32 { return r.n; }
			 function main(): i32 { var r: R = R { n: 4 }; return r.size(); }`},
		{"a Drop type never dropped by hand",
			`trait Drop { function drop(self: Self): void; }
			 struct R { n: i32 }
			 impl Drop for R { function drop(self: Self): void { } }
			 function main(): i32 { var r: R = R { n: 5 }; return r.n; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Check(prog); err != nil {
				t.Errorf("checker rejected a valid program: %v", err)
			}
		})
	}
}
