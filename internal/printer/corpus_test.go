package printer

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/parser"
)

// The two properties in this file are CORPUS-driven: they walk every `.fern`
// file under examples/ + internal/stdlib rather than a fixture list.
//
// That is the point. #6832 added the type-check property over
// `fmtParityCases`, a hand-maintained list, and #6812 then landed
// construction-site type arguments that neither printer emitted while the
// property stayed green — new syntax is invisible to a gate that only sees the
// rows somebody remembered to add, and new syntax is exactly when printer /
// parser drift happens (#6838). A corpus run needs no maintenance: a file is
// covered the moment it exists.
//
// They live in internal/printer, not internal/e2eselfhost, because that is
// where the unit lane runs them. The self-host lane executes only tests
// matching `^TestSelfHost` (`-test.list '^TestSelfHost'` in the shard
// partition, and a named `-test.run` in every other job), and
// scripts/unit-test-packages excludes internal/e2eselfhost — so a
// differently-named test in that package runs nowhere.

// corpusRoot returns the repository root, found by walking up from the test's
// working directory to the go.mod.
func corpusRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test's working directory")
		}
		dir = parent
	}
}

// corpusFiles lists every `.fern` file under examples/ and internal/stdlib,
// relative to the repository root, in sorted order.
func corpusFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, sub := range []string{"examples", filepath.Join("internal", "stdlib")} {
		err := filepath.WalkDir(filepath.Join(root, sub), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".fern" {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out = append(out, rel)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(out) < 400 {
		t.Fatalf("corpus walk found only %d files; the tree moved and this gate stopped covering it", len(out))
	}
	sort.Strings(out)
	return out
}

// formatSource is what `fern -fmt FILE` does: parse the one file, print it.
func formatSource(src string) (string, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return "", err
	}
	return Format(prog), nil
}

// TestFormatCorpusOutputTypeChecks states the property #6802 asks for over the
// whole corpus: formatting a program that compiles must yield a program that
// still compiles.
//
// Conditional on the input compiling, because a corpus file is not always a
// standalone program — several stdlib modules do not type-check on their own,
// and a formatter cannot be blamed for that.
//
// The check runs against a MIRROR of the tree with the one file replaced, so
// relative imports (`import "./parser"`) and stdlib imports resolve exactly as
// they do in place.
func TestFormatCorpusOutputTypeChecks(t *testing.T) {
	root := corpusRoot(t)
	files := corpusFiles(t, root)
	mirror := mirrorCorpus(t, root)

	checked := 0
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(mirror, rel)
		if typeChecksAt(path) != nil {
			continue
		}
		formatted, err := formatSource(string(src))
		if err != nil {
			t.Errorf("%s: parses in place but not through the formatter: %v", rel, err)
			continue
		}
		if err := os.WriteFile(path, []byte(formatted), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := typeChecksAt(path); err != nil {
			t.Errorf("%s: compiles as written, does not compile after -fmt: %v", rel, err)
		}
		if err := os.WriteFile(path, src, 0o644); err != nil {
			t.Fatal(err)
		}
		checked++
	}
	if checked < 300 {
		t.Errorf("only %d of %d corpus files compiled as written; the gate is asserting far less than it looks like", checked, len(files))
	}
}

// TestFormatCorpusRoundtripShape is the structural half, and the stronger of the
// two: parse → print → parse must yield the same AST modulo source position.
//
// A type-check property passes on output that still compiles while having lost
// information — a dropped `Box[i64]` instantiation re-infers as `Box[i32]` and
// compiles fine — so it sees only the subset of data loss that stops compiling.
// A structural comparison cannot be fooled that way, and unlike the type-check
// property it applies to every file whether or not it is a standalone program.
func TestFormatCorpusRoundtripShape(t *testing.T) {
	root := corpusRoot(t)
	for _, rel := range corpusFiles(t, root) {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		first, err := parser.Parse(string(src))
		if err != nil {
			// A corpus file that does not parse is not this gate's business.
			continue
		}
		printed := Format(first)
		second, err := parser.Parse(printed)
		if err != nil {
			t.Errorf("%s: formatted output does not parse: %v", rel, err)
			continue
		}
		want, got := astShape(first), astShape(second)
		if want == got {
			continue
		}
		t.Errorf("%s: -fmt changed the AST\n%s", rel, firstShapeDiff(want, got))
	}
}

// TestFormatCorpusIdempotent is the third property, also corpus-driven:
// formatting already-formatted source must change nothing.
//
// internal/printer/idempotence_test.go holds the same property over a fixture
// list. Over the corpus it costs one extra format pass and catches the shape the
// fixtures cannot: an instability that only appears in a construct nobody wrote a
// row for.
func TestFormatCorpusIdempotent(t *testing.T) {
	root := corpusRoot(t)
	for _, rel := range corpusFiles(t, root) {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		once, err := formatSource(string(src))
		if err != nil {
			continue
		}
		twice, err := formatSource(once)
		if err != nil {
			t.Errorf("%s: formatted output does not parse: %v", rel, err)
			continue
		}
		if once != twice {
			t.Errorf("%s: -fmt is not idempotent\n%s", rel, firstShapeDiff(once, twice))
		}
	}
}

// mirrorCorpus copies examples/ + internal/stdlib into a temporary directory so
// a formatted file can be type-checked with its imports intact without writing
// into the working tree.
func mirrorCorpus(t *testing.T, root string) string {
	t.Helper()
	dst := t.TempDir()
	for _, sub := range []string{"examples", filepath.Join("internal", "stdlib")} {
		err := filepath.WalkDir(filepath.Join(root, sub), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			target := filepath.Join(dst, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, b, 0o644)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

// typeChecksAt runs the load → constfold → check chain `fern -check` does.
func typeChecksAt(path string) error {
	prog, _, err := modload.Load(path)
	if err != nil {
		return err
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return err
	}
	_, err = checker.Check(prog)
	return err
}

// astShape renders an AST as a position-free structural string. Every
// ast.Position field is skipped, as are the three whitespace-keyed side tables
// (TypeRefs / Comments / BlankLines) whose line numbers necessarily move when
// the source is re-laid-out.
//
// Generated by reflection rather than a hand-written walk: a switch over node
// kinds only covers the kinds somebody wrote a case for, which over a whole
// corpus is the difference between a gate and a false pass.
func astShape(prog *ast.Program) string {
	var b strings.Builder
	shapeValue(&b, reflect.ValueOf(prog))
	return b.String()
}

var positionType = reflect.TypeOf(ast.Position{})

// synthPos matches the `_<line>_<col>` a parser-synthesised name carries —
// `__discard_243_9_1` for a `_` destructure binding, `__forc_12_5` for a C-style
// for's flag. Those are positions spelled inside a string, so they move when the
// source is re-laid-out even though nothing about the program changed.
var synthPos = regexp.MustCompile(`_\d+_\d+`)

func normalizeSynthName(s string) string {
	if !strings.HasPrefix(s, "__") {
		return s
	}
	return synthPos.ReplaceAllString(s, "_#_#")
}

// skippedShapeFields are the Program side tables keyed by source whitespace.
var skippedShapeFields = map[string]bool{"TypeRefs": true, "Comments": true, "BlankLines": true}

func shapeValue(b *strings.Builder, v reflect.Value) {
	switch v.Kind() {
	case reflect.Invalid:
		b.WriteString("nil")
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		shapeValue(b, v.Elem())
	case reflect.Struct:
		if v.Type() == positionType {
			return
		}
		b.WriteString(v.Type().Name())
		b.WriteByte('{')
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.Type == positionType || skippedShapeFields[f.Name] {
				continue
			}
			b.WriteString(f.Name)
			b.WriteByte(':')
			shapeValue(b, v.Field(i))
			b.WriteByte(' ')
		}
		b.WriteByte('}')
	case reflect.Slice, reflect.Array:
		b.WriteByte('[')
		for i := 0; i < v.Len(); i++ {
			shapeValue(b, v.Index(i))
			b.WriteByte(' ')
		}
		b.WriteByte(']')
	case reflect.Map:
		keys := make([]string, 0, v.Len())
		byKey := map[string]reflect.Value{}
		for _, k := range v.MapKeys() {
			var kb strings.Builder
			shapeValue(&kb, k)
			keys = append(keys, kb.String())
			byKey[kb.String()] = v.MapIndex(k)
		}
		sort.Strings(keys)
		b.WriteString("map[")
		for _, k := range keys {
			b.WriteString(k)
			b.WriteByte(':')
			shapeValue(b, byKey[k])
			b.WriteByte(' ')
		}
		b.WriteByte(']')
	case reflect.String:
		b.WriteString(strconv.Quote(normalizeSynthName(v.String())))
	case reflect.Bool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		b.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		b.WriteString(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	default:
		b.WriteString(v.Kind().String())
	}
}

// firstShapeDiff reports the first divergence between two shape strings with a
// window of context, since the whole shape of a corpus file is megabytes.
func firstShapeDiff(want, got string) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	lo := i - 200
	if lo < 0 {
		lo = 0
	}
	clip := func(s string) string {
		hi := i + 200
		if hi > len(s) {
			hi = len(s)
		}
		return s[lo:hi]
	}
	return "--- as written ---\n" + clip(want) + "\n--- after -fmt ---\n" + clip(got)
}
