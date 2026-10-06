package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestSelfHostExternHeapResultCustomProvider gates the `@import` result shapes
// whose payload the host materialises in guest memory and the wrapper copies
// into a Fern value (docs/WIT-BRING-YOUR-OWN.md): an `Option[string]`
// (option<string>), a `Result[u8[], i32]` (result<list<u8>, u32>), a variant
// with a string-payload arm beside unit arms (`variant { get, post,
// other(string) }`), a `(string, string)[]` (list<tuple<string, string>>), and
// an Option / Result result alongside string parameters (result<_, u32> from
// `append(f, name, value)`) — the five a wasi:http handler reads its request
// and writes its response through. A hand-written provider component
// implements the interface; the self-host program's core is composed against
// a world importing it and linked with wasm-tools compose, then run under
// wasmtime.
func TestSelfHostExternHeapResultCustomProvider(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	const iface = `interface src {
  variant method { get, post, other(string) }
  path: func() -> option<string>;
  read-chunk: func(n: u32) -> result<list<u8>, u32>;
  verb: func(k: u32) -> method;
  entries: func() -> list<tuple<string, string>>;
  append: func(f: u32, name: string, value: string) -> result<_, u32>;
}`
	write("provwit/src.wit", "package local:test@0.1.0;\n"+iface+"\nworld provider { export src; }\n")
	// The provider: every result goes through a return area its bump
	// allocator hands out 8-aligned (the canonical ABI reads the area at the
	// type's alignment). Strings live in its data section.
	write("prov_core.wat", `(module
  (memory (export "memory") 1)
  (data (i32.const 32) "/hello")
  (data (i32.const 48) "PATCH")
  (data (i32.const 64) "content-type")
  (data (i32.const 80) "text/plain")
  (data (i32.const 96) "x-fern")
  (data (i32.const 112) "yes")
  (global $h (mut i32) (i32.const 1024))
  (func $ra (param $ns i32) (result i32)
    (local $p i32) (local.set $p (i32.and (i32.add (global.get $h) (i32.const 7)) (i32.const -8)))
    (global.set $h (i32.add (local.get $p) (local.get $ns))) (local.get $p))
  (func (export "cabi_realloc") (param $op i32) (param $os i32) (param $al i32) (param $ns i32) (result i32)
    (call $ra (local.get $ns)))
  (func (export "local:test/src@0.1.0#path") (result i32)
    (local $r i32) (local.set $r (call $ra (i32.const 12)))
    (i32.store8 (local.get $r) (i32.const 1))
    (i32.store offset=4 (local.get $r) (i32.const 32))
    (i32.store offset=8 (local.get $r) (i32.const 6))
    (local.get $r))
  (func (export "local:test/src@0.1.0#read-chunk") (param $n i32) (result i32)
    (local $r i32) (local $b i32) (local.set $r (call $ra (i32.const 12)))
    (if (i32.eqz (local.get $n))
      (then (i32.store8 (local.get $r) (i32.const 1)) (i32.store offset=4 (local.get $r) (i32.const 7)))
      (else
        (local.set $b (call $ra (i32.const 3)))
        (i32.store8 (local.get $b) (i32.const 1))
        (i32.store8 offset=1 (local.get $b) (i32.const 2))
        (i32.store8 offset=2 (local.get $b) (i32.const 3))
        (i32.store8 (local.get $r) (i32.const 0))
        (i32.store offset=4 (local.get $r) (local.get $b))
        (i32.store offset=8 (local.get $r) (i32.const 3))))
    (local.get $r))
  (func (export "local:test/src@0.1.0#verb") (param $k i32) (result i32)
    (local $r i32) (local.set $r (call $ra (i32.const 12)))
    (i32.store8 (local.get $r) (local.get $k))
    (if (i32.eq (local.get $k) (i32.const 2)) (then
      (i32.store offset=4 (local.get $r) (i32.const 48))
      (i32.store offset=8 (local.get $r) (i32.const 5))))
    (local.get $r))
  (func (export "local:test/src@0.1.0#entries") (result i32)
    (local $r i32) (local $e i32)
    (local.set $r (call $ra (i32.const 8)))
    (local.set $e (call $ra (i32.const 32)))
    (i32.store (local.get $e) (i32.const 64)) (i32.store offset=4 (local.get $e) (i32.const 12))
    (i32.store offset=8 (local.get $e) (i32.const 80)) (i32.store offset=12 (local.get $e) (i32.const 10))
    (i32.store offset=16 (local.get $e) (i32.const 96)) (i32.store offset=20 (local.get $e) (i32.const 6))
    (i32.store offset=24 (local.get $e) (i32.const 112)) (i32.store offset=28 (local.get $e) (i32.const 3))
    (i32.store (local.get $r) (local.get $e))
    (i32.store offset=4 (local.get $r) (i32.const 2))
    (local.get $r))
  (func (export "local:test/src@0.1.0#append") (param $f i32) (param $np i32) (param $nl i32) (param $vp i32) (param $vl i32) (result i32)
    (local $r i32) (local.set $r (call $ra (i32.const 8)))
    (if (i32.eqz (local.get $vl))
      (then (i32.store8 (local.get $r) (i32.const 1)) (i32.store offset=4 (local.get $r) (i32.const 3)))
      (else (i32.store8 (local.get $r) (i32.const 0))))
    (local.get $r)))`)
	provider := filepath.Join(dir, "provider.wasm")
	run(wasmtools, "parse", filepath.Join(dir, "prov_core.wat"), "-o", filepath.Join(dir, "prov_core.wasm"))
	run(wasmtools, "component", "embed", filepath.Join(dir, "provwit"), "-w", "provider", filepath.Join(dir, "prov_core.wasm"), "-o", filepath.Join(dir, "prov_embed.wasm"))
	run(wasmtools, "component", "new", filepath.Join(dir, "prov_embed.wasm"), "-o", provider)

	userWit := filepath.Join(dir, "userwit")
	if err := os.MkdirAll(userWit, 0o755); err != nil {
		t.Fatal(err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
	write("userwit/deps/test/src.wit", "package local:test@0.1.0;\n"+iface+"\n")
	write("userwit/world.wit", "package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import local:test/src@0.1.0;\n}\n")
	run(wasmtools, "parse", write("empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", userWit, "-w", "u", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_runio_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_runio_run.fern", "wasm_runio_run")

	const want = "heap-ok"
	prog := `enum Method { Get, Post, Other(string) }
@import("local:test/src@0.1.0", "path")
function path_ext(): Option[string];
@import("local:test/src@0.1.0", "read-chunk")
function read_ext(n: i32): Result[u8[], i32];
@import("local:test/src@0.1.0", "verb")
function verb_ext(k: i32): Method;
@import("local:test/src@0.1.0", "entries")
function entries_ext(): (string, string)[];
@import("local:test/src@0.1.0", "append")
function append_ext(f: i32, name: string, value: string): Result[i32, i32];
function main(): i32 {
    let p: string = "none";
    match (path_ext()) { Some(s) => { p = s; }, None => { p = "none"; } }
    let rd: i32 = 0;
    match (read_ext(3)) {
        Ok(b) => { if (b.len() == 3 && (b[0] as i32) == 1 && (b[2] as i32) == 3) { rd = 1; } },
        Err(e) => { rd = 0 - e; }
    }
    let rerr: i32 = 0;
    match (read_ext(0)) { Ok(b) => { rerr = 99; }, Err(e) => { rerr = e; } }
    let m0: string = "";
    match (verb_ext(0)) { Get => { m0 = "GET"; }, Post => { m0 = "POST"; }, Other(s) => { m0 = s; } }
    let m2: string = "";
    match (verb_ext(2)) { Get => { m2 = "GET"; }, Post => { m2 = "POST"; }, Other(s) => { m2 = s; } }
    let es: (string, string)[] = entries_ext();
    let (n0, v0) = es[0];
    let (n1, v1) = es[1];
    let ap: i32 = 0;
    match (append_ext(1, "x-a", "b")) { Ok(v) => { ap = 1; }, Err(e) => { ap = 0 - e; } }
    let ae: i32 = 0;
    match (append_ext(1, "x-a", "")) { Ok(v) => { ae = 99; }, Err(e) => { ae = e; } }
    if (p == "/hello" && rd == 1 && rerr == 7 && m0 == "GET" && m2 == "PATCH"
        && es.len() == 2 && n0 == "content-type" && v0 == "text/plain" && n1 == "x-fern" && v1 == "yes"
        && ap == 1 && ae == 3) { write("` + want + `"); } else { write("heap-bad"); }
    return 0;
}`
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(watBytes) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes")
	}
	watPath := write("core.wat", string(watBytes))
	corePath := filepath.Join(dir, "core.wasm")
	run(wasmtools, "parse", watPath, "-o", corePath)
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatal(err)
	}
	userComp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	userPath := filepath.Join(dir, "user.wasm")
	if err := os.WriteFile(userPath, userComp, 0o644); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(dir, "final.wasm")
	run(wasmtools, "compose", userPath, "--definitions", provider, "-o", final)
	run(wasmtools, "validate", final)
	out, err := exec.Command(wasmtime, "run", final).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}
