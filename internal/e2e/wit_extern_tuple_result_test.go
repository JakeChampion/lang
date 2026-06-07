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

// TestExternTupleResultCustomProvider is the P4c tuple-*result* gate
// (docs/WIT-BRING-YOUR-OWN.md): an `@import` extern returns a `tuple`, lifted
// into a Fern tuple. A multi-element tuple flattens to > 1 core value, so (like
// a record) the canonical ABI returns it indirectly through a return-area
// pointer; the wasm result wrapper allocs that area, calls the import, then
// materializes a Fern tuple (rc header + per-element stores at the layout
// offsets precomputed during IR lowering, ir.ExternFunc.ResultRecord) and
// returns its pointer.
//
// The provider exports `make-pair: func(a: s32, b: s32) -> tuple<s32, s32>`,
// returning (a, b). The Fern side reads p.0 + p.1 and expects 42.
func TestExternTupleResultCustomProvider(t *testing.T) {
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

	// --- Provider component exporting local:test/src@0.1.0 make-point. ---
	provWit := filepath.Join(dir, "provwit")
	if err := os.MkdirAll(provWit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const iface = "interface src { make-pair: func(a: s32, b: s32) -> tuple<s32, s32>; }"
	if err := os.WriteFile(filepath.Join(provWit, "src.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export src; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	// make-point returns the record indirectly: it allocates an 8-byte area
	// (x@0, y@4) via cabi_realloc and returns its pointer (the canonical
	// record-result convention for an export).
	provCoreWat := filepath.Join(dir, "prov_core.wat")
	if err := os.WriteFile(provCoreWat, []byte(`(module
  (memory (export "memory") 1)
  (global $h (mut i32) (i32.const 1024))
  (func (export "cabi_realloc") (param $op i32) (param $os i32) (param $al i32) (param $ns i32) (result i32)
    (local $p i32)
    (local.set $p (global.get $h))
    (global.set $h (i32.add (global.get $h) (local.get $ns)))
    (local.get $p))
  (func (export "local:test/src@0.1.0#make-pair") (param $a i32) (param $b i32) (result i32)
    (local $r i32)
    (local.set $r (call 0 (i32.const 0) (i32.const 0) (i32.const 4) (i32.const 8)))
    (i32.store (local.get $r) (local.get $a))
    (i32.store offset=4 (local.get $r) (local.get $b))
    (local.get $r)))`), 0o644); err != nil {
		t.Fatalf("write provider core: %v", err)
	}
	provCore := filepath.Join(dir, "prov_core.wasm")
	provEmbed := filepath.Join(dir, "prov_embed.wasm")
	provider := filepath.Join(dir, "provider.wasm")
	run(wasmtools, "parse", provCoreWat, "-o", provCore)
	run(wasmtools, "component", "embed", provWit, "-w", "provider", provCore, "-o", provEmbed)
	run(wasmtools, "component", "new", provEmbed, "-o", provider)

	// --- User world: custom src iface + wasi stdout. ---
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
	emptyWat := filepath.Join(dir, "empty.wat")
	emptyWasm := filepath.Join(dir, "empty.wasm")
	embedded := filepath.Join(dir, "embedded.wasm")
	if err := os.WriteFile(emptyWat, []byte("(module)"), 0o644); err != nil {
		t.Fatalf("write empty.wat: %v", err)
	}
	run(wasmtools, "parse", emptyWat, "-o", emptyWasm)
	run(wasmtools, "component", "embed", userWit, "-w", "u", emptyWasm, "-o", embedded)
	embeddedBytes, err := os.ReadFile(embedded)
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	// --- User program: make_point(10, 32) -> Point; check x + y == 42. ---
	const want = "mkp-ok"
	src := `@import("local:test/src@0.1.0", "make-pair")
function make_pair(a: i32, b: i32): (i32, i32);

function main(): i32 {
	var p: (i32, i32) = make_pair(10, 32);
	if (p.0 + p.1 == 42) { write("` + want + `"); } else { write("mkp-bad"); }
	return 0;
}`
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
