package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

const delimitedHeaderMain = `import "core/map";

struct Point { x: i32, note: string }
struct Group { values: Point[] }
struct Box[T] { value: T }
struct Maybe { value: Option[i32] }

function one(value: Point): Point[] { return [value]; }
function identity[T](value: T): T { return value; }
function number(value: Point): i32 { return value.x; }
function positive(value: Point): boolean { return value.x > 0; }

function main(): i32 {
    let sum = 0;
    let base = [Point { x: 1, note: "base" }];
    for p in [Point { x: 2, note: "array" }, Point { x: 3, note: "heap" }] { sum = sum + p.x; }
    for p in (Group { values: base }).values { sum = sum + p.x; }
    for p in one(value = Point { x: 4, note: "named" }) { sum = sum + p.x; }
    for p in identity[Point[]]([Point { x: 5, note: "generic call" }]) { sum = sum + p.x; }
    let rows = [base, base];
    for p in rows[Point { x: 1, note: "index" }.x] { sum = sum + p.x; }
    for p in base[Point { x: 0, note: "low" }.x:Point { x: 1, note: "high" }.x] { sum = sum + p.x; }
    for p in base[:Point { x: 1, note: "slice" }.x] { sum = sum + p.x; }
    for (p, n) in [(Point { x: 6, note: "binder" }, 2)] { sum = sum + p.x + n; }
    for p in (Point { x: 0, note: "tuple" }, [Point { x: 7, note: "tuple array" }]).1 { sum = sum + p.x; }
    while (positive(Point { x: 1, note: "condition" })) { sum = sum + 1; break; }
    if let Some(p) = Some(Point { x: 9, note: "scrutinee" }) { sum = sum + p.x; }
    for n in 0..number(Point { x: 2, note: "range" }) { sum = sum + n; }
    for n in 0..(Point { x: 2, note: "range receiver" }).x { sum = sum + n; }
    for (k, p) in (Map { "hi": Point { x: 10, note: "map" } }) { sum = sum + k.len() + p.x; }
    let group = Group { values: base };
    for p in (group).values { sum = sum + p.x; }
    for b in [Box[i32] { value: 12 }] { sum = sum + b.value; }
    for n in ({ let value = 1; [value] }) { sum = sum + n; }
    if let Some(n) = (Maybe { value: Some(1) }).value { sum = sum + n; }
    let Some(n) = (Maybe { value: Some(1) }).value else { return 1; };
    sum = sum + n;
    let extra: i32 = if let Some(v) = (Maybe { value: Some(1) }).value { v } else { 0 };
    sum = sum + extra;
    if (sum != 74) { return 1; }
    return 42;
}
`

func TestSelfHostDelimitedHeaderStructX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	source := delimitedHeaderMain
	if got := interpExit(t, buildLangBinForInterp(t), source); got != 42 {
		t.Fatalf("interpreter = %d, want 42", got)
	}
	if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", source)); code != 42 {
		t.Fatalf("native exit %d, want 42", code)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", source, "FERN_LEAKCHECK=1"))
	stderr, code := runWithStdin(t, cli.runner, bin, nil)
	if code != 42 {
		t.Fatalf("native census exit %d, want 42\n%s", code, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostDelimitedHeaderStructWasm(t *testing.T) {
	cli := newStrictCLI(t)
	source := delimitedHeaderMain
	if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", source)); code != 42 {
		t.Fatalf("wasm exit %d, want 42", code)
	}
	path := filepath.Join(t.TempDir(), "census.wat")
	if err := os.WriteFile(path, []byte(cli.emit(t, "wasm32-wasi", source, "FERN_LEAKCHECK=1")), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, code := runWasmCensus(t, path)
	if code != 42 {
		t.Fatalf("wasm census exit %d, want 42\n%s", code, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostDelimitedHeaderFormatter(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(delimitedHeaderMain), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := runX86_64Bin(cli.runner, cli.bin, "-fmt", path).CombinedOutput()
	if err != nil {
		t.Fatalf("format: %v\n%s", err, first)
	}
	if err := os.WriteFile(path, first, 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := runX86_64Bin(cli.runner, cli.bin, "-fmt", path).CombinedOutput()
	if err != nil || string(second) != string(first) {
		t.Fatalf("format is not stable: %v\nfirst:\n%s\nsecond:\n%s", err, first, second)
	}
	if output, err := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("formatted source: %v\n%s", err, output)
	}
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, string(first), target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 42 {
				t.Fatalf("formatted source exit %d, want 42\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
