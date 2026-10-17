package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestCollectionMethodFixApplies(t *testing.T) {
	for _, tc := range []struct{ name, body, old, replacement string }{
		{"array-append", "let xs = [1]; let ys = xs.appendd(2); return ys.len();", "appendd", "append"},
		{"array-with", "let xs = [1]; let ys = xs.withh(0, 2); return ys[0];", "withh", "with"},
		{"slice-len", "let xs = [1, 2]; let ys = xs[0:1]; return ys.lenn();", "lenn", "len"},
		{"string-len", "let s = \"abc\"; return s.lenn();", "lenn", "len"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "function main(): i32 { " + tc.body + " }"
			prog, err := parser.Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Check(prog)
			es, ok := err.(diag.Errors)
			if !ok {
				t.Fatalf("expected diagnostics, got %v", err)
			}
			var fix *diag.Suggestion
			for _, e := range es {
				if ce, ok := e.(*Error); ok && ce.ErrCode == "E043" && ce.Fix != nil {
					fix = ce.Fix
				}
			}
			if fix == nil || fix.Replacement != tc.replacement {
				t.Fatalf("fix = %+v, want %q: %v", fix, tc.replacement, err)
			}
			start := fix.Pos.Col - 1
			if fix.Pos.Line != 1 || src[start:start+fix.Length] != tc.old {
				t.Fatalf("fix does not cover %q: %+v", tc.old, fix)
			}
			applied := src[:start] + fix.Replacement + src[start+fix.Length:]
			prog, err = parser.Parse(applied)
			if err != nil {
				t.Fatalf("edited source does not parse: %v", err)
			}
			if _, err = Check(prog); err != nil {
				t.Fatalf("edited source does not check: %v\n%s", err, applied)
			}
		})
	}
}

func TestRetiredCollectionMethodHasNoRespellingFix(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { let xs = [1]; xs.push(2); return xs.len(); }`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Check(prog)
	es, ok := err.(diag.Errors)
	if !ok || !strings.Contains(err.Error(), "assign the result back") {
		t.Fatalf("missing migration advice: %v", err)
	}
	for _, e := range es {
		if ce, ok := e.(*Error); ok && ce.Fix != nil {
			t.Fatalf("retired method received an incomplete fix: %+v", ce.Fix)
		}
	}
}
