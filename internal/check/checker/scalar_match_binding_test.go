package checker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestScalarMatchBindings(t *testing.T) {
	for _, form := range []string{"statement", "value"} {
		for _, tc := range []struct {
			name, declarations, typ, arms, code string
		}{
			{"literal-first", "", "i32", "0 => 10, n when n > 100 => n, _ => 14", ""},
			{"binding-first", "", "i32", "n when n > 100 => n, 0 => 10, rest => rest + 14", ""},
			{"binding-only", "", "i32", "value => value", ""},
			{"capital-name", "", "i32", "Value => Value", ""},
			{"array-is-not-scalar", "", "i32[]", "items => items.len()", "E035"},
			{"float", "", "f64", "value when value > 1.5 => value as i32, rest => rest as i32", ""},
			{"string", "", "string", `"" => 0, text when text.len() > 3 => text.len(), rest => rest.len()`, ""},
			{"boolean", "", "boolean", "value when value => 1, rest => if (rest) { 2 } else { 3 }", ""},
			{"boolean-literal-fallback", "", "boolean", "n when false => if (n) { 9 } else { 8 }, true => 1, false => 2, _ => 3", ""},
			{"guard-must-be-boolean", "", "i32", "n when n => n, rest => rest", "E027"},
			{"guard-is-not-exhaustive", "", "i32", "n when n > 0 => n", "E030"},
			{"binding-makes-next-arm-unreachable", "", "i32", "n => n, 0 => 10", "E026"},
			{"constructor", "enum Item { One, Two }", "i32", "One => 1, _ => 2", "E035"},
			{"qualified-constructor", "enum Item { One, Two }", "i32", "Item.One => 1, _ => 2", "E035"},
			{"empty-payload-is-not-a-name", "", "i32", "n() => 1, _ => 2", "E035"},
		} {
			t.Run(form+"/"+tc.name, func(t *testing.T) {
				arms := tc.arms
				body := "return match (tag) { " + arms + " };"
				if form == "statement" {
					var result []string
					for _, arm := range strings.Split(arms, ", ") {
						pattern, value, ok := strings.Cut(arm, " => ")
						if !ok {
							t.Fatal("test arm has no result")
						}
						result = append(result, pattern+" => { return "+value+"; }")
					}
					body = "match (tag) { " + strings.Join(result, ", ") + " }"
				}
				src := fmt.Sprintf("%s function f(tag: %s): i32 { %s }", tc.declarations, tc.typ, body)
				err := checkSource(t, src)
				if tc.code == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(diag.Format("binding.fern", src, err), tc.code) {
					t.Fatalf("want %s, got %v", tc.code, err)
				}
			})
		}
	}
}

func TestScalarMatchBindingDoesNotEscape(t *testing.T) {
	for _, body := range []string{
		"match (tag) { n => {} } return n;",
		"let result = match (tag) { n => n }; return n;",
	} {
		src := "function f(tag: i32): i32 { " + body + " }"
		if err := checkSource(t, src); err == nil || !strings.Contains(diag.Format("binding.fern", src, err), "E001") {
			t.Fatalf("binding escaped its arm: %v", err)
		}
	}
}
