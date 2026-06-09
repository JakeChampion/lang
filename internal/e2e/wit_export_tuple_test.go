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

// TestExportTupleResultRunsViaConsumer is the P6 tuple result export gate
// (docs/WIT-BRING-YOUR-OWN.md): a structural composite export. A Fern reactor
// `@export make_pair(a, b): (i32, i32)` returns a tuple value; the canonical
// tuple flattens to > 1 core value, so it returns indirectly, and the wasmbin
// wrapper copies each element from the tuple value into the canonical return
// area. The composer emits the `tuple<s32, s32>` component type and lifts via
// memory. A Fern consumer `@import`s it and reads `p.0`/`p.1` — linked with
// `wasm-tools compose` and run under wasmtime.
func TestExportTupleResultRunsViaConsumer(t *testing.T) {
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

	const iface = "interface pairs { make-pair: func(a: s32, b: s32) -> tuple<s32, s32>; }"

	// --- exporter: Fern reactor `@export make_pair(a, b): (i32, i32)`. ---
	expWit := filepath.Join(dir, "expwit")
	if err := os.MkdirAll(expWit, 0o755); err != nil {
		t.Fatalf("mkdir expwit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(expWit, "world.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld m {\n  export pairs;\n}\n"), 0o644); err != nil {
		t.Fatalf("write exporter wit: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "ee.wat", "(module)"), "-o", filepath.Join(dir, "ee.wasm"))
	run(wasmtools, "component", "embed", expWit, "-w", "m", filepath.Join(dir, "ee.wasm"), "-o", filepath.Join(dir, "eembed.wasm"))
	expEmbed, err := os.ReadFile(filepath.Join(dir, "eembed.wasm"))
	if err != nil {
		t.Fatalf("read exporter embed: %v", err)
	}
	expWorld, err := componenttype.DecodeWorldBytes(extractComponentType(t, expEmbed))
	if err != nil {
		t.Fatalf("DecodeWorldBytes (exporter): %v", err)
	}
	expSrc := `@export("local:test/pairs@0.1.0", "make-pair")
function make_pair(a: i32, b: i32): (i32, i32) { return (a + 1, b * 2); }`
	expPath := filepath.Join(dir, "exporter.fern")
	if err := os.WriteFile(expPath, []byte(expSrc), 0o644); err != nil {
		t.Fatalf("write exporter prog: %v", err)
	}
	expInfo, expProg := loadCheckMono(t, expPath)
	expCore, err := wasmbin.BuildWithOptions(expProg, expInfo, wasmbin.BuildOptions{ForceMemorySection: true, Preview2WASI: true})
	if err != nil {
		t.Fatalf("build exporter core: %v", err)
	}
	if !bytes.Contains(expCore, []byte("local:test/pairs@0.1.0#make-pair")) {
		t.Fatalf("exporter core missing the surfaced @export export")
	}
	expComp, err := component.ComposeExportsFromWorld(expCore, expWorld)
	if err != nil {
		t.Fatalf("ComposeExportsFromWorld (Fern tuple result): %v", err)
	}
	exporter := filepath.Join(dir, "exporter.wasm")
	if err := os.WriteFile(exporter, expComp, 0o644); err != nil {
		t.Fatalf("write exporter component: %v", err)
	}
	run(wasmtools, "validate", exporter)

	// --- consumer: Fern, @imports make-pair and reads p.0/p.1. ---
	userWit := filepath.Join(dir, "userwit")
	if err := os.MkdirAll(userWit, 0o755); err != nil {
		t.Fatalf("mkdir userwit: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
	if err := os.MkdirAll(filepath.Join(userWit, "deps", "test"), 0o755); err != nil {
		t.Fatalf("mkdir deps/test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "deps", "test", "pairs.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\n"), 0o644); err != nil {
		t.Fatalf("write pairs dep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import local:test/pairs@0.1.0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write user world: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "ue.wat", "(module)"), "-o", filepath.Join(dir, "ue.wasm"))
	run(wasmtools, "component", "embed", userWit, "-w", "u", filepath.Join(dir, "ue.wasm"), "-o", filepath.Join(dir, "uembed.wasm"))
	userEmbed, err := os.ReadFile(filepath.Join(dir, "uembed.wasm"))
	if err != nil {
		t.Fatalf("read user embed: %v", err)
	}
	userWorld, err := componenttype.DecodeWorldBytes(extractComponentType(t, userEmbed))
	if err != nil {
		t.Fatalf("DecodeWorldBytes (user): %v", err)
	}
	const want = "tup-ok"
	userSrc := `@import("local:test/pairs@0.1.0", "make-pair")
function make_pair(a: i32, b: i32): (i32, i32);

function main(): i32 {
	var p: (i32, i32) = make_pair(10, 21);
	if (p.0 == 11 && p.1 == 42) { write("` + want + `"); } else { write("tup-bad"); }
	return 0;
}`
	userPath := filepath.Join(dir, "consumer.fern")
	if err := os.WriteFile(userPath, []byte(userSrc), 0o644); err != nil {
		t.Fatalf("write consumer prog: %v", err)
	}
	userInfo, userProg := loadCheckMono(t, userPath)
	userCore, err := wasmbin.BuildWithOptions(userProg, userInfo, wasmbin.BuildOptions{
		ForceMemorySection: true, Preview2WASI: true, SynthCliRun: true, CliRunResult: true,
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
