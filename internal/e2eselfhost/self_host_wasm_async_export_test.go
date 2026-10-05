package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// asyncFeatures are the wasmtime flags a component with a stackful async lift
// needs to load at all.
var asyncFeatures = []string{"-W", "component-model-async,component-model-async-stackful"}

// TestSelfHostWasmAsyncExport pins the self-host's async-lifted exports: each
// `async function` with a body becomes a top-level `async func` of the
// wasi:cli/run component, its result delivered through task.return, beside
// the run entry, which still runs main.
func TestSelfHostWasmAsyncExport(t *testing.T) {
	requireWasmTools(t)
	cli := buildSelfHostCLI(t)
	comp, printed := buildAsyncComponent(t, cli, t.TempDir(), `async function add(a: i32, b: i32): i32 { return a + b; }
async function big(): u64 { return 4294967338; }
async function half(x: f64): f64 { return x / 2.0; }
async function scale_by(x: f32, k: i32): f32 { return x * (k as f32); }
async function is_pos(n: i64): boolean { return n > 0; }
async function bump(b: u8): u32 { return (b as u32) + 1; }
async function tick(n: i32): void { print("tick"); }
function main(): i32 { print("main ran"); return 0; }
`)
	checkDeclares(t, printed, []string{`"add" (func`, `"scale-by" (func`, `"[task-return]add"`}, nil)
	for _, c := range []struct{ invoke, want string }{
		{"add(20, 22)", "42"},
		{"add(-50, 8)", "-42"},
		{"big()", "4294967338"},
		{"half(85.0)", "42.5"},
		{"scale-by(10.5, 4)", "42"},
		{"is-pos(-4)", "false"},
		{"is-pos(9)", "true"},
		{"bump(41)", "42"},
		{"tick(7)", "tick\n()"},
	} {
		args := append(append([]string{"run"}, asyncFeatures...), "--invoke", c.invoke, comp)
		out, err := exec.Command("wasmtime", args...).CombinedOutput()
		if err != nil {
			t.Errorf("--invoke %s: %v\n%s", c.invoke, err, out)
			continue
		}
		if got := strings.TrimSpace(string(out)); got != c.want {
			t.Errorf("--invoke %s = %q, want %q", c.invoke, got, c.want)
		}
	}
	args := append(append([]string{"run"}, asyncFeatures...), comp)
	if out, err := exec.Command("wasmtime", args...).CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "main ran" {
		t.Errorf("wasmtime run = %q (err = %v), want main's %q", out, err, "main ran")
	}
}

// buildAsyncComponent compiles src to a wasi:cli/run component, validates it
// with the async features on and returns its path and printed text.
func buildAsyncComponent(t *testing.T, cli *selfHostCLI, dir, src string) (string, string) {
	t.Helper()
	srcPath, comp := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", comp, srcPath, cli.stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host component: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", "--features", "wasm2,component-model,cm-async,cm-async-stackful", comp).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	printed, err := exec.Command("wasm-tools", "print", comp).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print: %v\n%s", err, printed)
	}
	return comp, string(printed)
}

// TestSelfHostWasmAsyncExportRefusals pins what an async export cannot lift
// yet, and that wasm32-wasi-http, whose proxy world exports only the handler,
// refuses one rather than dropping it.
func TestSelfHostWasmAsyncExportRefusals(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	const handler = `import "std/http";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse { return http.ok("hi"); }
`
	for _, c := range []struct{ name, target, src, want string }{
		{"string-param", "wasm32-wasi", "async function f(s: string): i32 { return 1; }\n", "parameter s has type string, which an async export cannot take yet"},
		{"string-result", "wasm32-wasi", "async function f(): string { return \"x\"; }\n", "the result type string is one an async export cannot take yet"},
		{"camel-case", "wasm32-wasi", "async function fooBar(): i32 { return 1; }\n", "async function fooBar: the name has no kebab-case form"},
		{"http", "wasm32-wasi-http", handler + "async function f(): i32 { return 1; }\n", "async function f: only -target wasm32-wasi lifts an async export or lowers an async import"},
	} {
		srcPath := filepath.Join(dir, c.name+".fern")
		src := c.src
		if c.target == "wasm32-wasi" {
			src += "function main(): i32 { return 0; }\n"
		}
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", c.target, "-o", filepath.Join(dir, c.name+".wasm"), srcPath, cli.stdlib)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), c.want) {
			t.Errorf("%s: err = %v, want a refusal containing %q\n%s", c.name, err, c.want, out)
		}
	}
}
