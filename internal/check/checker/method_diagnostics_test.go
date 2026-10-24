package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestUnknownMethodDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{"retired array spelling names its replacement",
			`function main(): i32 { let xs: i32[] = [1]; xs.push(2); return 0; }`,
			[]string{`no method "push" on i32[]`, `use "append"`, "assign the result back"}},
		{"retired array set names its replacement",
			`function main(): i32 { let xs: i32[] = [1]; xs.set(0, 2); return 0; }`,
			[]string{`no method "set" on i32[]`, `use "with"`}},
		{"retired map spelling names its replacement",
			`import "core/map";
			 function main(): i32 { let m: Map[string, i32] = Map { "a": 1 }; m = m.set("b", 2); return 0; }`,
			[]string{`no method "set" on Map`, `use "insert"`}},
		{"near-miss on an array suggests the real method",
			`function main(): i32 { let xs: i32[] = [1]; xs.appendd(2); return 0; }`,
			[]string{`no method "appendd" on i32[]`, `did you mean "append"?`}},
		{"unknown string method points at the module that would define it",
			`function main(): i32 { let s: string = " x "; print(s.trim()); return 0; }`,
			[]string{`no method "trim" on string`, "add `import \"std/string\"`"}},
		{"unrecognisable name lists what the receiver has",
			`function main(): i32 { let xs: i32[] = [1]; xs.frobnicate(); return 0; }`,
			[]string{`no method "frobnicate" on i32[]`, "it has:", "append", "len"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Check(prog)
			if err == nil {
				t.Fatalf("checked clean, want an unknown-method error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v\nwant it to contain %q", err, want)
				}
			}
		})
	}
}

func TestUnknownMethodDiagnosticsLeaveNeighboursAlone(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"scalar method still names the missing import",
			`function main(): i32 { let n: i32 = 5; print(n.to_string()); return 0; }`,
			"add `import \"std/i32\"`"},
		{"struct field typo still reads as a field",
			`struct Point { x: i32, y: i32 }
			 function main(): i32 { let p: Point = Point { x: 1, y: 2 }; return p.z; }`,
			`struct Point has no field or method "z"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Check(prog)
			if err == nil {
				t.Fatalf("checked clean, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestUnknownMemberNamesTheImportedStruct(t *testing.T) {
	err := checkModuleSource(t, `import "core/iter" as it;
function main(): i32 { let total: i32 = it.of([1, 2, 3]).sum(); return total; }`)
	if err == nil {
		t.Fatal("expected E043 for a method ArrayIter does not have")
	}
	if got := err.Error(); !strings.Contains(got, `struct iter.ArrayIter has no field or method "sum"`) || strings.Contains(got, "__") {
		t.Errorf("error = %s", got)
	}
}
