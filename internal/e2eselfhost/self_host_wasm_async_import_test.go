package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// asyncImportProvider exports test:dep/d and test:dep/e, the interfaces
// TestSelfHostWasmAsyncImport's consumer imports. compute and note yield
// before task.return, so the consumer's call of them is still pending when
// its lower returns and the wrapper has to wait for it.
const asyncImportProvider = `(component
  (core func $tr_s32 (canon task.return (result s32)))
  (core func $tr_u64 (canon task.return (result u64)))
  (core func $tr_f64 (canon task.return (result f64)))
  (core func $tr_bool (canon task.return (result bool)))
  (core func $tr_none (canon task.return))
  (core func $yield (canon thread.yield))
  (core module $m
    (import "" "tr-s32" (func $tr_s32 (param i32)))
    (import "" "tr-u64" (func $tr_u64 (param i64)))
    (import "" "tr-f64" (func $tr_f64 (param f64)))
    (import "" "tr-bool" (func $tr_bool (param i32)))
    (import "" "tr-none" (func $tr_none))
    (import "" "yield" (func $yield (result i32)))
    (func (export "compute") (param i32)
      (drop (call $yield))
      (call $tr_s32 (i32.add (local.get 0) (i32.const 1))))
    (func (export "big") (call $tr_u64 (i64.const 4294967338)))
    (func (export "half") (param f64 f32)
      (call $tr_f64 (f64.add (f64.div (local.get 0) (f64.const 2)) (f64.promote_f32 (local.get 1)))))
    (func (export "flag") (param i32) (call $tr_bool (i32.eqz (local.get 0))))
    (func (export "note") (param i32) (drop (call $yield)) (call $tr_none)))
  (core instance $lib
    (export "tr-s32" (func $tr_s32)) (export "tr-u64" (func $tr_u64)) (export "tr-f64" (func $tr_f64))
    (export "tr-bool" (func $tr_bool)) (export "tr-none" (func $tr_none)) (export "yield" (func $yield)))
  (core instance $i (instantiate $m (with "" (instance $lib))))
  (type $compute_t (func async (param "n" s32) (result s32)))
  (type $big_t (func async (result u64)))
  (type $half_t (func async (param "x" f64) (param "k" f32) (result f64)))
  (type $flag_t (func async (param "b" bool) (result bool)))
  (type $note_t (func async (param "n" u8)))
  (func $compute (type $compute_t) (canon lift (core func $i "compute") async))
  (func $big (type $big_t) (canon lift (core func $i "big") async))
  (func $half (type $half_t) (canon lift (core func $i "half") async))
  (func $flag (type $flag_t) (canon lift (core func $i "flag") async))
  (func $note (type $note_t) (canon lift (core func $i "note") async))
  (instance $d (export "compute" (func $compute)) (export "big" (func $big)))
  (instance $e (export "half" (func $half)) (export "flag" (func $flag)) (export "note" (func $note)))
  (export "test:dep/d" (instance $d))
  (export "test:dep/e" (instance $e)))
`

// TestSelfHostWasmAsyncImport pins the self-host's async imports: an
// `@import(iface, name) async function` of an interface the fern world does
// not declare becomes an import of that interface, lowered with the async
// option, and its call waits for the result. The consumer is linked with
// asyncImportProvider by `wasm-tools compose`, and each async export that
// calls an import runs under wasmtime's async features.
func TestSelfHostWasmAsyncImport(t *testing.T) {
	requireWasmTools(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	consumer, printed := buildAsyncComponent(t, cli, dir, `@import("test:dep/d", "compute") async function compute(n: i32): i32;
@import("test:dep/d", "big") async function big(): u64;
@import("test:dep/e", "half") async function half(x: f64, k: f32): f64;
@import("test:dep/e", "flag") async function flag(b: boolean): boolean;
@import("test:dep/e", "note") async function note(n: u8): void;
async function go(): i32 { return compute(39) + 2; }
async function wide(): u64 { return big(); }
async function halve(): f64 { let k: f32 = 0.5; return half(83.0, k); }
async function negate(b: boolean): boolean { return flag(b); }
async function noted(): i32 { note(7); return 42; }
function main(): i32 { return 0; }
`)
	checkDeclares(t, printed, []string{`(import "test:dep/d"`, `(import "test:dep/e"`, `(canon waitable-set.wait`}, nil)
	providerWat, provider := filepath.Join(dir, "provider.wat"), filepath.Join(dir, "provider.wasm")
	if err := os.WriteFile(providerWat, []byte(asyncImportProvider), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("wasm-tools", "parse", providerWat, "-o", provider).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse provider: %v\n%s", err, out)
	}
	linked := filepath.Join(dir, "linked.wasm")
	if out, err := exec.Command("wasm-tools", "compose", consumer, "-d", provider, "-o", linked).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools compose: %v\n%s", err, out)
	}
	for _, c := range []struct{ invoke, want string }{
		{"go()", "42"},
		{"wide()", "4294967338"},
		{"halve()", "42"},
		{"negate(true)", "false"},
		{"negate(false)", "true"},
		{"noted()", "42"},
	} {
		args := append(append([]string{"run"}, asyncFeatures...), "--invoke", c.invoke, linked)
		out, err := exec.Command("wasmtime", args...).CombinedOutput()
		if err != nil {
			t.Errorf("--invoke %s: %v\n%s", c.invoke, err, out)
			continue
		}
		if got := strings.TrimSpace(string(out)); got != c.want {
			t.Errorf("--invoke %s = %q, want %q", c.invoke, got, c.want)
		}
	}
}

// TestSelfHostWasmAsyncImportRefusals pins what an async import cannot lower
// yet, and that one naming an interface the fern world declares is refused:
// the component would import that interface twice.
func TestSelfHostWasmAsyncImportRefusals(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	for _, c := range []struct{ name, src, want string }{
		{"string-param", `@import("test:dep/d", "f") async function f(s: string): i32;
function main(): i32 { return f("x"); }`, "async import f: parameter s has type string, which an async import cannot take yet"},
		{"five-params", `@import("test:dep/d", "f") async function f(a: i32, b: i32, c: i32, d: i32, e: i32): i32;
function main(): i32 { return f(1, 2, 3, 4, 5); }`, "async import f: more than four parameters"},
		{"world-interface", `@import("wasi:cli/stdout@0.2.0", "get-stdout") async function f(): i32;
function main(): i32 { return f(); }`, "async import from wasi:cli/stdout@0.2.0, an interface the world declares"},
	} {
		srcPath := filepath.Join(dir, c.name+".fern")
		if err := os.WriteFile(srcPath, []byte(c.src+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", filepath.Join(dir, c.name+".wasm"), srcPath, cli.stdlib)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), c.want) {
			t.Errorf("%s: err = %v, want a refusal containing %q\n%s", c.name, err, c.want, out)
		}
	}
}
