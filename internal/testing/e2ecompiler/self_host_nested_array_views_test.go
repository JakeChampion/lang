package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostNestedArrayViewDiagnostics(t *testing.T) {
	cli := newStrictCLI(t)
	cases := []struct{ name, decl string }{
		{"view-first", "let all = [data[:1], data];"},
		{"owned-first", "let all = [data, data[:1]];"},
		{"owned-destination", "let all: u8[][] = [data[:1], data];"},
		{"view-destination", "let all: [u8][] = [data[:1], data];"},
		{"single-view", "let all: u8[][] = [data[:1]];"},
		{"single-owned", "let all: [u8][] = [data];"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "function main(): i32 { let data: u8[] = [1u8, 2u8]; " + tc.decl + " return all.len(); }"
			dir := t.TempDir()
			want := strings.Join(goCheckerCodes(t, dir, src), " ")
			if want == "" {
				t.Fatal("Go checker accepted the invalid aggregate")
			}
			path := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib)
			out, err := cmd.CombinedOutput()
			if got := strings.Join(uniqueSortedCodes(codeRE.FindAllString(string(out), -1)), " "); err == nil || got != want {
				t.Fatalf("check = %v, codes %q, want the Go checker's %q\n%s", err, got, want, out)
			}
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				_, diags, err := cli.tryEmit(t, target, src)
				got := strings.Join(uniqueSortedCodes(codeRE.FindAllString(diags, -1)), " ")
				if err == nil || got != want || strings.Contains(diags, "FERN_SEM_IR") {
					t.Fatalf("%s = %v, codes %q, want the Go checker's %q\n%s", target, err, got, want, diags)
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
