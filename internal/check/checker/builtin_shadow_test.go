package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

func moduleCheckErrors(t *testing.T, src string) []*Error {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = Check(prog)
	if err == nil {
		return nil
	}
	es, ok := err.(diag.Errors)
	if !ok {
		t.Fatalf("checker returned %T, want diag.Errors: %v", err, err)
	}
	var out []*Error
	for _, e := range es {
		ce, ok := e.(*Error)
		if !ok {
			t.Fatalf("diagnostic %T is not a checker Error: %v", e, e)
		}
		out = append(out, ce)
	}
	return out
}

// An imported module that names a built-in type means the built-in, even when
// the entry declares a type of that name (#13357).
func TestEntryShadowOfBuiltinLeavesImportsTheBuiltin(t *testing.T) {
	cases := []struct {
		name, src, msg string
	}{
		{
			"enum-takes-builtin-struct-name",
			"import \"std/string\";\nenum Span { Empty, Wide(f64, string) }\nfunction f(s: Span): i32 {\n  match (s) { Wide(d, t) => { return (d * 4.0) as i32 + t.len(); }, Empty => { return 0; } }\n}\nfunction main(): i32 { return f(Wide(1.0, \"ab\")); }\n",
			`enum "Span" is a reserved built-in name`,
		},
		{
			"struct-takes-builtin-enum-name",
			"import \"std/string\";\nstruct Option { n: i32 }\nfunction main(): i32 { let o: Option = Option { n: 1 }; return o.n; }\n",
			`struct "Option" is a reserved built-in name`,
		},
		{
			"struct-takes-builtin-struct-name",
			"import \"std/string\";\nstruct Span { n: i32 }\nfunction main(): i32 { let s: Span = Span { n: 1 }; return s.n; }\n",
			`struct "Span" is a reserved built-in name`,
		},
		{
			"enum-takes-builtin-enum-name",
			"import \"std/string\";\nenum Option { A, B }\nfunction main(): i32 { let o: Option = A; return 0; }\n",
			`enum "Option" is a reserved built-in name`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := moduleCheckErrors(t, tc.src)
			if len(errs) != 1 || errs[0].ErrCode != "E010" {
				var got []string
				for _, e := range errs {
					got = append(got, e.ErrCode+" "+e.Path+": "+e.Msg)
				}
				t.Fatalf("want exactly one E010, got %d:\n  %s", len(errs), strings.Join(got, "\n  "))
			}
			if !strings.Contains(errs[0].Msg, tc.msg) {
				t.Errorf("E010 message %q does not contain %q", errs[0].Msg, tc.msg)
			}
		})
	}
}

func TestEntryShadowOfBuiltinIsNamedAsWritten(t *testing.T) {
	errs := moduleCheckErrors(t, "import \"std/string\";\nstruct Span { n: i32 }\nfunction main(): i32 { let s: Span = Span { n: 1 }; let t: string = s; return 0; }\n")
	var e003 *Error
	for _, e := range errs {
		if strings.Contains(e.Msg, "__") {
			t.Errorf("%s leaks the bundle name: %s", e.ErrCode, e.Msg)
		}
		if e.ErrCode == "E003" {
			e003 = e
		}
	}
	if e003 == nil || !strings.Contains(e003.Msg, "got Span") {
		t.Fatalf("want an E003 naming the entry's Span, got %v", errs)
	}
}
