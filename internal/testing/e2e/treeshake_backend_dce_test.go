package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func emitBackendBundle(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return e2eharness.EmitAsmWithSelfHost(t, e2eharness.SelfHostCLI(t), e2eharness.TargetX86_64Linux, filepath.Join(dir, "main.fern"))
}

func assertBackends(t *testing.T, asm string, present, absent []string) {
	t.Helper()
	for _, isa := range present {
		if m := "MARKER-" + isa; !strings.Contains(asm, m) {
			t.Errorf("the driven backend lost %s", m)
		}
	}
	for _, isa := range absent {
		if m := "MARKER-" + isa; strings.Contains(asm, m) {
			t.Errorf("%s survived into a driver that never reaches it", m)
		}
	}
}

func TestUnreachedBackendModulesNotEmitted(t *testing.T) {
	backend := func(isa string) string {
		return `pub function emit_module(n: i32): i32 { print("MARKER-` + isa + `"); return n * 3 + 1; }`
	}
	asm := emitBackendBundle(t, map[string]string{
		"asm_x86.fern":   backend("x86"),
		"asm_arm64.fern": backend("arm64"),
		"asm_wasm.fern":  backend("wasm"),
		"main.fern": `import "./asm_x86";
import "./asm_arm64";
import "./asm_wasm";
function main(): i32 { return asm_x86.emit_module(7); }`,
	})
	assertBackends(t, asm, []string{"x86"}, []string{"arm64", "wasm"})
}

func TestUnreachedBackendsBehindDeadDynDispatchNotEmitted(t *testing.T) {
	backend := func(isa string) string {
		return `import "./backend";
struct ` + isa + `Emitter { id: i32 }
impl backend.Backend for ` + isa + `Emitter {
    function emit(self: Self, n: i32): i32 { print("MARKER-` + isa + `"); return n * 3 + 1; }
}
pub function make(): dyn backend.Backend { return ` + isa + `Emitter { id: 1 }; }`
	}
	files := map[string]string{
		"backend.fern":   `pub trait Backend { function emit(self: Self, n: i32): i32; }`,
		"asm_x86.fern":   backend("x86"),
		"asm_arm64.fern": backend("arm64"),
		"asm_wasm.fern":  backend("wasm"),
	}
	dispatch := `import "./backend";
import "./asm_x86";
import "./asm_arm64";
import "./asm_wasm";

function select_backend(target: string): i32 {
    let b: dyn backend.Backend = asm_x86.make();
    if (target == "arm64") { b = asm_arm64.make(); }
    if (target == "wasm") { b = asm_wasm.make(); }
    return b.emit(7);
}
`
	files["main.fern"] = dispatch + `
function main(): i32 { return 0; }`
	assertBackends(t, emitBackendBundle(t, files), nil, []string{"x86", "arm64", "wasm"})

	files["main.fern"] = dispatch + `
function main(): i32 { return select_backend("wasm"); }`
	assertBackends(t, emitBackendBundle(t, files), []string{"x86", "arm64", "wasm"}, nil)
}
