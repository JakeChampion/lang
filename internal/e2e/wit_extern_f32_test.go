package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestExternF32CustomProvider: an f32 crosses an `@import` at its canonical
// width, as a scalar and as a list element, where the self-host holds it as an
// f64 (#11030).
func TestExternF32CustomProvider(t *testing.T) {
	cases := []struct {
		name, iface, core, src string
	}{
		{
			name:  "scalar",
			iface: "interface src { scale: func(x: f32, k: f32) -> f32; }",
			core: `(module
  (memory (export "memory") 1)
  (func (export "local:test/src@0.1.0#scale") (param $x f32) (param $k f32) (result f32)
    (f32.mul (local.get $x) (local.get $k))))`,
			src: `@import("local:test/src@0.1.0", "scale")
function scale(x: f32, k: f32): f32;

function main(): i32 {
	let x: f32 = 1.5;
	let k: f32 = 4.0;
	if (scale(x, k) == 6.0) { write("f32-ok"); } else { write("f32-bad"); }
	return 0;
}`,
		},
		{
			name:  "list-result",
			iface: "interface src { halves: func(n: u32) -> list<f32>; }",
			core: `(module
  (memory (export "memory") 1)
  (global $h (mut i32) (i32.const 1024))
  (func (export "cabi_realloc") (param $op i32) (param $os i32) (param $al i32) (param $ns i32) (result i32)
    (local $p i32)
    (local.set $p (global.get $h))
    (global.set $h (i32.add (global.get $h) (local.get $ns)))
    (local.get $p))
  (func (export "local:test/src@0.1.0#halves") (param $n i32) (result i32)
    (local $buf i32) (local $i i32) (local $ret i32)
    (local.set $buf (call 0 (i32.const 0) (i32.const 0) (i32.const 4) (i32.mul (local.get $n) (i32.const 4))))
    (block $d (loop $c
      (br_if $d (i32.ge_u (local.get $i) (local.get $n)))
      (f32.store (i32.add (local.get $buf) (i32.mul (local.get $i) (i32.const 4)))
        (f32.mul (f32.convert_i32_u (local.get $i)) (f32.const 0.5)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $c)))
    (local.set $ret (call 0 (i32.const 0) (i32.const 0) (i32.const 4) (i32.const 8)))
    (i32.store (local.get $ret) (local.get $buf))
    (i32.store (i32.add (local.get $ret) (i32.const 4)) (local.get $n))
    (local.get $ret)))`,
			src: `@import("local:test/src@0.1.0", "halves")
function halves(n: u32): f32[];

function main(): i32 {
	let xs: f32[] = halves(4u32);
	if (xs.len() == 4 && xs[1] == 0.5 && xs[3] == 1.5) { write("f32-ok"); } else { write("f32-bad"); }
	return 0;
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := runExternWithProvider(t, c.iface, c.core, c.src)
			if !bytes.Contains(out, []byte("f32-ok")) {
				t.Fatalf("stdout = %q, want it to contain %q", out, "f32-ok")
			}
		})
	}
}

// runExternWithProvider builds a provider component exporting
// local:test/src@0.1.0 (its core written as WAT), composes the program's
// self-host core against a world importing that interface and wasi stdout,
// links the two and returns what the program printed under wasmtime.
func runExternWithProvider(t *testing.T, iface, provCoreWat, src string) []byte {
	t.Helper()
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
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg := "package local:test@0.1.0;\n" + iface + "\n"

	write(filepath.Join(dir, "provwit", "src.wit"), pkg+"world provider { export src; }\n")
	write(filepath.Join(dir, "prov_core.wat"), provCoreWat)
	provider := filepath.Join(dir, "provider.wasm")
	run(wasmtools, "parse", filepath.Join(dir, "prov_core.wat"), "-o", filepath.Join(dir, "prov_core.wasm"))
	run(wasmtools, "component", "embed", filepath.Join(dir, "provwit"), "-w", "provider", filepath.Join(dir, "prov_core.wasm"), "-o", filepath.Join(dir, "prov_embed.wasm"))
	run(wasmtools, "component", "new", filepath.Join(dir, "prov_embed.wasm"), "-o", provider)

	userWit := filepath.Join(dir, "userwit")
	if err := os.MkdirAll(userWit, 0o755); err != nil {
		t.Fatal(err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
	write(filepath.Join(userWit, "deps", "test", "src.wit"), pkg)
	write(filepath.Join(userWit, "world.wit"), "package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import local:test/src@0.1.0;\n}\n")
	write(filepath.Join(dir, "empty.wat"), "(module)")
	run(wasmtools, "parse", filepath.Join(dir, "empty.wat"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", userWit, "-w", "u", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embedded, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embedded))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	mainPath := filepath.Join(dir, "main.fern")
	write(mainPath, src)
	userComp, err := component.ComposeFromWorldAuto(e2eharness.SelfHostComponentCore(t, mainPath), w)
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
	return out
}
