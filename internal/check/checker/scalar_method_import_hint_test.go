package checker

import (
	"strings"
	"testing"
)

func TestScalarMethodImportHint(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
		deny []string
	}{
		{
			name: "f64 names std/f64, not std/i32",
			src:  `function main(): i32 { let f: f64 = 1.5; return f.to_string_prec(2).len(); }`,
			want: []string{`import "std/f64"`},
			deny: []string{"std/i32"},
		},
		{
			name: "i64 names std/i64",
			src:  `function main(): i32 { let n: i64 = 7 as i64; return n.to_string_radix(16).len(); }`,
			want: []string{`import "std/i64"`},
		},
		{
			name: "to_string_radix gets the import but NOT the f-string note",
			src:  `function main(): i32 { let n: i32 = 7; return n.to_string_radix(16).len(); }`,
			want: []string{`no method "to_string_radix" on i32`, `import "std/i32"`},
			deny: []string{"desugars to"},
		},
		{
			name: "u32 radix names std/u32",
			src:  `function main(): i32 { let x: u32 = 7; return x.to_string_radix(16).len(); }`,
			want: []string{`no method "to_string_radix" on u32`, `import "std/u32"`},
		},
		{
			name: "i64 padded names std/i64",
			src:  `function main(): i32 { let x: i64 = 7; return x.to_string_padded(2).len(); }`,
			want: []string{`no method "to_string_padded" on i64`, `import "std/i64"`},
		},
		{
			// An unknown method must NOT get an import suggestion — suggesting
			// std/i32 for a method it does not define would send the reader off
			// to add an import that cannot help.
			name: "unknown method keeps the generic error",
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
			name: "a narrow Option method names its receiver",
			src:  `function main(): i32 { let x: Option[i32] = Some(1); x.failure(); return 0; }`,
			want: []string{`no stdlib module declares one`},
			deny: []string{"add `import"},
		},
		{
			name: "a nested receiver is not a general Option method",
			src:  `function main(): i32 { let x: Option[i32] = Some(1); x.unzip(); return 0; }`,
			want: []string{`"unzip" is declared for Option[(T, U)]`},
			deny: []string{"add `import"},
		},
		{
			name: "matching IoError receiver",
			src:  `function probe(x: Option[IoError]): void { x.failure(); } function main(): i32 { return 0; }`,
			want: []string{`no stdlib module declares one`},
			deny: []string{`is declared for`},
		},
		{
			name: "matching nested Option receiver",
			src:  `function probe(x: Option[Option[i32]]): void { x.flatten(); } function main(): i32 { return 0; }`,
			want: []string{`import "std/option"`},
			deny: []string{`is declared for`},
		},
		{
			name: "matching Option Result receiver",
			src:  `function probe(x: Option[Result[i32, string]]): void { x.transpose(); } function main(): i32 { return 0; }`,
			want: []string{`import "std/option"`},
			deny: []string{`is declared for`},
		},
		{
			name: "matching nested Result receiver",
			src:  `function probe(x: Result[Result[i32, string], string]): void { x.flatten(); } function main(): i32 { return 0; }`,
			want: []string{`import "std/result"`},
			deny: []string{`is declared for`},
		},
		{
			name: "matching Result failure receiver",
			src:  `function probe(x: Result[void, IoError]): void { x.failure(); } function main(): i32 { return 0; }`,
			want: []string{`no stdlib module declares one`},
			deny: []string{`is declared for`},
		},
		{
			name: "matching generic tuple receiver",
			src:  `function probe[A, B](x: Option[(A, B)]): void { x.unzip(); } function main(): i32 { return 0; }`,
			want: []string{`import "std/option"`},
			deny: []string{`is declared for`},
		},
		{
			name: "matching restricted receiver near miss",
			src:  `function probe(x: Option[IoError]): void { x.failur(); } function main(): i32 { return 0; }`,
			want: []string{`no stdlib module declares one`},
			deny: []string{`is declared for`},
		},
		{
			name: "mismatching repeated Result parameter",
			src:  `function probe(x: Result[Result[i32, string], i32]): void { x.flatten(); } function main(): i32 { return 0; }`,
			want: []string{`is declared for Result[Result[T, E], E] in "std/result"`},
			deny: []string{"add `import"},
		},
		{
			name: "a generic receiver is not a concrete IoError receiver",
			src:  `function probe[A](x: Option[A]): void { x.failure(); } function main(): i32 { return 0; }`,
			want: []string{`no stdlib module declares one`},
			deny: []string{"add `import"},
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
