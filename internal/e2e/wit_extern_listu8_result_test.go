package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestExternListU8ResultCustomProvider is the sub-4-byte `list<T>` result gate
// via a custom provider (docs/WIT-BRING-YOUR-OWN.md): an `@import` extern returns
// a canonical `list<u8>`, lifted into a Fern `u8[]`, composed through
// ComposeFromWorldAuto + a custom provider (not the legacy wasi:random registry).
// Pins the gMemRealloc path, which composes + runs cleanly.
//
// The provider exports `bytes: func(n: u32) -> list<u8>` returning [0,1,…,n-1];
// the Fern side requests 4 and checks element [3] == 3 and the length.
func TestExternListU8ResultCustomProvider(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	provWit := filepath.Join(dir, "provwit")
	os.MkdirAll(provWit, 0o755)
	os.WriteFile(filepath.Join(provWit, "src.wit"),
		[]byte("package local:test@0.1.0;\ninterface src { bytes: func(n: u32) -> list<u8>; }\nworld provider { export src; }\n"), 0o644)
	provCoreWat := filepath.Join(dir, "prov_core.wat")
	os.WriteFile(provCoreWat, []byte(`(module
  (memory (export "memory") 1)
  (global $h (mut i32) (i32.const 1024))
  (func (export "cabi_realloc") (param $op i32) (param $os i32) (param $al i32) (param $ns i32) (result i32)
    (local $p i32)
    (local.set $p (global.get $h))
    (global.set $h (i32.add (global.get $h) (local.get $ns)))
    (local.get $p))
  (func (export "local:test/src@0.1.0#bytes") (param $n i32) (result i32)
    (local $buf i32) (local $i i32) (local $ret i32)
    (local.set $buf (call 0 (i32.const 0) (i32.const 0) (i32.const 1) (local.get $n)))
    (block $d (loop $c
      (br_if $d (i32.ge_u (local.get $i) (local.get $n)))
      (i32.store8 (i32.add (local.get $buf) (local.get $i)) (local.get $i))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $c)))
    (local.set $ret (call 0 (i32.const 0) (i32.const 0) (i32.const 4) (i32.const 8)))
    (i32.store (local.get $ret) (local.get $buf))
    (i32.store (i32.add (local.get $ret) (i32.const 4)) (local.get $n))
    (local.get $ret)))`), 0o644)
	provCore := filepath.Join(dir, "prov_core.wasm")
	provEmbed := filepath.Join(dir, "prov_embed.wasm")
	provider := filepath.Join(dir, "provider.wasm")
	run(wasmtools, "parse", provCoreWat, "-o", provCore)
	run(wasmtools, "component", "embed", provWit, "-w", "provider", provCore, "-o", provEmbed)
	run(wasmtools, "component", "new", provEmbed, "-o", provider)

	userWit := filepath.Join(dir, "userwit")
	if out, err := exec.Command("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps")).CombinedOutput(); err != nil {
		_ = os.MkdirAll(userWit, 0o755)
		run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
		_ = out
	}
	os.MkdirAll(filepath.Join(userWit, "deps", "test"), 0o755)
	os.WriteFile(filepath.Join(userWit, "deps", "test", "src.wit"),
		[]byte("package local:test@0.1.0;\ninterface src { bytes: func(n: u32) -> list<u8>; }\n"), 0o644)
	os.WriteFile(filepath.Join(userWit, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import local:test/src@0.1.0;\n}\n"), 0o644)
	emptyWat := filepath.Join(dir, "empty.wat")
	emptyWasm := filepath.Join(dir, "empty.wasm")
	embedded := filepath.Join(dir, "embedded.wasm")
	os.WriteFile(emptyWat, []byte("(module)"), 0o644)
	run(wasmtools, "parse", emptyWat, "-o", emptyWasm)
	run(wasmtools, "component", "embed", userWit, "-w", "u", emptyWasm, "-o", embedded)
	embeddedBytes, _ := os.ReadFile(embedded)
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	const want = "bytes-ok"
	src := `@import("local:test/src@0.1.0", "bytes")
function get_bytes(n: u32): u8[];

function main(): i32 {
	var xs: u8[] = get_bytes(4u32);
	if (xs.len() == 4 && xs[3] == 3) { write("` + want + `"); } else { write("bytes-bad"); }
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	os.WriteFile(mainPath, []byte(src), 0o644)
	info, prog := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true, Preview2WASI: true, SynthCliRun: true, PrintMainResult: true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	userComp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	userPath := filepath.Join(dir, "user.wasm")
	os.WriteFile(userPath, userComp, 0o644)
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
