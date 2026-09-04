package wasmbin

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// Every callee the IR can emit on this target must have somewhere to land.
//
// TestProvidedSigsAgreeWithWasmRuntime checks the SHAPE of the helpers
// wasmbin already implements, and `continue`s past every name it does not
// know — so a builtin wasmbin has never heard of passes it silently. That
// is exactly how the strbuf family (#7947) reached codegen unimplemented
// on every wasm build: core builtins, so E066 never refuses them, and no
// spec, no scan case and no alias to catch them. The failure surfaced as
// `unknown callee "strbuf_reset"` from emitOp, at the far end of a
// whole-program compile.
//
// This gate asks the other question: for each provided callee, does the
// emitter have a target for it at all?
//
// The two exemptions below are the only honest ones. Anything else that
// fails here is a missing lowering, and belongs in the emitter rather than
// in a list.
//
//   - REFUSED EARLIER. `internal/platforms` withholds the callee on
//     wasm32-wasi, so E066 rejects the program before codegen and there is
//     nothing for the backend to emit.
//   - NEVER REACHES CODEGEN. IR lowering rewrites the call into ops or
//     inlines it, so no OpCallDirect with this name survives to emitOp.
//
// A third group is neither: genuinely missing lowerings that predate this
// gate. They are listed apart, under the issue that owns them, so the
// count can only go down.

// providedRefusedByPlatform are callees `internal/platforms` does not offer
// on wasm32-wasi. Each is checked against platforms itself below rather
// than trusted, so an entry that stops being refused fails the test.
var providedRefusedByPlatform = map[string]bool{
	"subprocess":   true,
	"proc_fork":    true,
	"proc_waitpid": true,
	"proc_exec":    true,
	// The arena mark/release pair: a bump-arena discipline the wasm
	// allocator does not implement.
	"__heap_mark":       true,
	"__heap_release_to": true,
	// `pollfd` — a timer expressed as a file descriptor to poll. Wasm's
	// readiness surface is wasi:io/poll pollables, which wasm_timer_pollable
	// already serves.
	"timer_fd": true,
	// `fsmode` — permission bits on a filesystem entry, which neither
	// preview1 nor the component-model filesystem has (#6133).
	"write_file_exec": true,
	// `cabi` — a C calling convention to hand a function pointer to.
	"__c_call0": true, "__c_call0_f32": true, "__c_call0_f64": true,
	"__c_call1": true, "__c_call1_f32": true, "__c_call1_f64": true,
	"__c_call2": true, "__c_call2_f32": true, "__c_call2_f64": true,
	"__c_call3": true, "__c_call3_f32": true, "__c_call3_f64": true,
	"__c_call4": true, "__c_call4_f32": true, "__c_call4_f64": true,
}

// providedNeverReachesCodegen are callees IR lowering consumes before the
// backend sees them: the name appears in a call expression, and what comes
// out of Lower is a dedicated op, an inlined sequence, or a rewritten
// callee. No OpCallDirect carrying these names reaches emitOp.
var providedNeverReachesCodegen = map[string]bool{
	// Bit intrinsics — dedicated IR ops (OpClz / OpCtz / OpPopcount),
	// emitted as the wasm instructions of the same name.
	"__clz32": true, "__clz64": true, "__ctz32": true, "__ctz64": true,
	"__popcount32": true, "__popcount64": true,
	// Float bit-casts — OpBitcast, a wasm reinterpret instruction.
	"f32_bits": true, "f32_from_bits": true,
	"f64_bits": true, "f64_from_bits": true,
	// Byte search and counting — lowered to the `__fern_`-prefixed
	// helpers, which this table does list and wasmbin does implement.
	"__memchr": true, "__rmemchr": true, "__count_byte": true,
	"__map_hash_seed": true, "__heap_bump_bytes": true,
	"__arr_push_shared_bytes": true, "__arr_push_shared_count": true,
	"__rc_underflow_count": true,
	// The rc trio — OpRcInc / OpRcDec (inline fast path, or the
	// `__fern_rc_*` helpers) and the raw count read.
	"__rc_get": true, "__rc_inc": true, "__rc_dec": true,
	// Cell construction and unchecked slicing are struct/slice
	// materialisation in the lowering, not calls.
	"cell_new": true, "slice_unchecked": true,
	// The method builtins the lowering opens up rather than calls: the
	// three lengths become a load (OpStrLen, or the count word ahead of
	// the payload), the Cell pair a load and a store, and the array
	// mutators the `__fern_arr_*` helpers this table lists separately.
	"__method_string_len": true, "__method_Array_len": true,
	"__method_slice_len": true,
	"__method_Cell_get":  true, "__method_Cell_set": true,
	"__method_Array_push": true, "__method_Array_set": true,
}

// providedMissingLowering are callees wasmbin genuinely cannot emit. Each
// fails the same way strbuf did — `unknown callee` out of emitOp, on a
// program that type-checks and passes E066. This list may only shrink, and
// it is empty: sleep_ms was the last entry (#7947).
var providedMissingLowering = map[string]bool{}

// loweringTarget resolves one callee the way emitOp does — through
// callDirectAlias — and reports where the emitter would find it.
func loweringTarget(name string) (target string, kind string, ok bool) {
	target = callDirectAlias(name)
	if _, isHelper := runtimeHelperSpecs[target]; isHelper {
		return target, "runtime helper", true
	}
	// A codegen alias names a stdlib `.fern` function that exists as an
	// ordinary IR function; wasmbin.Build threads CallDirectAliases into
	// the reachability walk so the impl survives dead-code elimination.
	if _, isImpl := ir.CodegenAliases[name]; isImpl {
		return target, "stdlib impl", true
	}
	return target, "", false
}

func TestEveryProvidedCalleeHasAWasmLowering(t *testing.T) {
	var missing []string
	for _, name := range ir.ProvidedCalleeNames() {
		switch {
		case providedRefusedByPlatform[name],
			providedNeverReachesCodegen[name],
			providedMissingLowering[name]:
			continue
		}
		if _, _, ok := loweringTarget(name); !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d provided callee(s) have no wasm lowering: %v\n"+
			"Each needs a runtimeHelperSpecs entry (plus a scanRuntimeHelpers case so it "+
			"lands in helpers.order) or a CallDirectAliases target. Adding a name to one "+
			"of the exemption maps in this file is only correct if E066 refuses it on "+
			"wasm32-wasi or IR lowering rewrites it before emitOp.", len(missing), missing)
	}

	// Exact in the other direction too, so a fix cannot leave the table
	// stale. Without this the list only ever grows in effect: implementing
	// write_file_exec and forgetting the row here still passes, and the next
	// reader takes a solved gap for an open one. Same reason the rc corpus
	// leak gate fails a case that leaks LESS than its baseline.
	var fixed []string
	for name := range providedMissingLowering {
		if _, _, ok := loweringTarget(name); ok {
			fixed = append(fixed, name)
		}
	}
	sort.Strings(fixed)
	if len(fixed) > 0 {
		t.Errorf("%d callee(s) in providedMissingLowering now HAVE a wasm lowering: %v\n"+
			"Delete the row(s) — the list is the live gap list for #7947 and may only "+
			"shrink, so a stale entry hides that the gap is closed.", len(fixed), fixed)
	}
}

// A spec alone is not enough: a helper with no scanRuntimeHelpers case
// never reaches helpers.order, so EmitWithOptions never assigns it a
// funcidx and the call fails with the same "unknown callee" as a helper
// that was never written. This walks the inclusion path a real call takes.
//
// Scoped to the builtins a wasm program may actually call, because those
// are the ones that reach the backend as an OpCallDirect carrying their
// own name. The internal helpers in the same table are reached instead
// through the dedicated ops that need them — OpStrLen pulls in
// __fern_str_len, OpDivS the guarded division — so a synthetic call by
// name says nothing about whether they are emittable.
func TestProvidedCalleeHelpersAreScanned(t *testing.T) {
	for _, name := range ir.ProvidedCalleeNames() {
		switch {
		case providedRefusedByPlatform[name],
			providedNeverReachesCodegen[name],
			providedMissingLowering[name]:
			continue
		}
		if !userCallableOnWasm(name) {
			continue
		}
		target, kind, ok := loweringTarget(name)
		if !ok || kind != "runtime helper" {
			continue
		}
		prog := &ir.Program{Funcs: []*ir.Func{{
			Name: "main",
			Ops:  []ir.Op{{Kind: ir.OpCallDirect, Str: name}},
		}}}
		helpers := scanRuntimeHelpers(prog, EmitOptions{})
		if !helpers.set[target] {
			t.Errorf("a call to %q does not pull %q into the helper set — "+
				"scanRuntimeHelpers has no case for it, so it gets no funcidx",
				name, target)
		}
	}
}

// platformProvidesOnWasm reports whether a wasm32-wasi program may call the
// builtin. An ungated name is either core or not user-callable at all, and
// E066 refuses neither.
func platformProvidesOnWasm(name string) bool {
	capability, gated := platforms.GatedBuiltin(name)
	if !gated {
		return true
	}
	return platforms.HasCapability("wasm32-wasi", capability)
}

// userCallableOnWasm reports whether the name is a builtin a wasm32-wasi
// program may write, as opposed to an internal runtime helper that only
// the lowering names.
func userCallableOnWasm(name string) bool {
	if platforms.CoreBuiltin(name) {
		return true
	}
	_, gated := platforms.GatedBuiltin(name)
	return gated && platformProvidesOnWasm(name)
}

// The platform exemptions are a claim about another package, so they are
// checked rather than believed: a name platforms starts offering on
// wasm32-wasi has to leave the list and grow a real lowering.
func TestPlatformExemptionsAreReallyRefused(t *testing.T) {
	for name := range providedRefusedByPlatform {
		if platformProvidesOnWasm(name) {
			t.Errorf("%q is listed as refused on wasm32-wasi but internal/platforms offers it — "+
				"it needs a real lowering, not an exemption", name)
		}
	}
}

// A strbuf-only program: the smallest one whose helper set is the string
// builder plus what it calls, so a missing dependency edge has nothing
// else to hide behind.
func TestStrbufOnlyProgramIsValidWasm(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `function main(): i32 {
    strbuf_reset();
    strbuf_append("a longer piece, past the inline form");
    strbuf_append("ab");
    return strbuf_take().len();
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := Build(prog, info)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := filepath.Join(t.TempDir(), "strbuf.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("a strbuf-only module must be valid wasm: %v\n%s", err, out)
	}
}
