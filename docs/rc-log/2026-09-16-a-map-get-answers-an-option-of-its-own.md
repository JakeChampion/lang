# 2026-09-16 — a map get answers an Option of its own

Three small things the corpus asked for after the destructuring leaves.

## `Map.get`

`unsupported call target` sat at 21 sites and named nothing. With the
receiver type and method in the refusal, nine were `Map[K, V].get`, five
were the rest of the map surface (`without`, `keys`, `values`, `iter`,
`cleared`), three `i32[].to_json` and `string[].join`, and one a `dyn`
receiver.

A `get` is now the semantic `map_get` (kind -30): it borrows the map and
the key, as `has` does, and hands the frame a fresh `Option[V]` box the
runtime allocates (`__fern_map_get`, op 127 on every backend), which the
plan treats as any handed-out unit and the physical lowering releases as
any Option. The runtime copies the column's entry into the box without a
retain, so the value column must be one the map frees by freeing the
buffer alone — an integer or a boolean — and a get over a string or
string-array column is refused ("map get of a counted value column")
rather than answering a payload two owners would release. The one such
site (`url_codec`, `Map[string, string[]]`) waits on that retain.

The seven admitted cases (`alloc_flat_map_get`, `if_let`, `map_i32`,
`map_string_key`, `none_branch`, `some_branch`, `string_key`) produce
whole and match native's output under the leak check.

## The defer flags are booleans

`lower_defers_module` declared its per-defer flags `__dfa<i>` and the
`?`-edge markers `__dfa_tryall` / `__dfa_tryerr` as `i32`, armed them
with `1` and tested them as conditions, which native's E008 rejects in
user code ("if condition must be boolean, got i32"). The semantic lowering
refused every function with a `defer` as "condition type", 13 sites, and
said nothing about the defer. The flags are booleans now, declared as
such, armed with `true` and cleared with `false`; `dl_arm_index` reads
the boolean. The AST lowering is unchanged by it — a boolean and an i32
are the same slot — and the defer conformance cases pass on the
self-host fixture legs.

With the flags typed, the same functions refuse for what they are: a
deferred action in a loop body is replayed at function exit, outside the
scope of the binding it reads (`unbound name … $binding$3$w`), which the
AST lowering serves from a function-level slot and this boundary's scoped
bindings cannot. That is the defer leaf proper, and it is not this change.

## Pinned

`TestSelfHostSSAPhysicalRC` lowers a get over an owned integer map:
the op is emitted, the map is freed, nothing is retained.
`TestSelfHostSemanticSourceRC` runs `map_get_hit` (a string-keyed get
matched and if-let'd, hit and miss) and `map_get_int` on all four targets
under the leak check; the print golden carries `map_get_line`.
