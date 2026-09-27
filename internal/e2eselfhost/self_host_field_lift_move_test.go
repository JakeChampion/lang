package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A nested-struct field lifted into a local (`var fr = st.fr`) takes no retain
// at the bind, so the local holds no claim of its own. The move analysis took
// it for an owned local and let a struct literal move it at its last use, while
// `st`'s exit drop released the same field: the new box was one count short and
// the frame read freed memory on the next call. The whole compiler was the
// first program to hit it, in irlower's strarr_own_node fold.
const fieldLiftHeader = `struct F { xs: string[] }
struct A { fr: F, n: i32 }
`

const fieldLiftMain = `function main(): i32 {
    var xs: string[] = ["a" + "b", "c" + "d"];
    var acc: A = A { fr: F { xs: xs }, n: 0 };
    var i: i32 = 0;
    while (i < 5) { acc = node(acc); i = i + 1; }
    var junk: string[] = ["zzzzzzzzzzzzzzzz" + "y", "q" + "r"];
    return acc.n * 10 + acc.fr.xs.len() + acc.fr.xs[1].len() + junk.len() * 0;
}
`

var fieldLiftCases = []struct{ name, src string }{
	{"straight", fieldLiftHeader + `function node(own st: A): A {
    var fr: F = st.fr;
    return A { fr: fr, n: st.n + 1 };
}
` + fieldLiftMain},
	{"branch-between", fieldLiftHeader + `function node(own st: A): A {
    var fr: F = st.fr;
    var n: i32 = st.n;
    if (n > 100) { n = 0; }
    return A { fr: fr, n: n + 1 };
}
` + fieldLiftMain},
	{"alias-of-lift", fieldLiftHeader + `function node(own st: A): A {
    var fr: F = st.fr;
    var g: F = fr;
    return A { fr: g, n: st.n + 1 };
}
` + fieldLiftMain},
	// The compiler's own shape: the accumulator threaded through a generic fold
	// whose visitor consumes it, lifting its frame and rebuilding it.
	{"fold-accumulator", `struct Src { owned: string[] }
struct Frame { key: string, params: string[], src: Src }
struct Acc { fr: Frame, out: string[] }
function fold[T](xs: i32[], own acc: T, step: (i32, own T) => T): T {
    for x in xs { acc = step(x, acc); }
    return acc;
}
function node(x: i32, own st: Acc): Acc {
    var fr: Frame = st.fr;
    var a: string[] = st.out;
    if (x < fr.params.len()) { a = a.append(fr.key + "#" + fr.params[x]); }
    return Acc { fr: fr, out: a };
}
function own_stmts(xs: i32[], fr: Frame, out: string[]): string[] {
    var acc: Acc = Acc { fr: fr, out: out };
    acc = fold(xs, acc, node);
    return acc.out;
}
function main(): i32 {
    var ps: string[] = [];
    var i: i32 = 0;
    while (i < 6) { ps = ps.append("p" + "q"); i = i + 1; }
    var fr: Frame = Frame { key: "f" + "n", params: ps, src: Src { owned: ["a" + "b"] } };
    var out: string[] = [];
    var round: i32 = 0;
    while (round < 20) {
        out = own_stmts([0, 1, 2, 3, 4, 5], fr, out);
        var junk: string = "zzzzzzzz" + "yyyyyyyy";
        round = round + 1;
    }
    return (out.len() + fr.params.len() + fr.key.len()) % 100;
}
`},
}

func TestSelfHostFieldLiftMove(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern", "wasm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	wasm := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasmdriver")
	interp := buildLangBinForInterp(t)
	want := map[string]int{}
	for _, tc := range fieldLiftCases {
		want[tc.name] = interpExit(t, interp, tc.src)
	}
	for _, target := range []string{"x86-64-linux", "x86-64-sanitize", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range fieldLiftCases {
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
