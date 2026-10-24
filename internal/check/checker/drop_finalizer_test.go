package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestExplicitDropCallRejected(t *testing.T) {
	const dropImpl = `trait Drop { function drop(self: Self): void; }
struct R { n: i32 }
impl Drop for R { function drop(self: Self): void { } }
`
	for _, tc := range []struct{ name, src string }{
		{"statement call", dropImpl +
			`function main(): i32 { let r: R = R { n: 1 }; r.drop(); return r.n; }`},
		{"call inside a nested block", dropImpl +
			`function main(): i32 { let r: R = R { n: 1 }; if (r.n > 0) { r.drop(); } return 0; }`},
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

func TestOrdinaryDropMethodAccepted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"drop method, no Drop impl",
			`struct Q { n: i32 }
			 function (q: Q) drop(): i32 { return q.n; }
			 function main(): i32 { let q: Q = Q { n: 3 }; return q.drop(); }`},
		{"other method on a Drop type",
			`trait Drop { function drop(self: Self): void; }
			 struct R { n: i32 }
			 impl Drop for R { function drop(self: Self): void { } }
			 function (r: R) size(): i32 { return r.n; }
			 function main(): i32 { let r: R = R { n: 4 }; return r.size(); }`},
		{"a Drop type never dropped by hand",
			`trait Drop { function drop(self: Self): void; }
			 struct R { n: i32 }
			 impl Drop for R { function drop(self: Self): void { } }
			 function main(): i32 { let r: R = R { n: 5 }; return r.n; }`},
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
