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

// TestExternSumTypeParamCustomProvider is the P4c sum-type-parameter gate
// (docs/WIT-BRING-YOUR-OWN.md): a Fern program passes an Option / Result to an
// `@import` extern whose WIT signature takes an `option` / `result`. A Fern
// enum is a heap box `[tag:i32 @0][payload @4]`; the canonical option/result
// flattens to (disc:i32, payload). The wrapper pushes the discriminant (the tag,
// remapped 1-tag for option since Fern's Some=0/None=1 is the reverse of
// canonical none=0/some=1; result's Ok=0/Err=1 matches) then the payload.
//
// The provider exports `check-result: func(r: result<s32, s32>) -> s32` (ok→v,
// err→-v) and `peek-option: func(o: option<s32>) -> s32` (some→v, none→-1). The
// Fern side checks Ok(42)→42, Err(5)→-5, Some(7)→7, None→-1.
func TestExternSumTypeParamCustomProvider(t *testing.T) {
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
	const iface = "interface sink { check-result: func(r: result<s32, s32>) -> s32; peek-option: func(o: option<s32>) -> s32; }"
	if err := os.WriteFile(filepath.Join(provWit, "sink.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export sink; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	// Both functions receive the flattened (disc, payload): result disc 0=ok /
	// 1=err; option disc 0=none / 1=some (canonical order).
	provCoreWat := filepath.Join(dir, "prov_core.wat")
	if err := os.WriteFile(provCoreWat, []byte(`(module
  (memory (export "memory") 1)
  (func (export "local:test/sink@0.1.0#check-result") (param $disc i32) (param $v i32) (result i32)
    (if (result i32) (i32.eqz (local.get $disc))
      (then (local.get $v))
      (else (i32.sub (i32.const 0) (local.get $v)))))
  (func (export "local:test/sink@0.1.0#peek-option") (param $disc i32) (param $v i32) (result i32)
    (if (result i32) (i32.eq (local.get $disc) (i32.const 1))
      (then (local.get $v))
      (else (i32.const -1)))))`), 0o644); err != nil {
		t.Fatalf("write provider core: %v", err)
	}
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

	const want = "sum-ok"
	src := `@import("local:test/sink@0.1.0", "check-result")
function check_result(r: Result[i32, i32]): i32;

@import("local:test/sink@0.1.0", "peek-option")
function peek_option(o: Option[i32]): i32;

function main(): i32 {
	var ok: Result[i32, i32] = Ok(42);
	var err: Result[i32, i32] = Err(5);
	var some: Option[i32] = Some(7);
	var none: Option[i32] = None;
	if (check_result(ok) == 42 && check_result(err) == -5 && peek_option(some) == 7 && peek_option(none) == -1) {
		write("` + want + `");
	} else {
		write("sum-bad");
	}
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
