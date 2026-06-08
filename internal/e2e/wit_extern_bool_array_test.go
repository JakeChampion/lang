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

// buildBoolArrayUserWorld writes the shared user world (custom src iface + wasi
// stdout), embeds it into an empty module, and decodes the resulting world.
func buildBoolArrayUserWorld(t *testing.T, wasmtools, dir, iface string) *componenttype.World {
	t.Helper()
	run := func(name string, args ...string) {
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
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
	return w
}

// buildComposeRunBoolArray builds the core from src, composes it against the
// world + provider, and runs it under wasmtime, asserting `want` in stdout.
func buildComposeRunBoolArray(t *testing.T, wasmtime, wasmtools, dir, src, want, provider string, w *componenttype.World) {
	t.Helper()
	run := func(name string, args ...string) {
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	info, prog := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		PrintMainResult:    true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	if !bytes.Contains(core, []byte("local:test/src@0.1.0")) {
		t.Fatalf("core is missing the custom extern import")
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

// TestExternBoolArrayParamCustomProvider is the P4c bool[]-*param* gate
// (docs/WIT-BRING-YOUR-OWN.md): a Fern bool[] passed to an `@import` extern
// whose WIT takes `list<bool>`. A Fern bool is a 4-byte i32 (0/1) but the
// canonical list<bool> element is a single byte, so the wrapper byte-repacks the
// array (contracting each 4-byte slot to one byte) into a fresh buffer and
// passes (buf, count) — it can't pass the array zero-copy like the numeric
// arrays. The provider exports `count-true: func(b: list<bool>) -> s32` summing
// the true bytes; the Fern side passes [true, false, true] and expects 2.
func TestExternBoolArrayParamCustomProvider(t *testing.T) {
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
	if err := os.MkdirAll(provWit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const iface = "interface src { count-true: func(b: list<bool>) -> s32; }"
	if err := os.WriteFile(filepath.Join(provWit, "src.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export src; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	// list<bool> is lowered into the provider's memory (1 byte/elem) via
	// cabi_realloc; count-true sums the bytes.
	if err := os.WriteFile(filepath.Join(dir, "prov_core.wat"), []byte(`(module
  (memory (export "memory") 1)
  (global $h (mut i32) (i32.const 1024))
  (func (export "cabi_realloc") (param $op i32) (param $os i32) (param $al i32) (param $ns i32) (result i32)
    (local $p i32)
    (local.set $p (global.get $h))
    (global.set $h (i32.add (global.get $h) (local.get $ns)))
    (local.get $p))
  (func (export "local:test/src@0.1.0#count-true") (param $ptr i32) (param $len i32) (result i32)
    (local $sum i32) (local $i i32)
    (block $d (loop $c
      (br_if $d (i32.ge_u (local.get $i) (local.get $len)))
      (local.set $sum (i32.add (local.get $sum) (i32.load8_u (i32.add (local.get $ptr) (local.get $i)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $c)))
    (local.get $sum)))`), 0o644); err != nil {
		t.Fatalf("write provider core: %v", err)
	}
	provider := filepath.Join(dir, "provider.wasm")
	run(wasmtools, "parse", filepath.Join(dir, "prov_core.wat"), "-o", filepath.Join(dir, "prov_core.wasm"))
	run(wasmtools, "component", "embed", provWit, "-w", "provider", filepath.Join(dir, "prov_core.wasm"), "-o", filepath.Join(dir, "prov_embed.wasm"))
	run(wasmtools, "component", "new", filepath.Join(dir, "prov_embed.wasm"), "-o", provider)

	w := buildBoolArrayUserWorld(t, wasmtools, dir, iface)
	const want = "ct-ok"
	src := `@import("local:test/src@0.1.0", "count-true")
function count_true(b: boolean[]): i32;

function main(): i32 {
	var bs: boolean[] = [true, false, true];
	if (count_true(bs) == 2) { write("` + want + `"); } else { write("ct-bad"); }
	return 0;
}`
	buildComposeRunBoolArray(t, wasmtime, wasmtools, dir, src, want, provider, w)
}
