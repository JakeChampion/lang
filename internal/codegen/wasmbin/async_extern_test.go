package wasmbin

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/encode"
)

// i32 is the result/param type used by the proven `dep(): i32` async-import
// shape (docs/WASI-PREVIEW3-ASYNC-PLAN.md).
func i32Type() ast.Type { return ast.NumberType{Width: 32, Signed: true} }

// progCallingExtern builds a minimal IR program whose single function references
// `name` (so scanExternImports treats the extern as used) and declares `ext`.
func progCallingExtern(name string, ext *ir.ExternFunc) *ir.Program {
	return &ir.Program{
		Funcs:   []*ir.Func{{Ops: []ir.Op{{Str: name}}}},
		Externs: []*ir.ExternFunc{ext},
	}
}

// TestScanExternImportsAsyncScalar pins the wasmbin half of the colorless WASI
// Preview-3 async-import vertical (docs/WASI-PREVIEW3-ASYNC-PLAN.md): an
// `@import(...) async function dep(): i32` lowers to a raw core import carrying
// the `canon lower async` signature — `(retptr) -> i32 status` — plus a wrapper
// the Fern `dep()` call resolves to, so the await stays colorless. A plain
// (non-async) extern keeps the direct `() -> i32` import with no wrapper.
func TestScanExternImportsAsyncScalar(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:       "dep",
		Iface:      "test:dep/d",
		WITName:    "compute",
		ReturnType: i32Type(),
		Async:      true,
	}
	var in importNeeds
	var helpers runtimeNeeds
	specs, wrappers, err := scanExternImports(progCallingExtern("dep", ext), &in, &helpers)
	if err != nil {
		t.Fatalf("scanExternImports: %v", err)
	}

	// The async lower appends a return-area pointer and returns an i32 status:
	// the raw import is `(i32) -> (i32)`, NOT the sync `() -> (i32)`.
	raw, ok := specs["dep$import"]
	if !ok {
		t.Fatalf("missing raw async import spec %q; specs=%v", "dep$import", specs)
	}
	if raw.module != "test:dep/d" || raw.name != "compute" {
		t.Errorf("raw import (module,name) = (%q,%q), want (test:dep/d, compute)", raw.module, raw.name)
	}
	if len(raw.params) != 1 || raw.params[0] != encode.ValtypeI32 {
		t.Errorf("raw import params = %v, want [i32] (the retptr)", raw.params)
	}
	if len(raw.results) != 1 || raw.results[0] != encode.ValtypeI32 {
		t.Errorf("raw import results = %v, want [i32] (the status)", raw.results)
	}

	// The Fern name must NOT be a bare import — it resolves to the wrapper.
	if _, bare := specs["dep"]; bare {
		t.Errorf("async extern emitted a bare import for %q; expected only the wrapper", "dep")
	}
	w, ok := wrappers["dep"]
	if !ok {
		t.Fatalf("missing wrapper for %q; wrappers=%v", "dep", wrappers)
	}
	if len(w.params) != 0 {
		t.Errorf("wrapper params = %v, want [] (the source-level signature has no params)", w.params)
	}
	if len(w.results) != 1 || w.results[0] != encode.ValtypeI32 {
		t.Errorf("wrapper results = %v, want [i32]", w.results)
	}

	// The raw import (not the bare name) is what gets a core import slot, and
	// the wrapper pulls in the bump allocator for its return area.
	if !in.set["dep$import"] {
		t.Errorf("raw import %q not registered in importNeeds", "dep$import")
	}
	if in.set["dep"] {
		t.Errorf("bare name %q should not be registered as an import", "dep")
	}
	if !helpers.set["dep"] || !helpers.set["__fern_alloc"] {
		t.Errorf("helpers missing wrapper/allocator: %v", helpers.set)
	}
}

// TestScanExternImportsAsyncString pins the string-result case of the async
// import: `@import(...) async function fetch(): string` lowers to the same raw
// `(retptr) -> i32 status` async-lower import, plus a wrapper that lifts the
// return-area (ptr,len) into a Fern string. It pulls in __bytes_to_lang_string
// (the lift) and cabi_realloc (the lower's realloc option materialises the host
// bytes in this module's memory), and the wrapper's result is the Fern heap
// string pair (i32, i32). See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestScanExternImportsAsyncString(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:       "fetch",
		Iface:      "test:dep/d",
		WITName:    "fetch",
		ReturnType: ast.StringType{},
		Async:      true,
	}
	var in importNeeds
	var helpers runtimeNeeds
	specs, wrappers, err := scanExternImports(progCallingExtern("fetch", ext), &in, &helpers)
	if err != nil {
		t.Fatalf("scanExternImports: %v", err)
	}
	raw, ok := specs["fetch$import"]
	if !ok {
		t.Fatalf("missing raw async import spec %q; specs=%v", "fetch$import", specs)
	}
	if len(raw.params) != 1 || raw.params[0] != encode.ValtypeI32 {
		t.Errorf("raw import params = %v, want [i32] (the retptr)", raw.params)
	}
	if len(raw.results) != 1 || raw.results[0] != encode.ValtypeI32 {
		t.Errorf("raw import results = %v, want [i32] (the status)", raw.results)
	}
	w, ok := wrappers["fetch"]
	if !ok {
		t.Fatalf("missing wrapper for %q", "fetch")
	}
	if len(w.results) != 2 || w.results[0] != encode.ValtypeI32 || w.results[1] != encode.ValtypeI32 {
		t.Errorf("wrapper results = %v, want [i32 i32] (Fern heap string)", w.results)
	}
	for _, h := range []string{"fetch", "__fern_alloc", "__bytes_to_lang_string", "cabi_realloc"} {
		if !helpers.set[h] {
			t.Errorf("helper %q not pulled in for the async string import", h)
		}
	}
}

// TestScanExternImportsSyncStaysDirect is the contrast: without `async` the same
// extern lowers to a direct `() -> i32` import and no wrapper — proving the
// async lowering is gated strictly on ExternFunc.Async.
func TestScanExternImportsSyncStaysDirect(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:       "dep",
		Iface:      "test:dep/d",
		WITName:    "compute",
		ReturnType: i32Type(),
		Async:      false,
	}
	var in importNeeds
	var helpers runtimeNeeds
	specs, wrappers, err := scanExternImports(progCallingExtern("dep", ext), &in, &helpers)
	if err != nil {
		t.Fatalf("scanExternImports: %v", err)
	}
	direct, ok := specs["dep"]
	if !ok {
		t.Fatalf("missing direct import spec for %q; specs=%v", "dep", specs)
	}
	if len(direct.params) != 0 {
		t.Errorf("sync import params = %v, want [] (no retptr)", direct.params)
	}
	if len(direct.results) != 1 || direct.results[0] != encode.ValtypeI32 {
		t.Errorf("sync import results = %v, want [i32]", direct.results)
	}
	if _, ok := specs["dep$import"]; ok {
		t.Errorf("sync extern should not emit an async raw import %q", "dep$import")
	}
	if _, ok := wrappers["dep"]; ok {
		t.Errorf("sync extern should not emit a wrapper")
	}
}

// TestScanExternImportsAsyncRejectsMemParam pins the slice boundary: an async
// extern with a non-scalar (string/array/record) parameter is rejected with a
// clear error until that marshalling lands.
func TestScanExternImportsAsyncRejectsMemParam(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:       "dep",
		Iface:      "test:dep/d",
		WITName:    "compute",
		Params:     []ast.Param{{Name: "s", Type: ast.StringType{}}},
		ReturnType: i32Type(),
		Async:      true,
	}
	var in importNeeds
	var helpers runtimeNeeds
	if _, _, err := scanExternImports(progCallingExtern("dep", ext), &in, &helpers); err == nil {
		t.Fatalf("expected an error for an async extern with a string parameter")
	}
}
