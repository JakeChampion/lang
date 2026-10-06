package checker

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// The self-host mirrors part of the checker's builtin struct table — in
// TWO places, and both have to agree with it.
//
// builtinStructDecls injects HttpRequest / HttpResponse / HeaderMap /
// Stream and friends into every native program. The
// self-host cannot inject them the same way, so it carries its own
// copies, and that is where it reads field OFFSETS and the shapes for
// chained access from:
//
//   - compiler/builtins.fern, `struct` declarations for the
//     import-driven driver;
//   - compiler/parser.fern, which appends StructDecl values
//     to the table for the paths that never read builtins.fern (the
//     asm_load_run driver, loading from a stdlib root).
//
// Nothing pinned any of them together. A field added, removed or
// retyped on the native side leaves the self-host resolving the old
// offsets against a struct the native backend now lays out
// differently, which is a wrong-address read rather than a diagnostic;
// and a field whose STRUCT type is stale there stops chained access
// (`req.body.data`) resolving at all, which bails the whole module out
// of the IR path. Both happened when HttpRequest.body became a Stream:
// builtins.fern was updated and parser.fern was not.
//
// Each mirror is deliberately a SUBSET: it declares only what its
// sources need. A checker builtin absent from a mirror is fine; one
// that disagrees is not.
func TestSelfHostBuiltinStructsMatchChecker(t *testing.T) {
	native := map[string]string{}
	for _, sd := range builtinStructDecls() {
		native[sd.Name] = fernStructBody(t, sd)
	}
	for _, m := range []struct {
		path  string
		parse func(*testing.T, string) map[string]string
		min   int
	}{
		{"../../../compiler/builtins.fern", parseFernStructDecls, 4},
		{"../../../compiler/parser.fern", parseInjectedStructDecls, 4},
	} {
		t.Run(filepath.Base(m.path), func(t *testing.T) {
			src, err := os.ReadFile(m.path)
			if err != nil {
				t.Fatalf("read %s: %v", m.path, err)
			}
			mirror := m.parse(t, string(src))
			checked := 0
			for name, fields := range mirror {
				want, isBuiltin := native[name]
				if !isBuiltin {
					continue // a self-host-only record; not this gate's business
				}
				checked++
				if fields != want {
					t.Errorf("%s: %s declares\n\t{ %s }\nbut the checker's builtin table says\n\t{ %s }\n"+
						"the self-host reads field offsets and chained-access shapes off its own copy, "+
						"so a disagreement is a wrong-address read or a whole-module IR bail",
						name, filepath.Base(m.path), fields, want)
				}
			}
			// Guard the gate itself: a rename or a reshaped injection site
			// that made every name stop matching would otherwise pass silently.
			if checked < m.min {
				t.Errorf("only %d builtin structs were compared in %s; it should mirror at least "+
					"HeaderMap, Stream, HttpRequest and HttpResponse", checked, filepath.Base(m.path))
			}
		})
	}
}

// parseFernStructDecls reads plain `struct N { a: T, ... }` declarations.
func parseFernStructDecls(t *testing.T, src string) map[string]string {
	t.Helper()
	decl := regexp.MustCompile(`(?m)^struct\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{([^}]*)\}`)
	out := map[string]string{}
	for _, m := range decl.FindAllStringSubmatch(src, -1) {
		out[m[1]] = normaliseFields(m[2])
	}
	if len(out) == 0 {
		t.Fatal("no struct declarations found")
	}
	return out
}

// parseInjectedStructDecls reads parser.fern's table injections: an
// `if (!name_declared(structs, "N"))` block whose body appends one
// StructFieldDecl per field, in order.
func parseInjectedStructDecls(t *testing.T, src string) map[string]string {
	t.Helper()
	// The guard is `name_declared`, which answers for an enum of that name
	// as well as a struct; `struct_declared` is the struct-only half it calls.
	// A block ends at the first `}` indented as far as its `if` line.
	head := regexp.MustCompile(`(?m)^([ \t]*)if \(!(?:name|struct)_declared\(structs, "([A-Za-z_][A-Za-z0-9_]*)"\)\) \{`)
	field := regexp.MustCompile(`StructFieldDecl \{[^}]*\}`)
	out := map[string]string{}
	for _, m := range head.FindAllStringSubmatchIndex(src, -1) {
		rest := src[m[1]:]
		end := strings.Index(rest, "\n"+src[m[2]:m[3]]+"}")
		if end < 0 {
			t.Fatalf("the injection block for %s has no closing brace at its own indent", src[m[4]:m[5]])
		}
		var parts []string
		for _, f := range field.FindAllString(rest[:end], -1) {
			parts = append(parts, injectedFieldSpelling(f))
		}
		if len(parts) > 0 {
			out[src[m[4]:m[5]]] = strings.Join(parts, ", ")
		}
	}
	if len(out) == 0 {
		t.Fatal("no injected struct declarations found — has the injection site been reshaped?")
	}
	return out
}

// injectedFieldSpelling renders one StructFieldDecl literal as builtins.fern
// spells the field. A fn-typed field carries the flat "fn" tag with its
// return and parameter spellings in the fn_ret / fn_param_types sidecars,
// which is where the `(P) => R` spelling is rebuilt from.
func injectedFieldSpelling(lit string) string {
	attr := func(key string) string {
		m := regexp.MustCompile(key + `: "([^"]*)"`).FindStringSubmatch(lit)
		if m == nil {
			return ""
		}
		return m[1]
	}
	name, ty := attr("name"), attr("type_name")
	if ty == "fn" {
		ty = "(" + attr("fn_param_types") + ") => " + attr("fn_ret")
	}
	return name + ": " + ty
}

// fernStructBody spells a builtin's fields the way builtins.fern does.
func fernStructBody(t *testing.T, sd *ast.StructDecl) string {
	t.Helper()
	parts := make([]string, 0, len(sd.Fields))
	for _, f := range sd.Fields {
		parts = append(parts, f.Name+": "+fernTypeSpelling(t, f.Type))
	}
	return strings.Join(parts, ", ")
}

func fernTypeSpelling(t *testing.T, ty ast.Type) string {
	t.Helper()
	switch v := ty.(type) {
	case ast.StringType:
		return "string"
	case ast.BoolType:
		return "boolean"
	case ast.FloatType:
		if v.Width == 32 {
			return "f32"
		}
		return "f64"
	case ast.NumberType:
		width := v.Width
		if width == 0 {
			width = 32
		}
		if v.Signed || v.Width == 0 {
			return fmt.Sprintf("i%d", width)
		}
		return fmt.Sprintf("u%d", width)
	case ast.ArrayType:
		return fernTypeSpelling(t, v.Elem) + "[]"
	case ast.StructType:
		if len(v.Args) == 0 {
			return v.Name
		}
		args := make([]string, 0, len(v.Args))
		for _, a := range v.Args {
			args = append(args, fernTypeSpelling(t, a))
		}
		return v.Name + "[" + strings.Join(args, ", ") + "]"
	case ast.EnumType:
		if len(v.Args) == 0 {
			return v.Name
		}
		args := make([]string, 0, len(v.Args))
		for _, a := range v.Args {
			args = append(args, fernTypeSpelling(t, a))
		}
		return v.Name + "[" + strings.Join(args, ", ") + "]"
	case *ast.FuncType:
		params := make([]string, 0, len(v.Params))
		for _, p := range v.Params {
			params = append(params, fernTypeSpelling(t, p))
		}
		return "(" + strings.Join(params, ", ") + ") => " + fernTypeSpelling(t, v.Result)
	}
	t.Fatalf("no Fern spelling for builtin field type %T — teach fernTypeSpelling about it", ty)
	return ""
}

// normaliseFields collapses whitespace so the comparison is on field
// names and types, not on how the declaration is laid out.
func normaliseFields(s string) string {
	var fields []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				fields = append(fields, s[start:i])
				start = i + 1
			}
		}
	}
	fields = append(fields, s[start:])
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Join(strings.Fields(f), " ")
		if f != "" {
			out = append(out, f)
		}
	}
	return strings.Join(out, ", ")
}
