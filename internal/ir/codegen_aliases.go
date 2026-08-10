package ir

// CodegenAliases maps a call target the IR lowering emits onto the stdlib
// function that actually implements it.
//
// The language does not yet support generic methods on a generic struct, so the
// Map / MapIter surface is declared in `internal/stdlib/core/map.fern` as
// concrete `_impl`-suffixed functions, while call sites keep the natural name
// (`map_new`, `__method_Map_get`). Every backend has to close that gap, and it
// has to close it in TWO places or the program does not link:
//
//   - at the call site, so `bl`/`call` names the function that exists;
//   - in the reachability walk, so dead-function elimination does not cull an
//     impl whose only reference is under its alias name. Pass this map as
//     LiveFunctionsWithAliases's `aliases` argument.
//
// Miss the first and the assembler reports a dangling label. Miss the second
// and the impl is culled, which reports the SAME dangling label from a
// completely different cause — that pairing is what made #6609 take a corpus
// sweep to diagnose, so treat the two as one change.
//
// These entries are target-INDEPENDENT: they are a fact about where the stdlib
// puts the Map runtime, not about any instruction set. That is why they live
// here rather than in a backend. Backend-SPECIFIC aliases (wasm routing `print`
// to its `__fern_print` runtime helper, say) do not belong in this map — a
// backend that has its own naming keeps its own table and merges this one in.
//
// NOT yet the single source of truth: internal/codegen/arm64 and
// internal/codegen/x86_64 still carry these pairs as `switch` arms with
// per-case side effects (`g.usesX = true`), so converting them is a separate
// refactor. If you add a Map method, add it here AND to those two switches
// until that lands.
var CodegenAliases = map[string]string{
	"map_new":             "map_new_impl",
	"__method_Map_len":    "__map_len_impl",
	"__method_Map_has":    "__map_has_impl",
	"__method_Map_get":    "__map_get_impl",
	"__method_Map_get_or": "__map_get_or_impl",
	"__method_Map_set":    "__map_set_impl",
	"__method_Map_delete": "__map_delete_impl",
	// Struct/enum (keyKind-3) keys: the `_keyed` variants take the key type's
	// derived hash/eq as trailing fn-value args (#2671).
	"__method_Map_has_keyed":    "__map_has_keyed_impl",
	"__method_Map_get_keyed":    "__map_get_keyed_impl",
	"__method_Map_get_or_keyed": "__map_get_or_keyed_impl",
	"__method_Map_set_keyed":    "__map_set_keyed_impl",
	"__method_Map_delete_keyed": "__map_delete_keyed_impl",
	"__method_Map_clear":        "__map_clear_impl",
	"__method_Map_keys":         "__map_keys_impl",
	"__method_Map_values":       "__map_values_impl",
	"__method_Map_iter":         "__map_iter_impl",
	"__method_MapIter_has_next": "__mapiter_has_next_impl",
	"__method_MapIter_key":      "__mapiter_key_impl",
	"__method_MapIter_value":    "__mapiter_value_impl",
	"__method_MapIter_advance":  "__mapiter_advance_impl",
}

// CodegenAlias resolves one call target through CodegenAliases, returning the
// name unchanged when it has no alias. Use it at the point a callee becomes a
// label, so a backend cannot apply the rewrite on some paths and not others.
func CodegenAlias(name string) string {
	if dst, ok := CodegenAliases[name]; ok {
		return dst
	}
	return name
}
