package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The diagnostic evidence comes from the same typed source-anchor walk that
// refuses production lowering. Callers refused only because their callee was
// refused must not acquire an independent source-escape diagnostic.
func TestSelfHostTypedViewEscape(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	const driver = `import "./lexer";
import "./parser";
import "./checker";
import "./semsource";
import "./util";
function main(): i32 {
  let mod = parser.module_with_builtins_typed(checker.annotate_module(parser.parse_module(lexer.tokenize(args()[1]))));
  let built = semsource.build_module(mod);
  if (args().len() > 2) {
    for d in semsource.check_views(mod) {
      print(d.code + ":" + util.i32_to_string(d.line) + ":" + util.i32_to_string(d.col));
    }
    return 0;
  }
  let i: i32 = 0;
  for p in built.decls {
    print(parser.decl_key(mod.funcs[i]) + ":" + util.i32_to_string(p.view_escape));
    i = i + 1;
  }
  for p in built.instances { print(p.key + ":" + util.i32_to_string(p.view_escape)); }
  return 0;
}

`
	if err := os.WriteFile(filepath.Join(dir, "typed_view_escape.fern"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "typed_view_escape.fern", "typed-view-escape")
	for _, tc := range []struct {
		name, source, want string
	}{
		{"local-string", `function f(): [u8] { let s: string = "a" + args()[0]; return s.as_bytes(); } function main(): i32 { return f().len(); }`, "f:1\nmain:0\n"},
		{"local-str", `function f(): [u8] { let s: string = "a" + args()[0]; let v: str = slice_unchecked(s, 0, 1); return v.as_bytes(); }`, "f:1\n"},
		{"parameter", `function f(s: string): [u8] { return s.as_bytes(); }`, "f:0\n"},
		{"str-parameter", `function f(s: str): [u8] { return s.as_bytes(); }`, "f:0\n"},
		{"static", `function f(): [u8] { return "a".as_bytes(); }`, "f:0\n"},
		{"call", `function view(s: string): [u8] { return s.as_bytes(); } function f(): [u8] { let s: string = "a" + args()[0]; return view(s); }`, "view:0\nf:1\n"},
		{"record", `struct Holder { bytes: [u8] } function f(): Holder { let s: string = "a" + args()[0]; return Holder { bytes: s.as_bytes() }; }`, "f:1\n"},
		{"owned-array", `function f(): u8[] { return [1, 2, 3]; }`, "f:0\n"},
		{"ordinary-array-view", `function f(): [i32] { let xs: i32[] = [1, 2, 3]; return xs[:]; }`, "f:1\n"},
		{"string-view-record", `struct Holder { text: str } function f(): Holder { let s: string = "a" + args()[0]; return Holder { text: slice_unchecked(s, 0, 1) }; }`, "f:2\n"},
		{"safe-array-unsafe-string", `struct Holder { bytes: [u8], text: str } function f(xs: [u8]): Holder { let s: string = "a" + args()[0]; return Holder { bytes: xs, text: slice_unchecked(s, 0, 1) }; }`, "f:2\n"},
		{"generic", `function hold[T](x: T): T { return x; } function f(): [u8] { let s: string = "a" + args()[0]; return hold(s.as_bytes()); }`, "hold:0\nf:1\nhold:0\n"},
		{"generic-local", `function local[T](x: T): [u8] { let s: string = "a" + args()[0]; return s.as_bytes(); } function main(): i32 { return local(1).len(); }`, "main:0\nlocal__i32:1\n"},
		{"branch-local", `function f(p: string, flag: boolean): [u8] { let s: string = p; if (flag) { s = "a" + args()[0]; } return s.as_bytes(); }`, "f:1\n"},
		{"reassigned-parameter", `function f(p: string): [u8] { let s: string = "a" + args()[0]; s = p; return s.as_bytes(); }`, "f:0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runX86_64Bin(runner, bin, tc.source).CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != strings.TrimSpace(tc.want) {
				t.Fatalf("typed escape evidence: %v\ngot %s\nwant %s", err, out, tc.want)
			}
		})
	}
	for _, tc := range []struct{ name, source, want string }{
		{"direct-diagnostic", "\nfunction f(): [u8] { let s: string = \"a\" + args()[0]; return s.as_bytes(); }", "E063:2:1"},
		{"generic-diagnostic", "\nfunction local[T](x: T): [u8] { let s: string = \"a\" + args()[0]; return s.as_bytes(); }\nfunction main(): i32 { return local(1).len() + local(true).len(); }", "E063:2:1"},
		{"string-diagnostic", "\nstruct Holder { text: str }\nfunction f(): Holder { let s: string = \"a\" + args()[0]; return Holder { text: slice_unchecked(s, 0, 1) }; }", "E065:3:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runX86_64Bin(runner, bin, tc.source, "diagnostics").CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != tc.want {
				t.Fatalf("typed diagnostic: %v\ngot %s\nwant %s", err, out, tc.want)
			}
		})
	}
}

func TestSelfHostCLIByteViewEscape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Run("imported-generic-locations", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"left", "right"} {
			source := `pub function view[T](x: T): [u8] { let s: string = "a" + args()[0]; return s.as_bytes(); }`
			if err := os.WriteFile(filepath.Join(dir, name+".fern"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(dir, "main.fern")
		source := `import "./left"; import "./right"; function main(): i32 { return left.view(1).len() + right.view(true).len(); }`
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib)
		out, err := cmd.CombinedOutput()
		if err == nil || cmd.ProcessState.ExitCode() != 1 || strings.Count(string(out), "error[E063]") != 2 {
			t.Fatalf("imported diagnostics: %v\n%s", err, out)
		}
		for _, name := range []string{"left", "right"} {
			if !strings.Contains(string(out), name+".fern:1:5: error[E063]") {
				t.Fatalf("missing source location for %s:\n%s", name, out)
			}
		}
	})
	for _, tc := range []struct {
		name, source string
		rejected     bool
	}{
		{"local-string", `function f(): [u8] { let s: string = "a" + args()[0]; return s.as_bytes(); } function main(): i32 { return f().len(); }`, true},
		{"local-str", `function f(): [u8] { let s: string = "a" + args()[0]; let v: str = slice_unchecked(s, 0, 1); return v.as_bytes(); } function main(): i32 { return f().len(); }`, true},
		{"unused-local-view", `function f(): [u8] { let s: string = "a" + args()[0]; return s.as_bytes(); } function main(): i32 { return 0; }`, true},
		{"record", `struct Holder { bytes: [u8] } function f(): Holder { let s: string = "a" + args()[0]; return Holder { bytes: s.as_bytes() }; } function main(): i32 { return f().bytes.len(); }`, true},
		{"call", `function view(s: string): [u8] { return s.as_bytes(); } function f(): [u8] { let s: string = "a" + args()[0]; return view(s); } function main(): i32 { return f().len(); }`, true},
		{"generic", `function local[T](x: T): [u8] { let s: string = "a" + args()[0]; return s.as_bytes(); } function main(): i32 { return local(1).len() + local(true).len(); }`, true},
		{"parameter-string", `function f(s: string): [u8] { return s.as_bytes(); } function main(): i32 { return f("a").len(); }`, false},
		{"parameter-str", `function f(s: str): [u8] { return s.as_bytes(); } function main(): i32 { return f("a").len(); }`, false},
		{"static", `function f(): [u8] { return "a".as_bytes(); } function main(): i32 { return f().len(); }`, false},
		{"owned-copy", `function f(): u8[] { let s: string = "a" + args()[0]; return s.bytes(); } function main(): i32 { return f().len(); }`, false},
		{"reassigned-parameter", `function f(p: string): [u8] { let s: string = "a" + args()[0]; s = p; return s.as_bytes(); } function main(): i32 { return f("a").len(); }`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte("import \"std/string\";\n"+tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib)
			out, err := cmd.CombinedOutput()
			code := cmd.ProcessState.ExitCode()
			want := 0
			if tc.rejected {
				want = 1
			}
			if code != want {
				t.Fatalf("check: %v, exit %d, want %d\n%s", err, code, want, out)
			}
			if tc.rejected && strings.Count(string(out), "error[E063]") != 1 {
				t.Fatalf("expected one E063 diagnostic, got:\n%s", out)
			}
		})
	}
}
