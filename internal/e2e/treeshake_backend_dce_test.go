// Whole-program dead-code elimination of unreached backend code in a
// merged multi-module bundle (#4114).
//
// A self-host driver links every backend it imports — x86-64, arm64, wasm
// emitters — but a given driver drives one of them. What the merged bundle
// must NOT keep is the emitters no path from `main` reaches. These tests
// pin that in the emitted artifact: an unreached module leaves no trace,
// and neither does one reachable only through a `dyn Trait` dispatch that
// is itself unreachable.
//
// The assertion is on a per-backend marker STRING rather than a function
// label, because a label is not evidence either way: a retained emitter
// that got inlined into its caller has no label of its own, and neither
// does one that was correctly culled. A string the emitter prints lands in
// `.rodata` whether or not the code around it was inlined, so its presence
// tracks the code being in the artifact.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// emitBackendBundle writes `files` into a temp dir and emits x86-64 asm
// for main.fern, returning the asm text.
func emitBackendBundle(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm
}

// assertBackends checks that every marker for `present` is in the asm and
// every marker for `absent` is not.
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

// The bundle imports three backends and `main` drives one. The other two
// must leave no trace in the artifact — that is the whole point of linking
// a multi-backend compiler into a single-target driver.
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

// The same bundle, but a backend is selected through a `dyn Backend`
// vtable and the selecting function is itself unreachable. A coercion
// site roots the impl methods its vtable points at — no call site names
// them — so the root has to die with the site. Rooting every coercion the
// checker recorded, dead sites included, kept all three backends (#4114).
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
	// Nothing calls select_backend, so no backend is reachable at all.
	dispatch := `import "./backend";
import "./asm_x86";
import "./asm_arm64";
import "./asm_wasm";

function select_backend(target: string): i32 {
    var b: dyn backend.Backend = asm_x86.make();
    if (target == "arm64") { b = asm_arm64.make(); }
    if (target == "wasm") { b = asm_wasm.make(); }
    return b.emit(7);
}
`
	files["main.fern"] = dispatch + `
function main(): i32 { return 0; }`
	assertBackends(t, emitBackendBundle(t, files), nil, []string{"x86", "arm64", "wasm"})

	// The gating must not cull a LIVE dispatch's impl methods: only the
	// vtable cell names them, so culling one leaves it pointing at a
	// dropped symbol (link failure). Same bundle, dispatch now reached.
	files["main.fern"] = dispatch + `
function main(): i32 { return select_backend("wasm"); }`
	assertBackends(t, emitBackendBundle(t, files), []string{"x86", "arm64", "wasm"}, nil)
}
