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

// TestExternVariantNonUniformParamCustomProvider is the *non-uniform* `variant`
// *parameter* gate (docs/WIT-BRING-YOUR-OWN.md): a Fern enum whose payloaded arms
// carry different scalar types of the same canonical core width —
// `enum Num { I(i32), F(f32) }` (both 32-bit) — passed to an `@import` extern
// taking the WIT `variant num { i(s32), f(f32) }`. The canonical join is the
// i32 bit-container, so the (disc, payload) wrapper pushes the payload's *bits*
// (`i32.load` of the box slot) regardless of arm; the host reinterprets per disc.
// The f32 arm's bits survive: the provider recovers the float with
// `f32.reinterpret_i32`.
//
// `take: func(v: num) -> s32`: i(p) → p, f(p) → `(reinterpret(p) * 4) as s32`.
// The Fern side passes I(7) (→ 7) and F(2.5) (→ 10 only if the f32 bits round-trip).
func TestExternVariantNonUniformParamCustomProvider(t *testing.T) {
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
	const iface = "interface sink { variant num { i(s32), f(f32) } take: func(v: num) -> s32; }"
	if err := os.WriteFile(filepath.Join(provWit, "sink.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export sink; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	// take receives the variant flattened to (disc:i32, payload:i32-join). The f
	// arm's payload is the f32 bits, recovered via f32.reinterpret_i32.
	if err := os.WriteFile(filepath.Join(dir, "prov_core.wat"), []byte(`(module
  (memory (export "memory") 1)
  (func (export "local:test/sink@0.1.0#take") (param $disc i32) (param $p i32) (result i32)
    (if (result i32) (i32.eqz (local.get $disc))
      (then (local.get $p))
      (else (i32.trunc_f32_s (f32.mul (f32.reinterpret_i32 (local.get $p)) (f32.const 4)))))))`), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(userWit, "deps", "test", "sink.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\n"), 0o644); err != nil {
		t.Fatalf("write user sink dep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import local:test/sink@0.1.0;\n}\n"), 0o644); err != nil {
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

	const want = "np-ok"
	src := `enum Num { I(i32), F(f32) }

@import("local:test/sink@0.1.0", "take")
function take(v: Num): i32;

function main(): i32 {
	if (take(I(7)) == 7 && take(F(2.5)) == 10) { write("` + want + `"); } else { write("np-bad"); }
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
