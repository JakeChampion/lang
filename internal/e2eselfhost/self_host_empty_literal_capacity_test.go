package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An empty array literal is allocated with room for four elements. On wasm the
// room has to fit the widest slot a push can use: sized for the 4-byte stride,
// the first four pushes into an i64[] or f64[] wrote 8-byte slots past the end
// of the block, over whatever was allocated next (#10461).
//
// Each case fills a `[]` to its pre-sized capacity with a neighbouring array
// allocated right after it, reads both back, then appends in argument position
// and grows past the capacity.
type emptyLitElem struct {
	ty     string
	vals   [5]string
	toI32  string // element `e` as an i32
	prefix string
}

var emptyLitElems = []emptyLitElem{
	{"i32", [5]string{"1", "2", "3", "4", "20"}, "e", ""},
	{"i64", [5]string{"(1 as i64)", "(2 as i64)", "(3 as i64)", "(4 as i64)", "(20 as i64)"}, "(e as i32)", ""},
	{"u64", [5]string{"(1 as u64)", "(2 as u64)", "(3 as u64)", "(4 as u64)", "(20 as u64)"}, "(e as i32)", ""},
	{"f64", [5]string{"1.5", "2.0", "3.5", "4.0", "20.0"}, "((e * 2.0) as i32)", ""},
	{"f32", [5]string{"(1.5 as f32)", "(2.0 as f32)", "(3.5 as f32)", "(4.0 as f32)", "(20.0 as f32)"}, "((e as f64 * 2.0) as i32)", ""},
	{"u8", [5]string{"(1 as u8)", "(2 as u8)", "(3 as u8)", "(4 as u8)", "(20 as u8)"}, "(e as i32)", ""},
	{"boolean", [5]string{"true", "false", "true", "true", "true"}, "b2i(e)",
		"function b2i(b: boolean): i32 { if (b) { return 1; } return 0; }\n"},
	{"string", [5]string{"\"a\"", "\"bb\"", "\"ccc\"", "\"dddd\"", "\"eeeee\""}, "e.len()", ""},
	{"P", [5]string{"P { x: 1, tag: [1] }", "P { x: 2, tag: [2] }", "P { x: 3, tag: [3] }", "P { x: 4, tag: [4] }", "P { x: 20, tag: [20] }"},
		"(e.x + e.tag[0])", "struct P { x: i32, tag: i32[] }\n"},
	{"i32[]", [5]string{"[1]", "[2, 2]", "[3]", "[4]", "[20]"}, "(e[0] + e.len())", ""},
}

func emptyLitSource(el emptyLitElem) string {
	var b strings.Builder
	b.WriteString(el.prefix)
	fmt.Fprintf(&b, "function sum(xs: %s[]): i32 {\n    let t: i32 = 0;\n    for e in xs { t = t + %s; }\n    return t;\n}\n", el.ty, el.toI32)
	fmt.Fprintf(&b, "function fresh(): %s[] { return []; }\n", el.ty)
	b.WriteString("function main(): i32 {\n")
	fmt.Fprintf(&b, "    let xs: %s[] = [];\n    let guard: i32[] = [5, 6, 7, 8];\n", el.ty)
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&b, "    xs = xs.append(%s);\n", el.vals[i])
	}
	b.WriteString("    let a: i32 = sum(xs) + guard[0] + guard[3] * 2 + guard.len();\n")
	fmt.Fprintf(&b, "    let c: i32 = sum(xs.append(%s));\n", el.vals[4])
	fmt.Fprintf(&b, "    xs = xs.append(%s);\n", el.vals[4])
	fmt.Fprintf(&b, "    let ys: %s[] = fresh();\n    let guard2: i32[] = [9, 10, 11, 12];\n", el.ty)
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&b, "    ys = ys.append(%s);\n", el.vals[i])
	}
	b.WriteString("    let d: i32 = sum(ys) + guard2[0] + guard2[3] + guard2.len();\n")
	b.WriteString("    return (a + c + d + sum(xs) + xs.len() + guard[1]) % 100;\n}\n")
	return b.String()
}

type emptyLitCase struct{ name, src string }

func emptyLitCases() []emptyLitCase {
	var cases []emptyLitCase
	for _, el := range emptyLitElems {
		cases = append(cases, emptyLitCase{"elem_" + strings.ReplaceAll(el.ty, "[]", "_arr"), emptyLitSource(el)})
	}
	// A generic `T[]` built from `[]`: the literal's element type is the type
	// parameter, so nothing at the literal says the slot is 8 bytes.
	cases = append(cases, emptyLitCase{"generic_f64_i64", `function rep[T](x: T, n: i32): T[] {
    let out: T[] = [];
    let i: i32 = 0;
    while (i < n) { out = out.append(x); i = i + 1; }
    return out;
}
function main(): i32 {
    let fs: f64[] = rep(2.5, 4);
    let guard: i32[] = [5, 6, 7, 8];
    let is: i64[] = rep(3 as i64, 4);
    let t: f64 = 0.0;
    for f in fs { t = t + f; }
    let u: i64 = 0;
    for v in is { u = u + v; }
    return (t as i32) + (u as i32) + guard[0] + guard[3] + guard.len() + fs.len() + is.len();
}
`})
	// The empty literal as a struct field.
	cases = append(cases, emptyLitCase{"struct_field_f64", `struct H { n: i32, xs: f64[] }
function main(): i32 {
    let h: H = H { n: 1, xs: [] };
    let guard: i32[] = [5, 6, 7, 8];
    let xs: f64[] = h.xs;
    xs = xs.append(1.5);
    xs = xs.append(2.5);
    xs = xs.append(3.5);
    xs = xs.append(4.5);
    let t: f64 = 0.0;
    for f in xs { t = t + f; }
    return (t as i32) + guard[0] + guard[3] + guard.len() + h.xs.len() + h.n;
}
`})
	return cases
}

func TestSelfHostEmptyLiteralCapacity(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern", "wasm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	wasm := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasmdriver")
	interp := buildLangBinForInterp(t)
	cases := emptyLitCases()
	want := map[string]int{}
	for _, tc := range cases {
		want[tc.name] = interpExit(t, interp, tc.src)
	}
	for _, target := range []string{"x86-64-linux", "x86-64-sanitize", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					var cmd *exec.Cmd
					switch target {
					case "x86-64-linux":
						asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(tc.src), "-ir")
						cmd = runX86_64Bin(runner, buildBin(t, gcc, dir, tc.name, string(asm)))
					case "x86-64-sanitize":
						asm := hevCompile(t, runner, driver, tc.src, []string{"FERN_SANITIZE=1"})
						cmd = runX86_64Bin(runner, buildBin(t, gcc, dir, tc.name+"-san", asm))
					case "arm64-linux":
						armgcc, armrunner := arm64Tooling(t)
						asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(tc.src), "-target", target, "-ir")
						cmd = runArm64Bin(armrunner, buildBinArm64(t, armgcc, dir, tc.name, string(asm)))
					case "wasm32-wasi":
						if _, err := exec.LookPath("wasmtime"); err != nil {
							t.Fatal(err)
						}
						wat := runCapture(t, gcc, runner, wasm, []byte(tc.src), "-ir")
						path := filepath.Join(dir, tc.name+".wat")
						if err := os.WriteFile(path, wat, 0o644); err != nil {
							t.Fatal(err)
						}
						cmd = exec.Command("wasmtime", "run", path)
					}
					out, _ := cmd.CombinedOutput()
					got := cmd.ProcessState.ExitCode()
					if got != want[tc.name] || strings.Contains(string(out), "fern-sanitizer:") && !strings.Contains(string(out), "leak") {
						t.Fatalf("exit %d, interpreter %d\n%s\n%s", got, want[tc.name], out, tc.src)
					}
				})
			}
		})
	}
}
