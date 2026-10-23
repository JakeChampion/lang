package checker

import (
	"strings"
	"testing"
)

// TestScalarMethodImportHint pins the E043 wording for a method call on a
// SCALAR whose defining stdlib module was not imported (#5494).
//
// Since the auto-prelude was removed (Phase 5) a program sees only what it
// imports, so `n.to_string()` without `import "std/i32"` fails. The bare
// diagnostic — "field access on non-struct value of type i32" — describes the
// desugared shape rather than the user's code, and is actively misleading for
// f-strings: `f"{n}"` desugars to `n.to_string()`, so a program that never
// mentions `to_string` reports a struct-field error pointed at the f-string.
//
// The hint names the import instead, read from the stdlib method index, so a
// method no module declares gets no import suggestion (#13064), which the last
// cases pin.
func TestScalarMethodImportHint(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string // all must appear in the error
		deny []string // none may appear
	}{
		{
			name: "f-string on i32 names std/i32 and explains the desugar",
			src:  `function main(): i32 { let n: i32 = 7; write(f"x{n}y"); return 0; }`,
			want: []string{`no method "to_string" on i32`, `import "std/i32"`, "desugars to"},
		},
		{
			name: "explicit to_string on i32",
			src:  `function main(): i32 { let n: i32 = 7; write(n.to_string()); return 0; }`,
			want: []string{`no method "to_string" on i32`, `import "std/i32"`},
		},
		{
			name: "f64 names std/f64, not std/i32",
			src:  `function main(): i32 { let f: f64 = 1.5; write(f.to_string()); return 0; }`,
			want: []string{`no method "to_string" on f64`, `import "std/f64"`},
			deny: []string{"std/i32"},
		},
		{
			name: "i64 names std/i64",
			src:  `function main(): i32 { let n: i64 = 7 as i64; write(n.to_string()); return 0; }`,
			want: []string{`import "std/i64"`},
		},
		{
			// Not an f-string desugar target, so the parenthetical must not
			// appear — it would be describing a mechanism the user did not use.
			name: "to_string_radix gets the import but NOT the f-string note",
			src:  `function main(): i32 { let n: i32 = 7; return n.to_string_radix(16).len(); }`,
			want: []string{`no method "to_string_radix" on i32`, `import "std/i32"`},
			deny: []string{"desugars to"},
		},
		{
			name: "any method the module declares names it",
			src:  `function main(): i32 { let n: i32 = -7; return n.abs(); }`,
			want: []string{`no method "abs" on i32`, `add ` + "`" + `import "std/i32"` + "`"},
		},
		{
			// An unknown method must NOT get an import suggestion — suggesting
			// std/i32 for a method it does not define would send the reader off
			// to add an import that cannot help.
			name: "unknown method gets no import",
			src:  `function main(): i32 { let n: i32 = 7; return n.nonexistent_method(); }`,
			want: []string{`no method "nonexistent_method" on i32, and no stdlib module declares one`},
			deny: []string{"import"},
		},
		{
			name: "a string method no module declares gets no import",
			src:  `function main(): i32 { let s: string = "abc"; return s.substring(0, 1).len(); }`,
			want: []string{`no method "substring" on string, and no stdlib module declares one`},
			deny: []string{"import"},
		},
		{
			name: "a near miss names the method and its import",
			src:  `function main(): i32 { let s: string = "abc"; return s.trimm().len(); }`,
			want: []string{`did you mean "trim", which needs ` + "`" + `import "std/string"` + "`"},
		},
		{
			name: "a method from another module names that module",
			src:  `import "std/string"; function main(): i32 { let s: string = "abc"; return s.to_json().len(); }`,
			want: []string{`add ` + "`" + `import "std/json"` + "`"},
			deny: []string{`import "std/string"` + "`"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if err == nil {
				t.Fatalf("expected a checker error, got none")
			}
			got := err.Error()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("error %q does not contain %q", got, w)
				}
			}
			for _, d := range tc.deny {
				if strings.Contains(got, d) {
					t.Errorf("error %q unexpectedly contains %q", got, d)
				}
			}
		})
	}
}

// NOTE on the positive path: there is deliberately no "with the import present
// it resolves" case here. checkSource goes through a bare parser.Parse, which
// does not run modload, so an `import "std/i32"` line is parsed but the module
// is never loaded and the method stays unregistered — the hint fires and the
// assertion would be testing the harness, not the compiler. That path is
// covered where imports are actually resolved: `fern -interp` on the same
// program prints `x7y`, and TestSelfHostWasmRun/fstring-int pins the f-string
// lowering end to end.
