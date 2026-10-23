package e2ecompiler

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestSelfHostCheckerOpaqueDifferentialX86_64 holds both checkers to the
// `opaque` struct rule (#12658, docs/TRAITS.md): outside the module that
// declares it, an opaque struct's fields cannot be read, assigned,
// destructured or constructed, while its name, its factory functions and its
// methods stay usable. Each row pins its expected codes in both checkers and
// requires the same E021 diagnostics, message and count, from each — a pure
// differential cannot tell both reporting E021 from neither reporting it.
func TestSelfHostCheckerOpaqueDifferentialX86_64(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)
	modules := map[string]string{
		"email.fern": `pub opaque struct Email { addr: string, n: i32 }
pub struct Open { addr: string, n: i32 }
pub function make(a: string): Email { return Email { addr: a, n: 1 }; }
pub function open(a: string): Open { return Open { addr: a, n: 1 }; }
pub function (e: Email) domain(): string { return e.addr; }
pub function inside(e: Email): i32 {
    let Email { n, .. } = e;
    let e2: Email = Email { ...e, n: 4 };
    let f = (x: Email): i32 => x.n;
    match (e2) { Email { addr, n: k } => { return n + k + addr.len() + f(e2); } }
}
`,
		"gbox.fern": `pub opaque struct Box[T] { v: T }
pub function mk(x: i32): Box[i32] { return Box { v: x }; }
pub function get(b: Box[i32]): i32 { return b.v; }
`,
		"greet.fern": `pub opaque struct Token { id: i32 }
pub function mk(): Token { return Token { id: 41 }; }
pub trait Greet {
    function tag(self: Self): i32;
    function greet(self: Self): i32 { let t: Token = mk(); return t.id + self.tag(); }
}
`,
	}
	e021 := []string{"E021"}
	cases := []struct {
		name, main string
		want       []string
	}{
		{"field-read", "import \"./email\";\nfunction main(): i32 { let e: email.Email = email.make(\"x\"); return e.n; }\n", e021},
		{"field-read-twice", "import \"./email\";\nfunction main(): i32 { let e: email.Email = email.make(\"x\"); return e.n + e.n; }\n", e021},
		{"field-read-in-a-lambda", "import \"./email\";\nfunction main(): i32 { let f = (e: email.Email): i32 => e.n; return f(email.make(\"x\")); }\n", e021},
		{"field-read-generic-instance", "import \"./gbox\";\nfunction main(): i32 { let b: gbox.Box[i32] = gbox.mk(3); return b.v; }\n", e021},
		{"field-read-unknown-field", "import \"./email\";\nfunction main(): i32 { let e: email.Email = email.make(\"x\"); return e.zzz; }\n", []string{"E021", "E043"}},
		{"field-read-in-a-receiver-declared-elsewhere", "import \"./email\";\nfunction (e: email.Email) peek(): i32 { return e.n; }\nfunction main(): i32 { return email.make(\"x\").peek(); }\n", e021},
		{"field-read-in-an-impl-method-declared-elsewhere", "import \"./greet\";\nstruct R { n: i32 }\nimpl greet.Greet for R { function tag(self: Self): i32 { let t: greet.Token = greet.mk(); return t.id; } }\nfunction main(): i32 { let r: R = R { n: 1 }; return r.greet(); }\n", e021},
		{"field-assign", "import \"./email\";\nfunction main(): i32 { let e: email.Email = email.make(\"x\"); e.n = 3; return 0; }\n", []string{"E021", "E048"}},
		{"construct", "import \"./email\";\nfunction main(): i32 { let e: email.Email = email.Email { addr: \"x\", n: 2 }; return 0; }\n", e021},
		{"construct-missing-field", "import \"./email\";\nfunction main(): i32 { let e: email.Email = email.Email { addr: \"x\" }; return 0; }\n", []string{"E005", "E021"}},
		{"struct-update", "import \"./email\";\nfunction main(): i32 { let e: email.Email = email.make(\"x\"); let f: email.Email = email.Email { ...e, n: 2 }; return 0; }\n", e021},
		// A destructure names the struct by its declared spelling, which an
		// importer reaches only as the mangled one.
		{"let-destructure", "import \"./email\";\nfunction main(): i32 { let e: email.Email = email.make(\"x\"); let email__Email { n, .. } = e; return n; }\n", e021},
		{"factory-and-method", "import \"./email\";\nfunction main(): i32 { let e: email.Email = email.make(\"ab\"); return e.domain().len() + email.inside(e); }\n", nil},
		{"generic-through-accessor", "import \"./gbox\";\nfunction main(): i32 { let b: gbox.Box[i32] = gbox.mk(3); return gbox.get(b); }\n", nil},
		// A trait default body is checked as the trait module's own source.
		{"trait-default-in-the-declaring-module", "import \"./greet\";\nstruct R { n: i32 }\nimpl greet.Greet for R { function tag(self: Self): i32 { return self.n; } }\nfunction main(): i32 { let r: R = R { n: 1 }; return r.greet(); }\n", nil},
		{"pub-struct-not-opaque", "import \"./email\";\nfunction main(): i32 { let o: email.Open = email.open(\"x\"); let p: email.Open = email.Open { ...o, n: 3 }; let q: email.Open = email.Open { addr: \"y\", n: 4 }; return o.n + p.n + q.addr.len(); }\n", nil},
		{"entry-module-opaque", "pub opaque struct S { x: i32 }\nfunction main(): i32 { let s: S = S { x: 3 }; let t: S = S { ...s, x: 4 }; let S { x } = t; match (s) { S { x: y } => { return x + y + t.x - 10; } } }\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range modules {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			entry := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(entry, []byte(tc.main), 0o644); err != nil {
				t.Fatal(err)
			}
			goDiags := goCheckerDiags(t, dir, tc.main)
			if got := uniqueSortedCodes(diagCodesOf(goDiags)); !equalStrings(got, tc.want) {
				t.Errorf("Go checker codes %v, want %v\nsrc: %s\ndiags: %v", got, tc.want, tc.main, goDiags)
			}
			out := checkFileModload(t, runner, driverBin, entry)
			shDiags := driverDiags(out)
			if got := driverCodes(out); !equalStrings(got, tc.want) {
				t.Errorf("self-host checker codes %v, want %v\nsrc: %s\noutput:\n%s", got, tc.want, tc.main, out)
			}
			if g, s := e021Messages(goDiags), e021Messages(shDiags); !equalStrings(g, s) {
				t.Errorf("E021 diagnostics differ\nGo:        %q\nself-host: %q\nsrc: %s", g, s, tc.main)
			}
		})
	}
}

func diagCodesOf(ds []driverDiag) []string {
	var codes []string
	for _, d := range ds {
		if d.code != "" {
			codes = append(codes, d.code)
		}
	}
	return codes
}

// e021Messages is every E021 message, duplicates kept, sorted.
func e021Messages(ds []driverDiag) []string {
	var out []string
	for _, d := range ds {
		if d.code == "E021" {
			out = append(out, d.msg)
		}
	}
	sort.Strings(out)
	return out
}
