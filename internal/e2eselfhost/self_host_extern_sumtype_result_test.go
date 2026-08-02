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

// TestSelfHostExternSumTypeResultCustomProvider is the self-host port of the
// sum-type `@import` result (docs/WIT-BRING-YOUR-OWN.md): the self-hosted wasm
// backend materializes a self-host Option/Result from a canonical result. The
// host writes (disc:u8 @0, payload:i32 @4) into the return area; the wrapper
// allocs a self-host enum box [tag:i32 @0][payload @4], remapping the
// discriminant back (1-disc for option since canonical none=0/some=1 reverses
// Fern's Some=0/None=1; result matches). Tested via the custom-provider harness:
// div(a,b) -> result<s32,s32> (b≠0 → ok(a/b), else err) and half(n) ->
// option<s32> (even → some(n/2), else none). Mirror of the Go
// TestExternSumTypeResultCustomProvider.
func TestSelfHostExternSumTypeResultCustomProvider(t *testing.T) {
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

	provWit := filepath.Join(dir, "provwit")
	if err := os.MkdirAll(provWit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const iface = "interface src { div: func(a: s32, b: s32) -> result<s32, s32>; half: func(n: s32) -> option<s32>; }"
	if err := os.WriteFile(filepath.Join(provWit, "src.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export src; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prov_core.wat"), []byte(`(module
  (memory (export "memory") 1)
  (global $h (mut i32) (i32.const 1024))
  (func $ra (param $ns i32) (result i32)
    (local $p i32) (local.set $p (global.get $h))
    (global.set $h (i32.add (global.get $h) (local.get $ns))) (local.get $p))
  (func (export "cabi_realloc") (param $op i32) (param $os i32) (param $al i32) (param $ns i32) (result i32)
    (call $ra (local.get $ns)))
  (func (export "local:test/src@0.1.0#div") (param $a i32) (param $b i32) (result i32)
    (local $r i32) (local.set $r (call $ra (i32.const 8)))
    (if (i32.eqz (local.get $b))
      (then (i32.store8 (local.get $r) (i32.const 1)) (i32.store offset=4 (local.get $r) (i32.const 0)))
      (else (i32.store8 (local.get $r) (i32.const 0)) (i32.store offset=4 (local.get $r) (i32.div_s (local.get $a) (local.get $b)))))
    (local.get $r))
  (func (export "local:test/src@0.1.0#half") (param $n i32) (result i32)
    (local $r i32) (local.set $r (call $ra (i32.const 8)))
    (if (i32.eqz (i32.and (local.get $n) (i32.const 1)))
      (then (i32.store8 (local.get $r) (i32.const 1)) (i32.store offset=4 (local.get $r) (i32.div_s (local.get $n) (i32.const 2))))
      (else (i32.store8 (local.get $r) (i32.const 0))))
    (local.get $r)))`), 0o644); err != nil {
		t.Fatalf("write provider core: %v", err)
	}
	provider := filepath.Join(dir, "provider.wasm")
	run(wasmtools, "parse", filepath.Join(dir, "prov_core.wat"), "-o", filepath.Join(dir, "prov_core.wasm"))
	run(wasmtools, "component", "embed", provWit, "-w", "provider", filepath.Join(dir, "prov_core.wasm"), "-o", filepath.Join(dir, "prov_embed.wasm"))
	run(wasmtools, "component", "new", filepath.Join(dir, "prov_embed.wasm"), "-o", provider)

	userWit := filepath.Join(dir, "userwit")
	if out, err := exec.Command("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps")).CombinedOutput(); err != nil {
		_ = os.MkdirAll(userWit, 0o755)
		run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
		_ = out
	}
	if err := os.MkdirAll(filepath.Join(userWit, "deps", "test"), 0o755); err != nil {
		t.Fatalf("mkdir deps/test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "deps", "test", "src.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\n"), 0o644); err != nil {
		t.Fatalf("write user src dep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import local:test/src@0.1.0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write user world: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", userWit, "-w", "u", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_runio_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")

	const want = "res-ok"
	prog := `@import("local:test/src@0.1.0", "div")
function div_ext(a: i32, b: i32): Result[i32, i32];
@import("local:test/src@0.1.0", "half")
function half_ext(n: i32): Option[i32];
function main(): i32 {
    var okv: i32 = 0 - 9;
    match (div_ext(20, 4)) { Ok(v) => { okv = v; }, Err(e) => { okv = 0 - 1; } }
    var errv: i32 = 0 - 9;
    match (div_ext(1, 0)) { Ok(v) => { errv = 99; }, Err(e) => { errv = 7; } }
    var somev: i32 = 0 - 9;
    match (half_ext(10)) { Some(v) => { somev = v; }, None => { somev = 0 - 1; } }
    var nonev: i32 = 0 - 9;
    match (half_ext(3)) { Some(v) => { nonev = 99; }, None => { nonev = 8; } }
    if (okv == 5 && errv == 7 && somev == 5 && nonev == 8) { write("` + want + `"); } else { write("res-bad"); }
    return 0;
}`
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(watBytes) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes")
	}
	watPath := filepath.Join(dir, "core.wat")
	if err := os.WriteFile(watPath, watBytes, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "core.wasm")
	run(wasmtools, "parse", watPath, "-o", corePath)
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}
	userComp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	userPath := filepath.Join(dir, "user.wasm")
	if err := os.WriteFile(userPath, userComp, 0o644); err != nil {
		t.Fatalf("write user component: %v", err)
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
