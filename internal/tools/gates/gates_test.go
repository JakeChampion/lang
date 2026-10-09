package gates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

// load returns src loaded the two ways `fern -check` reads an entry whose
// path is not absolute: as a file named relative to the working directory,
// and from stdin.
func load(t *testing.T, src string) map[string]func() (string, *ast.Program) {
	return map[string]func() (string, *ast.Program){
		"relative-path": func() (string, *ast.Program) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.fern"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			prog, _, err := modload.Load("main.fern")
			if err != nil {
				t.Fatal(err)
			}
			return "main.fern", prog
		},
		"stdin": func() (string, *ast.Program) {
			prog, _, err := modload.LoadSource(src)
			if err != nil {
				t.Fatal(err)
			}
			return "-", prog
		},
	}
}

// A violation in the entry module is placed in it, however the entry was
// named. Both gates compared the module modload stamps (an absolute path, or
// LoadSource's) against the entry as given, so a relative path or stdin lost
// the position and the message named the entry as an imported module.
func TestCheckPlacesEntryModuleViolations(t *testing.T) {
	cases := []struct {
		name, target, src, code string
		line, col               int
	}{
		{"ambient", "", `import "std/http";
import "std/platform";
function helper(): void { eprint("hit"); }
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    helper();
    return http.ok("");
}
function main(): i32 { return 0; }
`, "E080", 4, 1},
		{"target", "arm64-freestanding", `function main(): i32 {
  let r: Result[string, IoError] = read_file("x");
  return 0;
}
`, "E066", 2, 45},
	}
	for _, c := range cases {
		for how, ld := range load(t, c.src) {
			t.Run(c.name+"/"+how, func(t *testing.T) {
				entry, prog := ld()
				_, err := Check(entry, prog, c.target, nil)
				es, ok := err.(diag.Errors)
				if !ok || len(es) != 1 {
					t.Fatalf("want one %s, got %v", c.code, err)
				}
				e := es[0]
				if cd, ok := e.(diag.Coded); !ok || cd.Code() != c.code {
					t.Fatalf("want %s, got %v", c.code, e)
				}
				if p := e.(diag.Positioned).Position(); p.Line != c.line || p.Col != c.col {
					t.Errorf("at %d:%d, want %d:%d: %v", p.Line, p.Col, c.line, c.col, e)
				}
				if msg := e.Error(); strings.Contains(msg, "module") {
					t.Errorf("message names the entry as another module: %s", msg)
				}
			})
		}
	}
}

// The const fold runs before the type check, so a use of a top-level const
// is its value, not an undefined name (#11314).
func TestCheckFoldsConstsBeforeTheTypeCheck(t *testing.T) {
	prog, _, err := modload.LoadSource("const LIMIT: i32 = 10;\nfunction main(): i32 {\n  return LIMIT;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if ws, err := Check("-", prog, "", nil); err != nil || len(ws) != 0 {
		t.Fatalf("want a clean check, got warnings %v, error %v", ws, err)
	}
}

// A check that passes still reports the entry's `todo` stubs, at their
// positions.
func TestCheckWarnsOfTodoStubs(t *testing.T) {
	prog, _, err := modload.LoadSource("function f(): i32 {\n  todo;\n}\nfunction main(): i32 {\n  return 0;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := Check("-", prog, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 {
		t.Fatalf("want one warning, got %v", ws)
	}
	if got, want := ws[0].Format("<stdin>"), "<stdin>:2:3: warning: `todo` stub remaining"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := (Warning{Msg: "m"}).Format("x.fern"), "warning: m"; got != want {
		t.Errorf("a positionless warning: got %q, want %q", got, want)
	}
}

// A generic body is checked once with its type parameters open, so a capture
// of `x: T` is a view only in the instance at `str`: E082 comes from the
// re-check monomorphisation runs on that instance.
func TestCheckRefusesAGenericCaptureInstantiatedAtAView(t *testing.T) {
	const src = `function keep[T](x: T): () => T { return () => x; }
function main(): i32 {
    let s: string = "abc";
    let v: str = slice_unchecked(s, 0, 1);
    let g: () => str = keep(v);
    let n: () => i32 = keep(1);
    return g().len() + n();
}
`
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Check("-", prog, "", nil)
	if err == nil || !strings.Contains(diag.Format("main.fern", src, err), "error[E082]") {
		t.Fatalf("want E082 on the str instance, got %v", err)
	}
}
