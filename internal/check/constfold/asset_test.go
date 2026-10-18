package constfold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/embed"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
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
  let page: string = __fern_asset("html/index.html");
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
  let p: string = PAGE;
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
  let p: string = BOTH;
  return 0;
}`, set)
	if got := firstStringLit(t, prog); got != "AAABBB" {
		t.Fatalf("concatenated to %q, want %q", got, "AAABBB")
	}
}

// firstArrayLit returns the first ArrayLit anywhere in the program.
func firstArrayLit(t *testing.T, prog *ast.Program) *ast.ArrayLit {
	t.Helper()
	var arr *ast.ArrayLit
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if a, ok := n.(*ast.ArrayLit); ok && arr == nil {
			arr = a
		}
		return arr == nil
	})
	if arr == nil {
		t.Fatal("no ArrayLit in the folded program — the asset call was not substituted")
	}
	return arr
}

// u8Bytes reads a u8[] literal back as bytes, failing on anything else.
func u8Bytes(t *testing.T, e ast.Expr) string {
	t.Helper()
	arr, ok := e.(*ast.ArrayLit)
	if !ok {
		t.Fatalf("%T is not an array literal", e)
	}
	if !ast.Equal(arr.ElemType, ast.NumberType{Width: 8, Signed: false}) {
		t.Fatalf("array element type is %v, want u8", arr.ElemType)
	}
	out := make([]byte, len(arr.Elems))
	for i, el := range arr.Elems {
		n, ok := el.(*ast.NumberLit)
		if !ok || n.Width != 8 || !n.IsUnsigned {
			t.Fatalf("element %d is %#v, want a u8 literal", i, el)
		}
		out[i] = byte(n.Value)
	}
	return string(out)
}

// NUL bytes and bytes >= 0x80 survive the bytes form intact, as u8[].
func TestAssetBytesCarriesBinaryBytes(t *testing.T) {
	blob := string([]byte{0x00, 0x80, 0xff, 0x00, 'e', 'n', 'd'})
	set := assetSet(t, map[string]string{"blob.bin": blob})
	prog := foldAssets(t, `
function main(): i32 {
  let b: u8[] = __fern_asset_bytes("blob.bin");
  return 0;
}`, set)
	if got := u8Bytes(t, firstArrayLit(t, prog)); got != blob {
		t.Fatalf("binary asset became % x, want % x", got, blob)
	}
}

// The bytes form may initialise a const, and an empty file is a typed empty
// array rather than one demanding an annotation.
func TestAssetBytesInConst(t *testing.T) {
	set := assetSet(t, map[string]string{"icon.png": "\x89PNG", "empty": ""})
	prog := foldAssets(t, `
const ICON: u8[] = __fern_asset_bytes("icon.png");
function main(): i32 {
  let i: u8[] = ICON;
  let e: u8[] = __fern_asset_bytes("empty");
  return 0;
}`, set)
	var arrs []*ast.ArrayLit
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if a, ok := n.(*ast.ArrayLit); ok {
			arrs = append(arrs, a)
		}
		return true
	})
	if len(arrs) != 2 {
		t.Fatalf("found %d array literals, want 2", len(arrs))
	}
	if got := u8Bytes(t, arrs[0]); got != "\x89PNG" {
		t.Fatalf("const ICON became % x", got)
	}
	if got := u8Bytes(t, arrs[1]); got != "" {
		t.Fatalf("an empty file became % x", got)
	}
}

// A string is always valid UTF-8, so the text form refuses a file that is
// not, naming it and pointing at the bytes form — in a body and in a const.
func TestAssetTextRefusesInvalidUTF8(t *testing.T) {
	set := assetSet(t, map[string]string{"img/favicon.png": "\x89PNG\r\n\x1a\n\xff", "ok.txt": "héllo"})
	for _, src := range []string{
		`function main(): i32 { let s: string = __fern_asset("img/favicon.png"); return 0; }`,
		`const S: string = __fern_asset("img/favicon.png"); function main(): i32 { return 0; }`,
	} {
		got := foldAssetsErr(t, src, set)
		for _, want := range []string{`"img/favicon.png"`, "is not valid UTF-8", "__fern_asset_bytes"} {
			if !strings.Contains(got, want) {
				t.Errorf("error %q should contain %q", got, want)
			}
		}
	}
	prog := foldAssets(t, `function main(): i32 { let s: string = __fern_asset("ok.txt"); return 0; }`, set)
	if got := firstStringLit(t, prog); got != "héllo" {
		t.Fatalf("UTF-8 text became %q", got)
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
			src:  `function main(): i32 { let s: string = __fern_asset("html/index.htm"); return 0; }`,
			set:  set,
			want: `did you mean "html/index.html"`,
		},
		{
			name: "unknown asset with no near miss lists what is available",
			src:  `function main(): i32 { let s: string = __fern_asset("zzzzzzzzzz"); return 0; }`,
			set:  set,
			want: "embedded assets: html/index.html, style.css",
		},
		{
			name: "no -embed at all",
			src:  `function main(): i32 { let s: string = __fern_asset("a.txt"); return 0; }`,
			set:  nil,
			want: "no assets were embedded — pass -embed DIR",
		},
		{
			name: "computed name is rejected",
			src:  `function main(): i32 { let n: string = "a"; let s: string = __fern_asset(n); return 0; }`,
			set:  set,
			want: "needs a string literal",
		},
		{
			name: "wrong arity is rejected",
			src:  `function main(): i32 { let s: string = __fern_asset("a", "b"); return 0; }`,
			set:  set,
			want: "takes exactly one argument, got 2",
		},
		{
			name: "unknown asset in a const initialiser",
			src:  `const P: string = __fern_asset("nope-not-here"); function main(): i32 { let s: string = P; return 0; }`,
			set:  set,
			want: "no embedded asset",
		},
		{
			name: "bytes form wrong arity names the bytes form",
			src:  `function main(): i32 { let b: u8[] = __fern_asset_bytes(); return 0; }`,
			set:  set,
			want: "__fern_asset_bytes takes exactly one argument, got 0",
		},
		{
			name: "bytes form unknown asset",
			src:  `function main(): i32 { let b: u8[] = __fern_asset_bytes("style.cs"); return 0; }`,
			set:  set,
			want: `did you mean "style.css"`,
		},
		{
			name: "bytes form without -embed",
			src:  `function main(): i32 { let b: u8[] = __fern_asset_bytes("a"); return 0; }`,
			set:  nil,
			want: `__fern_asset_bytes("a") but no assets were embedded`,
		},
		{
			name: "bytes form under an operator in a const",
			src:  `const B: u8[] = __fern_asset_bytes("style.css") + __fern_asset_bytes("style.css"); function main(): i32 { return 0; }`,
			set:  set,
			want: "__fern_asset_bytes() builds an array",
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
  let xs = __fern_assets();
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

// The enumeration may be held in a const, as it may in a var (#7987).
func TestAssetsInConst(t *testing.T) {
	set := assetSet(t, map[string]string{"b.txt": "BB", "a.txt": "AAA"})
	prog := foldAssets(t, `
const XS: (string, string)[] = __fern_assets();
function main(): i32 {
  let xs = XS;
  return 0;
}`, set)
	want := [][2]string{{"a.txt", "AAA"}, {"b.txt", "BB"}}
	got := tupleElems(t, prog)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("const XS substituted %v, want %v", got, want)
	}
}

// The bytes enumeration carries binary contents as u8[], sorted like the
// text one, and may initialise a const.
func TestAssetsBytesCarriesBinaryBytes(t *testing.T) {
	blob := string([]byte{0x00, 0x80, 0xff, 0x00, 'z'})
	set := assetSet(t, map[string]string{"blob.bin": blob, "a.txt": "AAA"})
	for _, src := range []string{
		`function main(): i32 { let xs = __fern_assets_bytes(); return 0; }`,
		`const XS: (string, u8[])[] = __fern_assets_bytes(); function main(): i32 { let xs = XS; return 0; }`,
	} {
		arr := firstArrayLit(t, foldAssets(t, src, set))
		want := ast.TupleType{Elems: []ast.Type{ast.StringType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}}}
		if !ast.Equal(arr.ElemType, want) {
			t.Fatalf("element type %v, want %v", arr.ElemType, want)
		}
		if len(arr.Elems) != 2 {
			t.Fatalf("enumerated %d assets, want 2", len(arr.Elems))
		}
		for i, w := range [][2]string{{"a.txt", "AAA"}, {"blob.bin", blob}} {
			tup := arr.Elems[i].(*ast.TupleLit)
			if name := tup.Elems[0].(*ast.StringLit).Value; name != w[0] {
				t.Errorf("asset %d is %q, want %q", i, name, w[0])
			}
			if got := u8Bytes(t, tup.Elems[1]); got != w[1] {
				t.Errorf("asset %d contents % x, want % x", i, got, w[1])
			}
		}
	}
}

// The text enumeration refuses a bundle holding a file that is not UTF-8,
// naming the file.
func TestAssetsTextRefusesInvalidUTF8(t *testing.T) {
	set := assetSet(t, map[string]string{"a.txt": "AAA", "font.woff2": "wOF2\xc0\x00"})
	got := foldAssetsErr(t, `function main(): i32 { let xs = __fern_assets(); return 0; }`, set)
	for _, want := range []string{`"font.woff2"`, "is not valid UTF-8", "__fern_assets_bytes"} {
		if !strings.Contains(got, want) {
			t.Errorf("error %q should contain %q", got, want)
		}
	}
}

// An embed directory holding no files is legitimate. The empty array it
// expands to carries a stamped ElemType, so the checker does not demand an
// annotation the user has no way to write.
func TestAssetsEmptyBundleIsTypedNotBare(t *testing.T) {
	set := assetSet(t, map[string]string{})
	prog := foldAssets(t, `function main(): i32 { let xs = __fern_assets(); return 0; }`, set)
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
			src:  `function main(): i32 { let xs = __fern_assets(); return 0; }`,
			set:  nil,
			want: "no assets were embedded — pass -embed DIR",
		},
		{
			name: "arguments are rejected",
			src:  `function main(): i32 { let xs = __fern_assets("a.txt"); return 0; }`,
			set:  set,
			want: "takes no arguments, got 1",
		},
		{
			name: "a const of the wrong type",
			src:  `const A: string = __fern_assets(); function main(): i32 { return 0; }`,
			set:  set,
			want: "declared type string does not match initialiser type (string, string)[]",
		},
		{
			name: "bytes enumeration arguments are rejected",
			src:  `function main(): i32 { let xs = __fern_assets_bytes(1); return 0; }`,
			set:  set,
			want: "__fern_assets_bytes takes no arguments, got 1",
		},
		{
			name: "bytes enumeration without -embed",
			src:  `function main(): i32 { let xs = __fern_assets_bytes(); return 0; }`,
			set:  nil,
			want: "__fern_assets_bytes() but no assets were embedded",
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
