package constfold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/embed"
	"github.com/jakechampion/lang/internal/parser"
)

// assetSet writes name -> contents to a temp dir and loads it as an
// embed.Set, mirroring what `-embed DIR` builds at the CLI.
func assetSet(t *testing.T, files map[string]string) *embed.Set {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, err := embed.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// foldAssets parses src and folds it against an asset set.
func foldAssets(t *testing.T, src string, set *embed.Set) *ast.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Fold(prog, set); err != nil {
		t.Fatalf("fold: %v", err)
	}
	return prog
}

// foldAssetsErr expects the fold to fail and returns the message.
func foldAssetsErr(t *testing.T, src string, set *embed.Set) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Fold(prog, set); err != nil {
		return err.Error()
	}
	t.Fatal("expected a fold error but got none")
	return ""
}

// firstStringLit returns the first StringLit anywhere in the program, which
// for these single-expression fixtures is the substituted asset.
func firstStringLit(t *testing.T, prog *ast.Program) string {
	t.Helper()
	var found *ast.StringLit
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if s, ok := n.(*ast.StringLit); ok {
			found = s
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("no StringLit in the folded program — the asset call was not substituted")
	}
	return found.Value
}

func TestAssetSubstitutedInFunctionBody(t *testing.T) {
	set := assetSet(t, map[string]string{"html/index.html": "<h1>hi</h1>"})
	prog := foldAssets(t, `
function main(): i32 {
  var page: string = __fern_asset("html/index.html");
  return 0;
}`, set)
	if got := firstStringLit(t, prog); got != "<h1>hi</h1>" {
		t.Fatalf("substituted %q, want %q", got, "<h1>hi</h1>")
	}
}

// A const initialiser is resolved by evalConst, a different path from the
// body substituter — an asset has to work in both.
func TestAssetSubstitutedInConstInitialiser(t *testing.T) {
	set := assetSet(t, map[string]string{"page.html": "<p>const</p>"})
	prog := foldAssets(t, `
const PAGE: string = __fern_asset("page.html");
function main(): i32 {
  var p: string = PAGE;
  return 0;
}`, set)
	if got := firstStringLit(t, prog); got != "<p>const</p>" {
		t.Fatalf("substituted %q, want %q", got, "<p>const</p>")
	}
}

// Assets are constants, so const-expression arithmetic still applies to
// them: concatenation must fold at compile time like any string literal.
func TestAssetFoldsIntoConstConcatenation(t *testing.T) {
	set := assetSet(t, map[string]string{"a.txt": "AAA", "b.txt": "BBB"})
	prog := foldAssets(t, `
const BOTH: string = __fern_asset("a.txt") + __fern_asset("b.txt");
function main(): i32 {
  var p: string = BOTH;
  return 0;
}`, set)
	if got := firstStringLit(t, prog); got != "AAABBB" {
		t.Fatalf("concatenated to %q, want %q", got, "AAABBB")
	}
}

// NUL bytes and bytes >= 0x80 have to survive substitution intact: the
// emitted literal carries an explicit length, so the NUL is not a
// terminator and the high bytes are not re-encoded.
func TestAssetCarriesBinaryBytes(t *testing.T) {
	blob := string([]byte{0x00, 0x80, 0xff, 0x00, 'e', 'n', 'd'})
	set := assetSet(t, map[string]string{"blob.bin": blob})
	prog := foldAssets(t, `
function main(): i32 {
  var b: string = __fern_asset("blob.bin");
  return 0;
}`, set)
	if got := firstStringLit(t, prog); got != blob {
		t.Fatalf("binary asset became % x, want % x", got, blob)
	}
}

func TestAssetErrors(t *testing.T) {
	set := assetSet(t, map[string]string{"html/index.html": "x", "style.css": "y"})
	tests := []struct {
		name string
		src  string
		set  *embed.Set
		want string
	}{
		{
			name: "unknown asset suggests the near miss",
			src:  `function main(): i32 { var s: string = __fern_asset("html/index.htm"); return 0; }`,
			set:  set,
			want: `did you mean "html/index.html"`,
		},
		{
			name: "unknown asset with no near miss lists what is available",
			src:  `function main(): i32 { var s: string = __fern_asset("zzzzzzzzzz"); return 0; }`,
			set:  set,
			want: "embedded assets: html/index.html, style.css",
		},
		{
			name: "no -embed at all",
			src:  `function main(): i32 { var s: string = __fern_asset("a.txt"); return 0; }`,
			set:  nil,
			want: "no assets were embedded — pass -embed DIR",
		},
		{
			name: "computed name is rejected",
			src:  `function main(): i32 { var n: string = "a"; var s: string = __fern_asset(n); return 0; }`,
			set:  set,
			want: "needs a string literal",
		},
		{
			name: "wrong arity is rejected",
			src:  `function main(): i32 { var s: string = __fern_asset("a", "b"); return 0; }`,
			set:  set,
			want: "takes exactly one argument, got 2",
		},
		{
			name: "unknown asset in a const initialiser",
			src:  `const P: string = __fern_asset("nope-not-here"); function main(): i32 { var s: string = P; return 0; }`,
			set:  set,
			want: "no embedded asset",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := foldAssetsErr(t, tc.src, tc.set)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("error was %q,\nwant it to contain %q", got, tc.want)
			}
		})
	}
}

// Passing -embed does not change a program that never asks for an asset.
func TestAssetsUnusedIsHarmless(t *testing.T) {
	set := assetSet(t, map[string]string{"unused.txt": "x"})
	foldAssets(t, `
const N: i32 = 2 + 3;
function main(): i32 { return N; }`, set)
}

// --- slice 3: enumeration -----------------------------------------

// tupleElems returns the (name, contents) pairs of the array literal
// __fern_assets() expanded to, in emitted order.
func tupleElems(t *testing.T, prog *ast.Program) [][2]string {
	t.Helper()
	var arr *ast.ArrayLit
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if arr != nil {
			return false
		}
		if a, ok := n.(*ast.ArrayLit); ok {
			arr = a
			return false
		}
		return true
	})
	if arr == nil {
		t.Fatal("no ArrayLit — __fern_assets() was not substituted")
	}
	out := make([][2]string, 0, len(arr.Elems))
	for i, e := range arr.Elems {
		tup, ok := e.(*ast.TupleLit)
		if !ok || len(tup.Elems) != 2 {
			t.Fatalf("element %d is %T, want a 2-element TupleLit", i, e)
		}
		name, ok1 := tup.Elems[0].(*ast.StringLit)
		data, ok2 := tup.Elems[1].(*ast.StringLit)
		if !ok1 || !ok2 {
			t.Fatalf("element %d is not a (string, string) pair", i)
		}
		out = append(out, [2]string{name.Value, data.Value})
	}
	return out
}

// Enumeration order is Names(), which is sorted — a filesystem-order walk
// would make the emitted program differ between hosts.
func TestAssetsEnumeratesSortedWithContents(t *testing.T) {
	set := assetSet(t, map[string]string{
		"sub/b.txt": "BB",
		"a.txt":     "AAA",
		"c.bin":     "C",
	})
	prog := foldAssets(t, `
function main(): i32 {
  var xs = __fern_assets();
  return 0;
}`, set)
	want := [][2]string{{"a.txt", "AAA"}, {"c.bin", "C"}, {"sub/b.txt", "BB"}}
	got := tupleElems(t, prog)
	if len(got) != len(want) {
		t.Fatalf("enumerated %d assets, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("asset %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// Binary contents survive enumeration the same way they survive a single
// __fern_asset lookup.
func TestAssetsCarriesBinaryBytes(t *testing.T) {
	blob := string([]byte{0x00, 0x80, 0xff, 0x00, 'z'})
	set := assetSet(t, map[string]string{"blob.bin": blob})
	prog := foldAssets(t, `function main(): i32 { var xs = __fern_assets(); return 0; }`, set)
	got := tupleElems(t, prog)
	if len(got) != 1 || got[0][1] != blob {
		t.Fatalf("enumerated % x, want % x", got, blob)
	}
}

// An embed directory holding no files is legitimate. The empty array it
// expands to carries a stamped ElemType, so the checker does not demand an
// annotation the user has no way to write.
func TestAssetsEmptyBundleIsTypedNotBare(t *testing.T) {
	set := assetSet(t, map[string]string{})
	prog := foldAssets(t, `function main(): i32 { var xs = __fern_assets(); return 0; }`, set)
	if got := tupleElems(t, prog); len(got) != 0 {
		t.Fatalf("enumerated %v, want nothing", got)
	}
	var arr *ast.ArrayLit
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if a, ok := n.(*ast.ArrayLit); ok && arr == nil {
			arr = a
		}
		return arr == nil
	})
	if arr.ElemType == nil {
		t.Fatal("empty enumeration must stamp ElemType or the checker raises E020")
	}
}

func TestAssetsErrors(t *testing.T) {
	set := assetSet(t, map[string]string{"a.txt": "x"})
	tests := []struct {
		name string
		src  string
		set  *embed.Set
		want string
	}{
		{
			name: "no -embed at all",
			src:  `function main(): i32 { var xs = __fern_assets(); return 0; }`,
			set:  nil,
			want: "no assets were embedded — pass -embed DIR",
		},
		{
			name: "arguments are rejected",
			src:  `function main(): i32 { var xs = __fern_assets("a.txt"); return 0; }`,
			set:  set,
			want: "takes no arguments, got 1",
		},
		{
			name: "not a constant expression",
			src:  `const A: string = __fern_assets(); function main(): i32 { return 0; }`,
			set:  set,
			want: "builds an array, which is not a constant expression",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := foldAssetsErr(t, tc.src, tc.set)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("error was %q,\nwant it to contain %q", got, tc.want)
			}
		})
	}
}
