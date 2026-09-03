package checker

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// The self-host mirrors part of the checker's builtin struct table.
//
// builtinStructDecls injects HttpRequest / HttpResponse / HeaderMap /
// Stream / Platform and friends into every native program; the
// self-host compiler cannot inject them the same way, so
// examples/self_host/builtins.fern carries `struct` declarations for
// the ones its own sources need, and that is where the self-host reads
// its field OFFSETS from.
//
// Nothing pinned the two together. A field added, removed or retyped
// on the native side leaves the self-host resolving the old offsets
// against a struct the native backend now lays out differently, which
// is a wrong-address read, not a diagnostic — so this gate holds every
// name the mirror does declare to the checker's spelling, field for
// field and in order.
//
// The mirror is deliberately a SUBSET: the self-host declares only what
// its sources mention. A checker builtin absent from builtins.fern is
// fine; one that disagrees is not.
func TestSelfHostBuiltinStructsMatchChecker(t *testing.T) {
	const mirrorPath = "../../examples/self_host/builtins.fern"
	src, err := os.ReadFile(mirrorPath)
	if err != nil {
		t.Fatalf("read %s: %v", mirrorPath, err)
	}

	native := map[string]string{}
	for _, sd := range builtinStructDecls() {
		native[sd.Name] = fernStructBody(t, sd)
	}

	decl := regexp.MustCompile(`(?m)^struct\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{([^}]*)\}`)
	matches := decl.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		t.Fatalf("no struct declarations found in %s", mirrorPath)
	}

	checked := 0
	for _, m := range matches {
		name, fields := m[1], normaliseFields(m[2])
		want, isBuiltin := native[name]
		if !isBuiltin {
			continue // a self-host-only record; not this gate's business
		}
		checked++
		if fields != want {
			t.Errorf("%s: builtins.fern declares\n\t{ %s }\nbut the checker's builtin table says\n\t{ %s }\n"+
				"the self-host reads field offsets off its own declaration, so a disagreement is a wrong-address read",
				name, fields, want)
		}
	}
	// Guard the gate itself: a rename on either side that made every
	// name stop matching would otherwise pass silently.
	if checked < 4 {
		t.Errorf("only %d builtin structs were compared; builtins.fern should mirror at least HeaderMap, Stream, HttpRequest and HttpResponse", checked)
	}
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
	}
	t.Fatalf("no Fern spelling for builtin field type %T — teach fernTypeSpelling about it", ty)
	return ""
}

// normaliseFields collapses whitespace so the comparison is on field
// names and types, not on how the declaration is laid out.
func normaliseFields(s string) string {
	fields := strings.Split(s, ",")
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Join(strings.Fields(f), " ")
		if f != "" {
			out = append(out, f)
		}
	}
	return strings.Join(out, ", ")
}
