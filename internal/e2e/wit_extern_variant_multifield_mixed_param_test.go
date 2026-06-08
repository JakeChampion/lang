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

// TestExternVariantMultiFieldMixedParamCustomProvider is the *general* multi-field
// `variant` *parameter* gate (docs/WIT-BRING-YOUR-OWN.md): a Fern enum whose cases
// carry more than one payload AND whose fields are mixed-width / float — `enum Ev {
// Move(i32, i64), Spin(f32, f64), Stop }` — passed to an `@import` extern taking the
// WIT `variant ev { move(tuple<s32, s64>), spin(tuple<f32, f64>), stop }`.
//
// The canonical join is computed position-wise: slot0 = join(s32, f32) = i32,
// slot1 = join(s64, f64) = i64. So the flattened param is (disc:i32, s0:i32,
// s1:i64). The Move arm's fields pass directly (i32, i64); the Spin arm's f32 rides
// the i32 slot as its raw bits and its f64 rides the i64 slot as its raw bits —
// the provider recovers them with f32.reinterpret_i32 / f64.reinterpret_i64.
//
// `take: func(e: ev) -> s32`: Move(a,b) → a + b*1000, Spin(f,d) → trunc(f) +
// trunc(d)*1000 + 500000, Stop → -1. The Fern side passes Move(5,7), Spin(2.0,3.0),
// Stop, each weighted so a wrong slot width / coercion fails.
func TestExternVariantMultiFieldMixedParamCustomProvider(t *testing.T) {
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
	const iface = "interface sink { variant ev { move(tuple<s32, s64>), spin(tuple<f32, f64>), stop } take: func(e: ev) -> s32; }"
	if err := os.WriteFile(filepath.Join(provWit, "sink.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export sink; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	// take receives the variant flattened to (disc:i32, s0:i32, s1:i64). For Spin
	// the i32/i64 slots carry the f32/f64 bits, recovered via reinterpret.
	if err := os.WriteFile(filepath.Join(dir, "prov_core.wat"), []byte(`(module
  (memory (export "memory") 1)
  (func (export "local:test/sink@0.1.0#take") (param $disc i32) (param $s0 i32) (param $s1 i64) (result i32)
    (if (result i32) (i32.eqz (local.get $disc))
      (then (i32.add (local.get $s0) (i32.mul (i32.wrap_i64 (local.get $s1)) (i32.const 1000))))
      (else (if (result i32) (i32.eq (local.get $disc) (i32.const 1))
        (then (i32.add
          (i32.add
            (i32.trunc_f32_s (f32.reinterpret_i32 (local.get $s0)))
            (i32.mul (i32.trunc_f64_s (f64.reinterpret_i64 (local.get $s1))) (i32.const 1000)))
          (i32.const 500000)))
        (else (i32.const -1)))))))`), 0o644); err != nil {
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

	const want = "mfmp-ok"
	src := `enum Ev { Move(i32, i64), Spin(f32, f64), Stop }

@import("local:test/sink@0.1.0", "take")
function take(e: Ev): i32;

function main(): i32 {
	if (take(Move(5, 7)) == 7005 && take(Spin(2.0, 3.0)) == 503002 && take(Stop) == -1) { write("` + want + `"); } else { write("mfmp-bad"); }
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
