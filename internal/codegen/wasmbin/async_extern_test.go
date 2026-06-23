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

// TestScanExternImportsAsyncStringParam covers an async import that takes a
// single string ARGUMENT with a scalar result — `@import async function
// send(s: string): i32`. It lowers to the raw `(ptr, len, retptr) -> i32 status`
// import plus a wrapper that normalises the string (pulling in __fern_str_len /
// __fern_str_byte / __fern_alloc) and reads the scalar result. (No cabi_realloc
// on the consumer side — the param bytes are the caller's.)
func TestScanExternImportsAsyncStringParam(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:       "send",
		Iface:      "test:dep/d",
		WITName:    "send",
		Params:     []ast.Param{{Name: "s", Type: ast.StringType{}}},
		ReturnType: i32Type(),
		Async:      true,
	}
	var in importNeeds
	var helpers runtimeNeeds
	specs, wrappers, err := scanExternImports(progCallingExtern("send", ext), &in, &helpers)
	if err != nil {
		t.Fatalf("scanExternImports: %v", err)
	}
	raw, ok := specs["send$import"]
	if !ok {
		t.Fatalf("missing raw async import spec %q; specs=%v", "send$import", specs)
	}
	if len(raw.params) != 3 || raw.params[0] != encode.ValtypeI32 || raw.params[2] != encode.ValtypeI32 {
		t.Errorf("raw import params = %v, want [i32 i32 i32] (ptr, len, retptr)", raw.params)
	}
	if len(raw.results) != 1 || raw.results[0] != encode.ValtypeI32 {
		t.Errorf("raw import results = %v, want [i32] (status)", raw.results)
	}
	w, ok := wrappers["send"]
	if !ok {
		t.Fatalf("missing wrapper for %q", "send")
	}
	if len(w.params) != 2 {
		t.Errorf("wrapper params = %v, want 2 slots (string data,len)", w.params)
	}
	for _, h := range []string{"send", "__fern_alloc", "__fern_str_len", "__fern_str_byte"} {
		if !helpers.set[h] {
			t.Errorf("helper %q not pulled in for the async string-param import", h)
		}
	}
}

// TestScanExternImportsAsyncArrayParam covers an async import that takes a single
// numeric-array ARGUMENT with a scalar result — `@import async function
// recv(xs: u8[]): i32`. Like the string-param case it lowers to the raw
// `(ptr, len, retptr) -> i32 status` import plus a wrapper, but the wrapper's
// source-level signature is a single Fern slot (the element pointer) and it pulls
// in only __fern_alloc (no string-normalisation helpers — a numeric array is
// already canonical, count at ptr-4). No cabi_realloc on the consumer side: the
// param bytes are the caller's.
func TestScanExternImportsAsyncArrayParam(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:       "recv",
		Iface:      "test:dep/d",
		WITName:    "recv",
		Params:     []ast.Param{{Name: "xs", Type: ast.ArrayType{Elem: ast.NumberType{Width: 8}}}},
		ReturnType: i32Type(),
		Async:      true,
	}
	var in importNeeds
	var helpers runtimeNeeds
	specs, wrappers, err := scanExternImports(progCallingExtern("recv", ext), &in, &helpers)
	if err != nil {
		t.Fatalf("scanExternImports: %v", err)
	}
	raw, ok := specs["recv$import"]
	if !ok {
		t.Fatalf("missing raw async import spec %q; specs=%v", "recv$import", specs)
	}
	if len(raw.params) != 3 || raw.params[0] != encode.ValtypeI32 || raw.params[2] != encode.ValtypeI32 {
		t.Errorf("raw import params = %v, want [i32 i32 i32] (ptr, len, retptr)", raw.params)
	}
	if len(raw.results) != 1 || raw.results[0] != encode.ValtypeI32 {
		t.Errorf("raw import results = %v, want [i32] (status)", raw.results)
	}
	w, ok := wrappers["recv"]
	if !ok {
		t.Fatalf("missing wrapper for %q", "recv")
	}
	if len(w.params) != 1 || w.params[0] != encode.ValtypeI32 {
		t.Errorf("wrapper params = %v, want 1 slot (the element pointer)", w.params)
	}
	if len(w.results) != 1 || w.results[0] != encode.ValtypeI32 {
		t.Errorf("wrapper results = %v, want [i32]", w.results)
	}
	if !helpers.set["recv"] || !helpers.set["__fern_alloc"] {
		t.Errorf("helpers missing wrapper/allocator: %v", helpers.set)
	}
	// A numeric array needs NO string-normalisation helpers.
	if helpers.set["__fern_str_len"] || helpers.set["__fern_str_byte"] {
		t.Errorf("array-param wrapper should not pull in string-normalisation helpers: %v", helpers.set)
	}
}

// TestScanExternImportsAsyncMixedMultiParam covers the multi-arg edge-handler
// shape — `@import async function fetch(url: string, timeout: i32): i32`. The raw
// import flattens to the canonical `(ptr, len, i32, retptr) -> i32 status` and
// the wrapper takes 3 Fern slots (string data,len + the scalar). A string in the
// mix pulls in the string-normalisation helpers.
func TestScanExternImportsAsyncMixedMultiParam(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:    "fetch",
		Iface:   "test:dep/d",
		WITName: "fetch",
		Params: []ast.Param{
			{Name: "url", Type: ast.StringType{}},
			{Name: "timeout", Type: i32Type()},
		},
		ReturnType: i32Type(),
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
	want := []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32} // ptr,len,timeout,retptr
	if len(raw.params) != len(want) {
		t.Errorf("raw import params = %v, want %v (ptr, len, timeout, retptr)", raw.params, want)
	}
	if len(raw.results) != 1 || raw.results[0] != encode.ValtypeI32 {
		t.Errorf("raw import results = %v, want [i32] (status)", raw.results)
	}
	w, ok := wrappers["fetch"]
	if !ok {
		t.Fatalf("missing wrapper for %q", "fetch")
	}
	if len(w.params) != 3 {
		t.Errorf("wrapper params = %v, want 3 slots (string data,len + scalar)", w.params)
	}
	for _, h := range []string{"fetch", "__fern_alloc", "__fern_str_len", "__fern_str_byte"} {
		if !helpers.set[h] {
			t.Errorf("helper %q not pulled in for the mixed async multi-param import", h)
		}
	}
}

// TestScanExternImportsAsyncMemParamStringResult covers the composite-param-AND-
// result quadrant — `@import async function echo(s: string): string`. The raw
// import flattens to `(ptr, len, retptr) -> i32 status`, the wrapper's result is
// the Fern heap string pair (i32, i32), and it pulls in the string-lift helpers
// (__bytes_to_lang_string) AND cabi_realloc (the lower's realloc materialises the
// result bytes in this module's memory) on top of the string-param normalisation
// helpers.
func TestScanExternImportsAsyncMemParamStringResult(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:       "echo",
		Iface:      "test:dep/d",
		WITName:    "echo",
		Params:     []ast.Param{{Name: "s", Type: ast.StringType{}}},
		ReturnType: ast.StringType{},
		Async:      true,
	}
	var in importNeeds
	var helpers runtimeNeeds
	_, wrappers, err := scanExternImports(progCallingExtern("echo", ext), &in, &helpers)
	if err != nil {
		t.Fatalf("scanExternImports: %v", err)
	}
	w, ok := wrappers["echo"]
	if !ok {
		t.Fatalf("missing wrapper for %q", "echo")
	}
	if len(w.results) != 2 || w.results[0] != encode.ValtypeI32 || w.results[1] != encode.ValtypeI32 {
		t.Errorf("wrapper results = %v, want [i32 i32] (Fern heap string)", w.results)
	}
	for _, h := range []string{"echo", "__fern_alloc", "__fern_str_len", "__fern_str_byte", "__bytes_to_lang_string", "cabi_realloc"} {
		if !helpers.set[h] {
			t.Errorf("helper %q not pulled in for the async string-param/string-result import", h)
		}
	}
}

// TestScanExternImportsAsyncStreamParamRejectedPending pins the slice boundary
// for the colorless stream[T] PARAMETER: the type parses + type-checks (the
// checker rewrites the param to T[] and records StreamParamElems), but until the
// stream-produce wrapper lands, wasmbin rejects it rather than mislowering the
// rewritten T[] as a single list block. See docs/STREAM-TYPE-SURFACE.md.
func TestScanExternImportsAsyncStreamParamRejectedPending(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:             "sink",
		Iface:            "test:dep/d",
		WITName:          "sink",
		Params:           []ast.Param{{Name: "s", Type: ast.ArrayType{Elem: ast.NumberType{Width: 8}}}}, // rewritten u8[]
		StreamParamElems: map[int]ast.Type{0: ast.NumberType{Width: 8}},                                 // stream[u8] param
		ReturnType:       i32Type(),
		Async:            true,
	}
	var in importNeeds
	var helpers runtimeNeeds
	if _, _, err := scanExternImports(progCallingExtern("sink", ext), &in, &helpers); err == nil {
		t.Fatalf("expected an error for an async stream[T] parameter (produce-wrapper codegen pending)")
	}
}

// TestScanExternImportsAsyncRecordParam covers a record/tuple ARGUMENT to an
// async import — `@import async function add(p: (i32, i32)): i32`. With its
// flattened field layout resolved (ex.ParamRecords, as IR lowering sets it), the
// async path accepts it exactly like the sync path: the raw import flattens the
// tuple to its element core types `(i32, i32, retptr) -> i32 status`, the wrapper
// takes the single Fern tuple slot, and no string/realloc helpers are pulled in.
func TestScanExternImportsAsyncRecordParam(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:    "add",
		Iface:   "test:dep/d",
		WITName: "add",
		Params: []ast.Param{
			{Name: "p", Type: ast.TupleType{Elems: []ast.Type{i32Type(), i32Type()}}},
		},
		ReturnType: i32Type(),
		Async:      true,
		// Flattened field layout the IR lowering would precompute for the tuple.
		ParamRecords: map[int][]ir.ExternRecordField{
			0: {
				{Offset: 0, Type: i32Type()},
				{Offset: 4, Type: i32Type()},
			},
		},
	}
	var in importNeeds
	var helpers runtimeNeeds
	specs, wrappers, err := scanExternImports(progCallingExtern("add", ext), &in, &helpers)
	if err != nil {
		t.Fatalf("scanExternImports: %v", err)
	}
	raw, ok := specs["add$import"]
	if !ok {
		t.Fatalf("missing raw async import spec %q; specs=%v", "add$import", specs)
	}
	want := []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32} // x, y, retptr
	if len(raw.params) != len(want) {
		t.Errorf("raw import params = %v, want %v (x, y, retptr)", raw.params, want)
	}
	w, ok := wrappers["add"]
	if !ok {
		t.Fatalf("missing wrapper for %q", "add")
	}
	if len(w.params) != 1 || w.params[0] != encode.ValtypeI32 {
		t.Errorf("wrapper params = %v, want 1 slot (the Fern tuple value)", w.params)
	}
	if helpers.set["__fern_str_len"] || helpers.set["cabi_realloc"] {
		t.Errorf("a scalar-tuple param/result should pull in no string/realloc helpers: %v", helpers.set)
	}
}

// TestScanExternImportsAsyncStreamResult covers the colorless stream[T] result
// collect-wrapper (docs/STREAM-TYPE-SURFACE.md): an `@import async function
// body(): stream[u8]` (the checker rewrote the result to u8[] and set
// StreamResultElem). The raw import is the async lower `(retptr) -> i32 status`
// (delivering the stream readable handle), the wrapper's result is the Fern array
// element pointer (i32), and it registers the waitable intrinsics PLUS
// stream.read / stream.drop-readable.
func TestScanExternImportsAsyncStreamResult(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:             "body",
		Iface:            "test:dep/d",
		WITName:          "body",
		ReturnType:       ast.ArrayType{Elem: ast.NumberType{Width: 8}}, // rewritten u8[]
		StreamResultElem: ast.NumberType{Width: 8},                      // stream[u8]
		Async:            true,
	}
	var in importNeeds
	var helpers runtimeNeeds
	specs, wrappers, err := scanExternImports(progCallingExtern("body", ext), &in, &helpers)
	if err != nil {
		t.Fatalf("scanExternImports: %v", err)
	}
	raw, ok := specs["body$import"]
	if !ok {
		t.Fatalf("missing raw async import spec %q; specs=%v", "body$import", specs)
	}
	if len(raw.params) != 1 || raw.params[0] != encode.ValtypeI32 {
		t.Errorf("raw import params = %v, want [i32] (the retptr)", raw.params)
	}
	if len(raw.results) != 1 || raw.results[0] != encode.ValtypeI32 {
		t.Errorf("raw import results = %v, want [i32] (status)", raw.results)
	}
	w, ok := wrappers["body"]
	if !ok {
		t.Fatalf("missing wrapper for %q", "body")
	}
	if len(w.results) != 1 || w.results[0] != encode.ValtypeI32 {
		t.Errorf("wrapper results = %v, want [i32] (Fern array element pointer)", w.results)
	}
	for _, n := range []string{"async_ws_new", "async_w_join", "async_ws_wait", "async_ws_drop", "async_stream_read", "async_stream_drop_readable", "body$import"} {
		if !in.set[n] {
			t.Errorf("intrinsic/import %q not registered for the async stream result", n)
		}
	}
}

// TestScanExternImportsAsyncRejectsCompositeResult pins the remaining boundary:
// a composite (record/tuple) RESULT from an async import is still rejected — only
// a scalar / string / numeric-array result is supported.
func TestScanExternImportsAsyncRejectsCompositeResult(t *testing.T) {
	ext := &ir.ExternFunc{
		Name:       "mk",
		Iface:      "test:dep/d",
		WITName:    "mk",
		ReturnType: ast.TupleType{Elems: []ast.Type{i32Type(), i32Type()}},
		Async:      true,
	}
	var in importNeeds
	var helpers runtimeNeeds
	if _, _, err := scanExternImports(progCallingExtern("mk", ext), &in, &helpers); err == nil {
		t.Fatalf("expected an error for an async extern with a composite (tuple) result")
	}
}
