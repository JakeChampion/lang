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

// TestExternVariantMultiFieldMixedResultCustomProvider is the *general* multi-field
// `variant` *result* gate (docs/WIT-BRING-YOUR-OWN.md): a WIT variant whose cases
// carry more than one mixed-width / float payload — `variant ev { move(tuple<s32,
// s64>), spin(tuple<f32, f64>), stop }` — lifted into a Fern enum `Ev { Move(i32,
// i64), Spin(f32, f64), Stop }`.
//
// The canonical join is returned indirectly in the variant *memory* layout: a
// 1-byte discriminant, then the payload 8-aligned (a 64-bit field is present), so
// field0 @ offset 8 and field1 @ offset 16. The result wrapper copies each matched
// arm's fields from those canonical offsets into the Fern box by field width (i32
// for the 4-byte fields, i64 for the 8-byte ones), the float bits surviving the
// integer move (appendVariantResultStoreMultiField + ir's per-arm FieldAreaOffsets).
//
// `classify: func(n: s32) -> ev`: n<10 → stop, n<100 → move(5, 10), else →
// spin(2.0, 4.0). The Fern `match` recovers each arm's fields and checks them, so a
// wrong width / offset / coercion fails.
func TestExternVariantMultiFieldMixedResultCustomProvider(t *testing.T) {
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
	const iface = "interface src { variant ev { move(tuple<s32, s64>), spin(tuple<f32, f64>), stop } classify: func(n: s32) -> ev; }"
	if err := os.WriteFile(filepath.Join(provWit, "src.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export src; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	// classify returns the variant indirectly in the canonical memory layout: a
	// 24-byte area (disc:u8 @0, field0 @8, field1 @16) via cabi_realloc, 8-aligned
	// because a 64-bit field is present. move=0/spin=1/stop=2.
	if err := os.WriteFile(filepath.Join(dir, "prov_core.wat"), []byte(`(module
  (memory (export "memory") 1)
  (global $h (mut i32) (i32.const 1024))
  (func (export "cabi_realloc") (param $op i32) (param $os i32) (param $al i32) (param $ns i32) (result i32)
    (local $p i32)
    (local.set $p (global.get $h))
    (global.set $h (i32.add (global.get $h) (local.get $ns)))
    (local.get $p))
  (func (export "local:test/src@0.1.0#classify") (param $n i32) (result i32)
    (local $r i32)
    (local.set $r (call 0 (i32.const 0) (i32.const 0) (i32.const 8) (i32.const 24)))
    (if (i32.lt_s (local.get $n) (i32.const 10))
      (then (i32.store8 (local.get $r) (i32.const 2)))
      (else (if (i32.lt_s (local.get $n) (i32.const 100))
        (then
          (i32.store8 (local.get $r) (i32.const 0))
          (i32.store offset=8 (local.get $r) (i32.const 5))
          (i64.store offset=16 (local.get $r) (i64.const 10)))
        (else
          (i32.store8 (local.get $r) (i32.const 1))
          (f32.store offset=8 (local.get $r) (f32.const 2))
          (f64.store offset=16 (local.get $r) (f64.const 4))))))
    (local.get $r)))`), 0o644); err != nil {
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

	const want = "mfmr-ok"
	src := `enum Ev { Move(i32, i64), Spin(f32, f64), Stop }

@import("local:test/src@0.1.0", "classify")
function classify(n: i32): Ev;

function rank(n: i32): i32 {
	match (classify(n)) {
		Move(a, b) => { if (a == 5 && b == 10) { return 100; } else { return 1; } },
		Spin(f, d) => { if (f == 2.0 && d == 4.0) { return 200; } else { return 2; } },
		Stop => { return -1; },
	}
}

function main(): i32 {
	if (rank(5) == -1 && rank(50) == 100 && rank(200) == 200) { write("` + want + `"); } else { write("mfmr-bad"); }
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
