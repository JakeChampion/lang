package e2ecompiler

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
	linked := linkAsyncProvider(t, dir, consumer, asyncImportProvider)
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

// asyncMemProvider exports test:dep/m, whose functions pass strings, lists
// and a tuple in memory. Its memory and realloc live in a core module of
// their own, so the lifts and the string / list task.returns can name them
// before the module that uses them is instantiated.
const asyncMemProvider = `(component
  (core module $mm
    (memory (export "mem") 1)
    (global $bump (mut i32) (i32.const 4096))
    (func (export "realloc") (param i32 i32 i32 i32) (result i32) (local $p i32)
      (local.set $p (i32.and (i32.add (global.get $bump) (i32.sub (local.get 2) (i32.const 1))) (i32.sub (i32.const 0) (local.get 2))))
      (global.set $bump (i32.add (local.get $p) (local.get 3)))
      (local.get $p)))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "mem" (core memory $mem))
  (alias core export $mi "realloc" (core func $realloc))
  (core func $tr_s32 (canon task.return (result s32)))
  (core func $tr_str (canon task.return (result string) (memory $mem)))
  (core func $tr_list (canon task.return (result (list u8)) (memory $mem)))
  (core func $yield (canon thread.yield))
  (core module $m
    (import "env" "mem" (memory 1))
    (import "" "tr-s32" (func $tr_s32 (param i32)))
    (import "" "tr-str" (func $tr_str (param i32 i32)))
    (import "" "tr-list" (func $tr_list (param i32 i32)))
    (import "" "yield" (func $yield (result i32)))
    (data (i32.const 16) "abc")
    (func (export "send") (param i32 i32) (call $tr_s32 (local.get 1)))
    (func (export "sum") (param $p i32) (param $n i32) (local $i i32) (local $t i32)
      (block $d (loop $l
        (br_if $d (i32.ge_u (local.get $i) (local.get $n)))
        (local.set $t (i32.add (local.get $t) (i32.load8_u (i32.add (local.get $p) (local.get $i)))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $l)))
      (call $tr_s32 (local.get $t)))
    (func (export "isum") (param $p i32) (param $n i32) (local $i i32) (local $t i32)
      (block $d (loop $l
        (br_if $d (i32.ge_u (local.get $i) (local.get $n)))
        (local.set $t (i32.add (local.get $t) (i32.load (i32.add (local.get $p) (i32.mul (local.get $i) (i32.const 4))))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $l)))
      (call $tr_s32 (local.get $t)))
    (func (export "add") (param i32 i32) (call $tr_s32 (i32.add (local.get 0) (local.get 1))))
    (func (export "mixed") (param i32 i32 i32) (call $tr_s32 (i32.add (local.get 1) (local.get 2))))
    (func (export "echo") (param i32 i32) (drop (call $yield)) (call $tr_str (local.get 0) (local.get 1)))
    (func (export "bytes") (call $tr_list (i32.const 16) (i32.const 3))))
  (core instance $env (export "mem" (memory $mem)))
  (core instance $lib (export "tr-s32" (func $tr_s32)) (export "tr-str" (func $tr_str))
    (export "tr-list" (func $tr_list)) (export "yield" (func $yield)))
  (core instance $i (instantiate $m (with "env" (instance $env)) (with "" (instance $lib))))
  (type $send_t (func async (param "s" string) (result s32)))
  (type $sum_t (func async (param "xs" (list u8)) (result s32)))
  (type $isum_t (func async (param "xs" (list s32)) (result s32)))
  (type $add_t (func async (param "p" (tuple s32 s32)) (result s32)))
  (type $mixed_t (func async (param "url" string) (param "n" s32) (result s32)))
  (type $echo_t (func async (param "s" string) (result string)))
  (type $bytes_t (func async (result (list u8))))
  (func $send (type $send_t) (canon lift (core func $i "send") async (memory $mem) (realloc $realloc)))
  (func $sum (type $sum_t) (canon lift (core func $i "sum") async (memory $mem) (realloc $realloc)))
  (func $isum (type $isum_t) (canon lift (core func $i "isum") async (memory $mem) (realloc $realloc)))
  (func $add (type $add_t) (canon lift (core func $i "add") async))
  (func $mixed (type $mixed_t) (canon lift (core func $i "mixed") async (memory $mem) (realloc $realloc)))
  (func $echo (type $echo_t) (canon lift (core func $i "echo") async (memory $mem) (realloc $realloc)))
  (func $bytes (type $bytes_t) (canon lift (core func $i "bytes") async (memory $mem)))
  (instance $x (export "send" (func $send)) (export "sum" (func $sum)) (export "isum" (func $isum))
    (export "add" (func $add)) (export "mixed" (func $mixed)) (export "echo" (func $echo)) (export "bytes" (func $bytes)))
  (export "test:dep/m" (instance $x)))
`

// TestSelfHostWasmAsyncImportMemory pins the async imports whose values cross
// in memory: string, list and tuple parameters, and string and list results,
// which the host writes into the consumer's memory through its cabi_realloc.
// echo yields, so its string result arrives after the lower has returned.
func TestSelfHostWasmAsyncImportMemory(t *testing.T) {
	requireWasmTools(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	consumer, _ := buildAsyncComponent(t, cli, dir, `@import("test:dep/m", "send") async function send(s: string): i32;
@import("test:dep/m", "sum") async function sum(xs: u8[]): i32;
@import("test:dep/m", "isum") async function isum(xs: i32[]): i32;
@import("test:dep/m", "add") async function add(p: (i32, i32)): i32;
@import("test:dep/m", "mixed") async function mixed(url: string, n: i32): i32;
@import("test:dep/m", "echo") async function echo(s: string): string;
@import("test:dep/m", "bytes") async function bytes(): u8[];
async function t_send(): i32 { return send("hello"); }
async function t_sum(): i32 { let xs: u8[] = [1u8, 2u8, 39u8]; return sum(xs); }
async function t_isum(): i32 { let xs: i32[] = [40, -8, 10]; return isum(xs); }
async function t_add(): i32 { return add((20, 22)); }
async function t_mixed(): i32 { return mixed("hi", 40); }
async function t_echo(): i32 { let r: string = echo("hello, world"); if (r == "hello, world") { return 42; } return r.len(); }
async function t_bytes(): i32 { let b: u8[] = bytes(); return b.len() * 100 + (b[2] as i32); }
function main(): i32 { return 0; }
`)
	linked := linkAsyncProvider(t, dir, consumer, asyncMemProvider)
	for _, c := range []struct{ invoke, want string }{
		{"t-send()", "5"},
		{"t-sum()", "42"},
		{"t-isum()", "42"},
		{"t-add()", "42"},
		{"t-mixed()", "42"},
		{"t-echo()", "42"},
		{"t-bytes()", "399"},
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

// linkAsyncProvider assembles the WAT provider and links it into consumer
// with `wasm-tools compose`, returning the linked component's path.
func linkAsyncProvider(t *testing.T, dir, consumer, providerWat string) string {
	t.Helper()
	watPath, provider, linked := filepath.Join(dir, "provider.wat"), filepath.Join(dir, "provider.wasm"), filepath.Join(dir, "linked.wasm")
	if err := os.WriteFile(watPath, []byte(providerWat), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("wasm-tools", "parse", watPath, "-o", provider).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse provider: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "compose", consumer, "-d", provider, "-o", linked).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools compose: %v\n%s", err, out)
	}
	return linked
}

// TestSelfHostWasmAsyncImportRefusals pins what an async import cannot lower
// yet, and that one naming an interface the fern world declares is refused:
// the component would import that interface twice.
func TestSelfHostWasmAsyncImportRefusals(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	for _, c := range []struct{ name, src, want string }{
		{"struct-param", `struct P { a: i32 }
@import("test:dep/d", "f") async function f(p: P): i32;
function main(): i32 { return f(P { a: 1 }); }`, "async import f: parameter p has type P, which an async import cannot take yet"},
		{"five-params", `@import("test:dep/d", "f") async function f(a: i32, b: i32, c: i32, d: i32, e: i32): i32;
function main(): i32 { return f(1, 2, 3, 4, 5); }`, "async import f: its parameters flatten to more than four core values"},
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
