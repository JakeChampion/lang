package e2ecompiler

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The self-host front end must report exactly the Go checker's diagnostics,
// one per diagnostic, for nested arrays whose view-ness or element type does
// not match the destination.
func TestSelfHostNestedArrayViewDiagnostics(t *testing.T) {
	cli := newStrictCLI(t)
	const data = "function main(): i32 { let data: u8[] = [1u8, 2u8]; "
	cases := []struct{ name, src string }{
		{"view-first", data + "let all = [data[:1], data]; return all.len(); }"},
		{"owned-first", data + "let all = [data, data[:1]]; return all.len(); }"},
		{"owned-destination", data + "let all: u8[][] = [data[:1], data]; return all.len(); }"},
		{"view-destination", data + "let all: [u8][] = [data[:1], data]; return all.len(); }"},
		{"single-view", data + "let all: u8[][] = [data[:1]]; return all.len(); }"},
		{"single-owned", data + "let all: [u8][] = [data]; return all.len(); }"},
		{"owned-local-into-views", data + "let owned: u8[][] = [data]; let all: [u8][] = owned; return all.len(); }"},
		{"owned-in-tuple-view", data + "let t: (u8[], i32) = (data, 1); let u: ([u8], i32) = t; return u.1; }"},
		{"owned-in-option-view", data + "let p: Option[u8[][]] = Some([data]); let q: Option[[u8][]] = p; return 0; }"},
		{"literal-into-nested-view", "function main(): i32 { let all: [u8][] = [[1u8]]; return all.len(); }"},
		{"literal-into-tuple-view", "function main(): i32 { let t: ([u8], i32) = ([1u8], 1); return t.1; }"},
		{"literal-into-option-view", "function main(): i32 { let o: Option[[u8]] = Some([1u8]); return 0; }"},
		{"literal-into-variant-view", "enum E { A([u8]), B }\nfunction main(): i32 { let e: E = A([1u8]); return 0; }"},
		{"literal-argument-into-nested-view", "function h(xs: [u8][]): i32 { return xs.len(); }\nfunction main(): i32 { return h([[1u8]]); }"},
		{"widened-element-let", "function main(): i32 { let narrow: i32[] = [1]; let all: i64[][] = [narrow]; return all.len(); }"},
		{"widened-element-return", "function f(values: i32[]): i64[][] { return [values]; }\nfunction main(): i32 { return 0; }"},
		{"widened-element-argument", "function g(xs: i64[][]): i32 { return xs.len(); }\nfunction f(values: i32[]): i32 { return g([values]); }\nfunction main(): i32 { return 0; }"},
		{"struct-element", "struct S { x: i32 }\nstruct T { x: i64 }\nfunction f(v: S): T[] { return [v]; }\nfunction main(): i32 { return 0; }"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			want := goCheckerDiagCodes(t, dir, tc.src)
			if len(want) == 0 {
				t.Fatal("Go checker accepted the invalid aggregate")
			}
			path := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib)
			out, err := cmd.CombinedOutput()
			if got := sortedCodes(string(out)); err == nil || !slices.Equal(got, want) {
				t.Fatalf("check = %v, codes %v, want %v\n%s", err, got, want, out)
			}
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				_, diags, err := cli.tryEmit(t, target, tc.src)
				if got := sortedCodes(diags); err == nil || !slices.Equal(got, want) || strings.Contains(diags, "FERN_SEM_IR") {
					t.Fatalf("%s = %v, codes %v, want checker %v\n%s", target, err, got, want, diags)
				}
			}
		})
	}
}

const nestedArrayViewsSource = `function size(data: [u8]): i32 { return data.len(); }
function main(): i32 {
    let data: u8[] = [1u8, 2u8];
    let owned = [data, data];
    let views = [data[:1], data[1:]];
    let explicit: [u8][] = [data[:1], data[1:]];
    let numbers = [[1], [2i64]];
    let reverse = [[2i64], [1]];
    let wide: i64[][] = [[1], [5000000000i64]];
    if (owned[1][1] != 2u8 || views[0][0] != 1u8 || explicit[1][0] != 2u8) { return 1; }
    if (numbers[0][0] != 1 || reverse[1][0] != 1 || wide[1][0] != 5000000000i64) { return 2; }
    if (size(data) != 2) { return 3; }
    return 42;
}
`

func TestSelfHostNestedArrayViewsX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	if got := interpExit(t, buildLangBinForInterp(t), nestedArrayViewsSource); got != 42 {
		t.Fatalf("interpreter = %d, want 42", got)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", nestedArrayViewsSource, "FERN_SANITIZE=1"))
	stderr, code := runWithStdin(t, cli.runner, bin, nil)
	if code != 42 {
		t.Fatalf("native = %d, want 42\n%s", code, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostNestedArrayViewsWasm(t *testing.T) {
	cli := newStrictCLI(t)
	path := filepath.Join(t.TempDir(), "census.wat")
	if err := os.WriteFile(path, []byte(cli.emit(t, "wasm32-wasi", nestedArrayViewsSource, "FERN_LEAKCHECK=1")), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, code := runWasmCensus(t, path)
	if code != 42 {
		t.Fatalf("wasm = %d, want 42\n%s", code, stderr)
	}
	assertBalancedCensus(t, stderr)
}
