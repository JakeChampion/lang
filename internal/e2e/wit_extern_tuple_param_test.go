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

// TestExternTupleParamCustomProvider is the P4c tuple-parameter gate
// (docs/WIT-BRING-YOUR-OWN.md): a Fern program passes a tuple to an `@import`
// extern whose WIT signature takes a `tuple`. A tuple is laid out like a struct
// (rc header + elements), so the same record machinery flattens it — the
// canonical ABI flattens a small tuple param to its elements' core types, and
// the wasm wrapper loads each element off the Fern tuple value and pushes it in
// order (the layout precomputed during IR lowering — ir.ExternFunc.ParamRecords
// now covers tuples via externCompositeFieldTypes).
//
// The provider exports `sum-pair: func(p: tuple<s32, s32>) -> s32`, receiving
// the flattened (x, y) and returning their sum. The Fern side passes (10, 32).
func TestExternTupleParamCustomProvider(t *testing.T) {
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

	// --- Provider component exporting local:test/sink@0.1.0 sum-point. ---
	provWit := filepath.Join(dir, "provwit")
	if err := os.MkdirAll(provWit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const iface = "interface sink { sum-pair: func(p: tuple<s32, s32>) -> s32; }"
	if err := os.WriteFile(filepath.Join(provWit, "sink.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export sink; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	// sum-point receives the record flattened to (x, y) — two i32 params — and
	// returns their sum.
	provCoreWat := filepath.Join(dir, "prov_core.wat")
	if err := os.WriteFile(provCoreWat, []byte(`(module
  (memory (export "memory") 1)
  (func (export "local:test/sink@0.1.0#sum-pair") (param $x i32) (param $y i32) (result i32)
    (i32.add (local.get $x) (local.get $y))))`), 0o644); err != nil {
		t.Fatalf("write provider core: %v", err)
	}
	provCore := filepath.Join(dir, "prov_core.wasm")
	provEmbed := filepath.Join(dir, "prov_embed.wasm")
	provider := filepath.Join(dir, "provider.wasm")
	run(wasmtools, "parse", provCoreWat, "-o", provCore)
	run(wasmtools, "component", "embed", provWit, "-w", "provider", provCore, "-o", provEmbed)
	run(wasmtools, "component", "new", provEmbed, "-o", provider)

	// --- User world: custom sink iface + wasi stdout. ---
	userWit := filepath.Join(dir, "userwit")
	if out, err := exec.Command("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps")).CombinedOutput(); err != nil {
		_ = os.MkdirAll(userWit, 0o755)
		run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
		_ = out
	}
	if err := os.MkdirAll(filepath.Join(userWit, "deps", "test"), 0o755); err != nil {
		t.Fatalf("mkdir deps/test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "deps", "test", "sink.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\n"), 0o644); err != nil {
		t.Fatalf("write user sink dep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import local:test/sink@0.1.0;\n}\n"), 0o644); err != nil {
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

	// --- User program: pass Point{ x: 10, y: 32 } (sum 42) to the extern. ---
	const want = "pair-ok"
	src := `@import("local:test/sink@0.1.0", "sum-pair")
function sum_pair(p: (i32, i32)): i32;

function main(): i32 {
	var p: (i32, i32) = (10, 32);
	if (sum_pair(p) == 42) { write("` + want + `"); } else { write("pair-bad"); }
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
	if !bytes.Contains(core, []byte("local:test/sink@0.1.0")) {
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
