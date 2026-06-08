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

// TestExternSingleFieldRecordResultCustomProvider is the P4c single-field
// record-*result* gate (docs/WIT-BRING-YOUR-OWN.md): an `@import` extern
// returns a record with exactly one field. Unlike a multi-field record (which
// flattens to > 1 core value and so returns indirectly through a return-area
// pointer), a single-field record flattens to exactly one core value — it fits
// MAX_FLAT_RESULTS=1 — so the canonical ABI returns it *by value*. The raw
// import returns the field's core valtype directly (no return area, no
// cabi_realloc), and the wrapper materializes the one-field Fern struct from it
// (ir.ExternRecordResult.Direct).
//
// The provider exports `make-wrapped: func(a: s32) -> record wrapped { v: s32 }`
// returning {v: a} by value. The Fern side reads w.v and expects 42.
func TestExternSingleFieldRecordResultCustomProvider(t *testing.T) {
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

	// --- Provider component exporting local:test/src@0.1.0 make-wrapped. ---
	provWit := filepath.Join(dir, "provwit")
	if err := os.MkdirAll(provWit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const iface = "interface src { record wrapped { v: s32 } make-wrapped: func(a: s32) -> wrapped; }"
	if err := os.WriteFile(filepath.Join(provWit, "src.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export src; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	// A single-field record result is returned by value (it fits the 1 flat
	// result slot), so the export is just `(param $a i32) (result i32)` — no
	// return area, no cabi_realloc.
	provCoreWat := filepath.Join(dir, "prov_core.wat")
	if err := os.WriteFile(provCoreWat, []byte(`(module
  (memory (export "memory") 1)
  (func (export "local:test/src@0.1.0#make-wrapped") (param $a i32) (result i32)
    (local.get $a)))`), 0o644); err != nil {
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

	// --- User program: make_wrapped(42) -> Wrapped; check w.v == 42. ---
	const want = "w-ok"
	src := `struct Wrapped { v: i32 }
@import("local:test/src@0.1.0", "make-wrapped")
function make_wrapped(a: i32): Wrapped;

function main(): i32 {
	var w: Wrapped = make_wrapped(42);
	if (w.v == 42) { write("` + want + `"); } else { write("w-bad"); }
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
