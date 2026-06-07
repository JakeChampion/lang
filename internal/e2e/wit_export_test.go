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

// TestExportScalarReactorComposes is the P6 slice-3 gate
// (docs/WIT-BRING-YOUR-OWN.md): the world-driven composer lifts an arbitrary
// scalar `@export` function as a named world export. A reactor (a library with
// no cli/run) exports `local:test/math@0.1.0#add`; the wasm backend surfaces
// the `iface#wit-name` core export (slice 2) and ComposeExportsFromWorld aliases
// it, lifts it with the WIT canonical ABI `(u32,u32)->u32`, and emits the
// component export. Gated by `wasm-tools validate` (the lift + export are
// well-formed); running it through a consumer is the next slice.
func TestExportScalarReactorComposes(t *testing.T) {
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

	// A world that EXPORTS a custom scalar interface.
	witDir := filepath.Join(dir, "wit")
	if err := os.MkdirAll(witDir, 0o755); err != nil {
		t.Fatalf("mkdir wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "world.wit"),
		[]byte("package local:test@0.1.0;\ninterface math {\n  add: func(a: u32, b: u32) -> u32;\n}\nworld m {\n  export math;\n}\n"), 0o644); err != nil {
		t.Fatalf("write world.wit: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", witDir, "-w", "m", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	// A reactor exporter: just the @export function, no main / cli/run.
	src := `@export("local:test/math@0.1.0", "add")
function add(a: i32, b: i32): i32 { return a + b; }`
	mainPath := filepath.Join(dir, "exporter.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	info, prog := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	if !bytes.Contains(core, []byte("local:test/math@0.1.0#add")) {
		t.Fatalf("core is missing the surfaced @export core export")
	}
	comp, err := component.ComposeExportsFromWorld(core, w)
	if err != nil {
		t.Fatalf("ComposeExportsFromWorld: %v", err)
	}
	mine := filepath.Join(dir, "exporter.component.wasm")
	if err := os.WriteFile(mine, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	// The component must declare the world export.
	out, err := exec.Command(wasmtools, "component", "wit", mine).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("local:test/math@0.1.0")) {
		t.Fatalf("component WIT missing the exported interface:\n%s", out)
	}
}

// TestExportScalarRunsViaConsumer is the runnable P6 slice-3 gate: a Fern
// reactor's `@export add` is composed into a component (ComposeExportsFromWorld),
// then a separate Fern consumer that `@import`s `local:test/math#add` and calls
// it from main is composed (ComposeFromWorldAuto) and the two are linked with
// `wasm-tools compose`. The consumer runs under wasmtime and observes the
// exporter's result — proving the lifted export is actually callable across the
// component boundary (docs/WIT-BRING-YOUR-OWN.md).
func TestExportScalarRunsViaConsumer(t *testing.T) {
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

	// --- 1. Build the Fern exporter reactor → exporter component. ---
	expWit := filepath.Join(dir, "expwit")
	if err := os.MkdirAll(expWit, 0o755); err != nil {
		t.Fatalf("mkdir expwit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(expWit, "world.wit"),
		[]byte("package local:test@0.1.0;\ninterface math {\n  add: func(a: u32, b: u32) -> u32;\n}\nworld m {\n  export math;\n}\n"), 0o644); err != nil {
		t.Fatalf("write exporter wit: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "ee.wat", "(module)"), "-o", filepath.Join(dir, "ee.wasm"))
	run(wasmtools, "component", "embed", expWit, "-w", "m", filepath.Join(dir, "ee.wasm"), "-o", filepath.Join(dir, "eembed.wasm"))
	expEmbedBytes, err := os.ReadFile(filepath.Join(dir, "eembed.wasm"))
	if err != nil {
		t.Fatalf("read exporter embed: %v", err)
	}
	expWorld, err := componenttype.DecodeWorldBytes(extractComponentType(t, expEmbedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes (exporter): %v", err)
	}
	expSrc := `@export("local:test/math@0.1.0", "add")
function add(a: i32, b: i32): i32 { return a + b; }`
	expPath := filepath.Join(dir, "exporter.fern")
	if err := os.WriteFile(expPath, []byte(expSrc), 0o644); err != nil {
		t.Fatalf("write exporter prog: %v", err)
	}
	expInfo, expProg := loadCheckMono(t, expPath)
	expCore, err := wasmbin.BuildWithOptions(expProg, expInfo, wasmbin.BuildOptions{ForceMemorySection: true, Preview2WASI: true})
	if err != nil {
		t.Fatalf("build exporter core: %v", err)
	}
	expComp, err := component.ComposeExportsFromWorld(expCore, expWorld)
	if err != nil {
		t.Fatalf("ComposeExportsFromWorld: %v", err)
	}
	exporter := filepath.Join(dir, "exporter.wasm")
	if err := os.WriteFile(exporter, expComp, 0o644); err != nil {
		t.Fatalf("write exporter component: %v", err)
	}
	run(wasmtools, "validate", exporter)

	// --- 2. Build the Fern consumer command importing local:test/math#add. ---
	userWit := filepath.Join(dir, "userwit")
	if err := os.MkdirAll(userWit, 0o755); err != nil {
		t.Fatalf("mkdir userwit: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
	if err := os.MkdirAll(filepath.Join(userWit, "deps", "test"), 0o755); err != nil {
		t.Fatalf("mkdir deps/test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "deps", "test", "math.wit"),
		[]byte("package local:test@0.1.0;\ninterface math { add: func(a: u32, b: u32) -> u32; }\n"), 0o644); err != nil {
		t.Fatalf("write math dep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import local:test/math@0.1.0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write user world: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "ue.wat", "(module)"), "-o", filepath.Join(dir, "ue.wasm"))
	run(wasmtools, "component", "embed", userWit, "-w", "u", filepath.Join(dir, "ue.wasm"), "-o", filepath.Join(dir, "uembed.wasm"))
	userEmbedBytes, err := os.ReadFile(filepath.Join(dir, "uembed.wasm"))
	if err != nil {
		t.Fatalf("read user embed: %v", err)
	}
	userWorld, err := componenttype.DecodeWorldBytes(extractComponentType(t, userEmbedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes (user): %v", err)
	}
	const want = "export-ok"
	userSrc := `@import("local:test/math@0.1.0", "add")
function add(a: u32, b: u32): u32;

function main(): i32 {
	if (add(20 as u32, 3 as u32) == 23 as u32) { write("` + want + `"); } else { write("export-bad"); }
	return 0;
}`
	userPath := filepath.Join(dir, "consumer.fern")
	if err := os.WriteFile(userPath, []byte(userSrc), 0o644); err != nil {
		t.Fatalf("write consumer prog: %v", err)
	}
	userInfo, userProg := loadCheckMono(t, userPath)
	userCore, err := wasmbin.BuildWithOptions(userProg, userInfo, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		PrintMainResult:    true,
	})
	if err != nil {
		t.Fatalf("build consumer core: %v", err)
	}
	userComp, err := component.ComposeFromWorldAuto(userCore, userWorld)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto (consumer): %v", err)
	}
	consumer := filepath.Join(dir, "consumer.wasm")
	if err := os.WriteFile(consumer, userComp, 0o644); err != nil {
		t.Fatalf("write consumer component: %v", err)
	}

	// --- 3. Link the exporter into the consumer's import; run it. ---
	final := filepath.Join(dir, "final.wasm")
	run(wasmtools, "compose", consumer, "--definitions", exporter, "-o", final)
	run(wasmtools, "validate", final)
	out, err := exec.Command(wasmtime, "run", final).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}
